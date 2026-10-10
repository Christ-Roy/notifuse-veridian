# 62. « Pourquoi ça n'envoie pas » (lot 1 de la fiche 59)

Date : 10/10/2026. Base : `origin/veridian` après la fiche 58 (prod `v61.0-veridian.95039722`). Livré en `v62.0`. Conception : fiche 59 (sections 3.2, 4, 7, 8). Demande de Robert (10/10) : voir en une page pourquoi chaque groupe d'entrées attend, avec la valeur, la limite et le verdict de chaque gate, sans faire gonfler la base.

## Ce qui est livré

1. **La raison d'un report est persistée sur l'entrée de file** (`email_queue`, V62). Écrite dans le MÊME `UPDATE` que le report (`EmailQueueRepository.SetDeferral`, `SetNextRetry` conservée comme repli) : coût ajouté nul. Colonnes : `node_id`, `defer_reason`, `defer_detail`, `defer_profile`, `deferred_at`, `defer_count`, `first_examined_at`, `last_examined_at`, `decision_logged_at`.
2. **Chaque gate rend un verdict structuré** (`Gate, Value, Limit, Delay, Blocked`) : `veridian_gate_verdict.go` et une variante `…Verdict` par gate (exclusion, réputation, débit de classe, plafond journalier, plafond par adresse, fenêtre). L'ancienne fonction `(délai, bool)` devient un wrapper : un test de parité exige le même résultat. **Zéro changement de décision.**
3. **Le masquage est supprimé** : la raison d'un report n'est plus celle du premier gate qui refuse mais celle du gate au délai le plus long chez le candidat le plus prometteur. Test de non-régression : samedi, fenêtre fermée + débit de classe épuisé donne `window_closed` (réouverture lundi), pas `class_rate`.
4. **Journal des décisions** : table `veridian_send_decisions` (V62), niveau par workspace `veridian_decision_log_level` (`off`, `transitions` par défaut, `all`). Routes `GET|POST /api/veridian/decisions.list`, `queue.explain`, `POST queue.recompute` (permissions `automations:read` / `automations:write`).
5. **CLI** (embarqué et skill `notifuse-cli`) : `notifuse queue:explain <ws> [--group-by …] [--entry ID]`, `notifuse logs:decisions <ws> [--since 2h --reason … --trace]`, `notifuse queue:recompute <ws> --automation X --limit N --yes`.
6. **Console** : page « File d'envoi » (`/send-queue`, groupe Envoi) : arbre repliable automation, nœud, raison, profil ; filtres ; tiroir d'une entrée avec la trace gate par gate (valeur, limite, verdict) ; action « recalculer » bornée. Traductions fr et en (les autres langues retombent sur l'anglais).
7. **« completed » honnête** : un nœud email qui met en file pose l'action `queued` (et plus `completed`) ; `completed` ne s'écrit qu'à l'envoi confirmé (`HandleEmailSent`), `failed` à l'échec définitif. Les stats de nœud exposent `count_queued`, et `StatNode` l'affiche à part. Les anciennes lignes (`completed` avec `output.queued=true`) sont comptées en `queued` à la lecture.
8. **Orphelins `sending`** : cause supprimée à la source (la garde finale du worker supprimait la ligne de file d'une automation en pause sans prévenir l'exécuteur) et réconciliation idempotente dans le tick existant du planificateur (voir plus bas).

## Taxonomie des raisons (codes stables, partagés worker, API, CLI, UI)

`not_examined`, `window_closed`, `capacity` (détail : `warmup`, `provider_class`, `per_recipient`, `per_sender`), `class_rate`, `reputation_stopped`, `excluded_class`, `profile_paused`, `no_profile_in_pool`, `circuit_open`, `anchor_wait`, `quota_denied`, `render_failed`, `guard_retry`, `automation_paused`, `send_error`, `in_flight`, `orphan_parked`, `deferred_legacy` (report posé avant V62 : on le dit, on ne le devine pas). `followup_not_due` et `scheduler_lag` relèvent du lot 2 (graphe vivant).

## Volume du journal (borné, mesuré)

Politique `transitions` (défaut) : un report n'est écrit que s'il est le premier de l'entrée, si sa raison ou son profil change, un battement au plus par entrée et par 24 h, et un examen sur 200 en trace complète (`sampled`). Envois, échecs et rejets par garde : toujours écrits. Rétention opportuniste à l'écriture (aucune tâche planifiée) : trace élaguée après 14 jours, ligne supprimée après 90 jours, plafond dur de 500 000 lignes par workspace.

Entrées mesurées sur `robertbrunon` (lecture seule, 10/10 14h45 Paris) : 2 227 entrées `pending` (1 003 dues, 1 224 en attente d'un `next_retry_at`), 22 433 `paused` (jamais examinées : automations en pause), 185 envois sur 24 h. Ré-examen toutes les 5 minutes = 288 examens/jour/entrée due.

| Poste | Lignes/jour | Remarque |
|---|---|---|
| Premier report de chaque entrée neuve (~2 000/j) | ~2 000 | une fois par entrée |
| Changement de raison (week-end vers lundi, etc.) | quelques centaines | par évènement |
| Échantillon 1/200 d'un ré-examen toutes les 5 min | ~1,4 par entrée due, soit ~3 100 | borne haute si tout est dû |
| Battement 24 h | plafonné à 1 par entrée, soit ≤ 2 200 | remplacé par l'échantillon quand il tombe |
| Envois et échecs | ~600 | trace complète |
| **Total attendu** | **~6 000 à 8 000** | ~2 à 5 Mo/jour selon la trace |

Sans échantillonnage : 345 000 lignes par jour (1 200 entrées x 288). La mesure réelle après déploiement est dans la section « Preuves en production ».

## Orphelins `sending` : réparation

Mesure avant (lecture seule, `robertbrunon`, 10/10 14h57 Paris) : 89 contacts `sending` d'`ecomdevenir-j0j4j10-r2` (51 en `j0a`, 38 en `j0b`, tous entrés le 29/09) sans aucune entrée de file. Contrôle dans `message_history` AVANT d'agir : 0 de ces 89 contacts n'a un seul message envoyé (`sent_at`), tous les mails mis en file par le nœud sont absents de `message_history` sauf un (échec définitif, `failed_at` posé, jamais envoyé) ; aucun n'a répondu, aucun n'a d'entrée dans une autre file, l'automation est `live`.

Règle de la réconciliation (`service/veridian_orphan_reconcile.go`, jamais de mail en double) :

1. le mail mis en file est PARTI (`sent_at`) : on avance comme le callback perdu (`HandleEmailSent`) ;
2. le mail a ÉCHOUÉ définitivement : le contact sort (`HandleEmailFailed`) ;
3. un message existe sans état : on n'y touche pas ;
4. aucun mail connu ET le contact a déjà reçu un mail d'une autre source : il sort (`orphan_already_contacted`) ;
5. aucun mail connu et jamais contacté : le contact repasse `active` sur son nœud, qui rejoue ses contrôles (réponse, statut de liste) puis remet UN mail en file. Aucun mail n'était parti : rien à doubler.

Une grâce de 15 minutes couvre la fenêtre entre « ligne de file supprimée par un envoi réussi » et le callback. Idempotent : un contact réconcilié n'est plus `sending`. Un workspace par tick du planificateur d'automations existant, 100 contacts au plus : pas de tâche nouvelle (loi 2).

Ce qui a été modifié dans `robertbrunon` : voir « Preuves en production » (compte avant/après).

## Expand & Contract (§12)

V62 : `ADD COLUMN IF NOT EXISTS` nullables sans défaut ni index (métadonnée seule) et `CREATE TABLE/INDEX IF NOT EXISTS` sur une table neuve. Le tag précédent (`v61`) tourne sur ce schéma : il ignore les colonnes et la table. Aucun `DROP`, aucun `NOT NULL` sur table peuplée. `repository.EmailQueueRepository` retombe sur `SetNextRetry` si une colonne manque.

## Tests

Colocalisés (mapping 1-pour-1 contrôlé par `scripts/ci/check-test-mapping.sh`) : voir `git log` du commit. Points d'épreuve : parité des verdicts avec l'ancienne cascade, cas du samedi, échantillonnage et battement du journal, rétention, règles des orphelins (aucun cas ne remet en file un mail parti), contrat de l'API, arbre et tiroir de la console, complétude fr/en.

## Preuves en production

(complétées après déploiement)

## Pièges

- La raison affichée vient du gate au délai le plus long : ne jamais la lire comme « le premier gate qui a refusé ».
- `deferred_legacy` n'est pas une panne : ce sont les reports d'avant V62 ; ils se renomment au premier ré-examen.
- Un niveau `all` écrit un examen par ligne : à ne mettre que quelques heures pour déboguer un cas.
- `queue.recompute` exige un filtre (`automation_id` ou `entry_ids`) et une limite de 5 000 au plus : jamais « tout le workspace ».
