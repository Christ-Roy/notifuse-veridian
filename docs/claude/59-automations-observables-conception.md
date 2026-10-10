# 59. Automations observables : conception (logique d'envoi visible, pilotable, louable)

Date : 10/10/2026. Base : `origin/veridian` au commit `60241024` (fiche 58 incluse ; prod `v61.0-veridian.fd85ce35`, la correction de famine n'y est pas encore). Document de CONCEPTION : aucun code produit, aucun push, aucune écriture sur `robertbrunon` (mesures en lecture seule, `SET default_transaction_read_only = on`).

Demande de Robert (10/10) : toute la logique d'envoi, de relance et de sortie visible dans l'UI Notifuse, réutilisable par un client, pilotable aussi par CLI ou agent, avec un suivi fin ; puis : un **journal des décisions d'envoi** pour tout contrôler (section 8), et la **capacité d'un profil** qui devient la somme pondérée par classe (section 2.5, référencée, non redéfinie).

---

## 0. Ce qu'il faut retenir

1. **70 % de la matière existe déjà, la raison d'un report n'existe nulle part.** `email_queue.last_error` est NULL sur 24 585 lignes ; seul `next_retry_at` bouge. Les gates rendent `(délai, bool)` (`veridianSelectSendableIntegration`, `worker.go:379-392`), la raison est jetée dans un log DEBUG.
2. **Le premier gate qui refuse masque les autres.** Ordre : exclusion, réputation, débit de classe, plafond journalier, plafond par adresse, fenêtre. Mesure du 10/10 12h (samedi, fenêtre fermée jusqu'au lundi 06h UTC selon `profiles:overview`) : 1 200 entrées de `ecomdevenir` reportées de moins de 5 minutes. 5 minutes est exactement le plafond du débit de classe (`veridianMaxProviderClassRetryDelay`), pas celui de la fenêtre (1 h). La vraie raison (fenêtre fermée tout le week-end) est masquée par un gate placé avant elle, et les mêmes 1 200 entrées sont ré-examinées toutes les 5 minutes, ce qui occupe les lots du dépilage (fiche 58). Inférence cohérente avec la mesure ; seule la raison persistée la confirmera.
3. **Le nœud « Email » ment sur « complété ».** `automation_node_executions` pose `completed` à la MISE EN FILE (sortie `queued: true`). Mesure : `j4` = 199 « completed », et ces 199 sont toujours en file, jamais examinées, aucune relance envoyée. Les stats du viewer (`getNodeStats`) comptent donc des mails en file comme faits.
4. **Orphelins invisibles.** 89 contacts de `ecomdevenir` (29/09) sont `sending` sans aucune entrée en file ni message : parqués à jamais. Aucune vue ne les montre.
5. **Le nœud `webhook` n'est pas louable en l'état** : aucun garde SSRF (client HTTP nu, alors que `webhook_ssrf_guard.go` existe pour les abonnements), auth par simple `Bearer`, secret stocké en clair dans le JSON du nœud et aucune rédaction trouvée dans `automation_handler.go` / `automation_service.go`. À corriger AVANT toute location d'un « script client ». Aucune automation de `robertbrunon` n'utilise ce nœud aujourd'hui.
6. **Recommandation** : des **calques** (rails de politique, badges vivants, panneaux) sur les nœuds existants, deux extensions additives (`reply_branch` typé, `webhook` signé), un seul nouveau type de nœud (`exit` nommé), et des presets de palette. Pas de nœud « Envoi à froid » : il créerait un quatrième niveau dans la cascade des réglages. Zéro migration des 5 séquences r2.

---

## 1. Inventaire des règles d'envoi Veridian

Légende visibilité : **Oui** (écran ou API lisible), **Partielle**, **Non**. Fichiers sous `internal/` (service/queue pour le worker).

| # | Règle | Où elle s'exécute | Visible aujourd'hui | Ce qu'un opérateur devrait voir |
|---|---|---|---|---|
| 1 | Rendu au dépilage (modèle et contact courants) | `queue/veridian_render_at_send.go`, moteur `service/veridian_automation_email_render.go` (fiche 52) | Non (échec = `status_info: render_at_send: …`) | Version du modèle réellement envoyée ; échecs de rendu par nœud avec la raison |
| 2 | Tourniquet et relances d'abord (dépilage équitable) | `domain/veridian_fair_queue.go`, `FetchPending` (fiche 58) | Non | Âge de la plus vieille entrée « jamais examinée » par nœud ; alerte de famine |
| 3 | Profil assigné à la mise en file, pool de rotation | nœud email (`automation_node_executor.go`), `domain/veridian_sender_rotation.go` | Partielle (page Profils : pool) | Profil candidat, profil choisi, pourquoi |
| 4 | Failover de pool à l'envoi, ancre de séquence (la relance garde l'expéditeur du J0, 48 h) | `queue/veridian_pool_failover.go` | Non | « Relance attend son expéditeur d'origine » (raison `anchor_wait`) ; bascule effectuée |
| 5 | Pause d'un profil | `veridian_paused`, saute le candidat | Oui (page Profils) | Compte d'entrées bloquées par une pause |
| 6 | Circuit breaker transport | `queue/circuit_breaker.go` | Non | Intégration en cooldown, jusqu'à quand |
| 7 | Exclusion de classes | `queue/veridian_excluded_class_gate.go` (sortie PERMANENTE si tous les candidats excluent) | Partielle (profil) ; la sortie finit en `exit_reason` texte libre | Entrées sorties par exclusion, par classe |
| 8 | Fusible de réputation proportionné (facteur ÷2, ÷4, arrêt, par couple domaine x classe) | `queue/veridian_reputation_gate.go`, `service/veridian_reputation_status_service.go` | Partielle (`overview` : facteur et taux ; `messages.reputationStatus` sans écran propre) | Couple ralenti ou arrêté et le taux qui le déclenche, sur le nœud concerné |
| 9 | Débit par classe (+ jitter) | `queue/veridian_provider_throttle.go`, `veridian_jitter.go` | Partielle (débit configuré) | Débit effectif, prochaine place |
| 10 | Plafond journalier : chauffe, profil, adresse, classe, destinataire | `queue/veridian_daily_cap.go`, `veridian_per_sender_cap.go`, plan `veridian_effective_plan.go` ; la capacité devient la somme pondérée par classe (2.5) | Oui par profil (`emailProfiles.overview`), Non par entrée | Porte limitante pour CETTE entrée |
| 11 | Réservation atomique du quota | `queue/veridian_daily_quota.go` (refus = remboursement de tentative + report) | Non | Raison `quota_denied` |
| 12 | Fenêtre d'envoi | `queue/veridian_sending_window_gate.go` | Oui par profil | Réouverture exacte, entrées qui attendent |
| 13 | Pré-filtre (syntaxe, jetable, DNS) | `queue/veridian_prefilter.go` (sortie permanente) | Non (`exit_reason` texte) | Sorties « adresse invalide » par raison |
| 14 | Anti-hash identique, spintax | enqueue (broadcast) + filet LOG-ONLY `veridian_content_hash_gate.go` | Non | Collisions résiduelles par nœud (info) |
| 15 | Débit technique SMTP | `rate_limiter.Wait` dans `worker.go` | Non | Raison `technical_rate` |
| 16 | Garde finale d'automation (automation supprimée, non live, contact a répondu, hors liste, statut de liste) | `queue/veridian_automation_send_guard.go` | Non (entrée jetée sans message) | Entrées jetées par raison |
| 17 | Sortie sur réponse humaine, typage human/auto/challenge | IMAP : `service/veridian_reply_consumer.go`, `domain/veridian_reply_type.go`, table `veridian_contact_reply` ; porte d'exit à CHAQUE tick `service/veridian_cold_exit.go` | Partielle (`prospection.stats`, `messages.replyStats`) | Sorties par réponse, par type, par nœud où le contact attendait |
| 18 | Rejets rattachés (NDR) | `service/veridian_bounce_consumer.go` -> `contact_lists.status='bounced'`, `message_history.bounce_type` | Partielle (stats bounce) | Rejet rattaché au message ET au nœud |
| 19 | Séparation transactionnel / commercial | `queue/veridian_transactional_entry.go` (fiche 56) | Oui (page Profils) | Badge « transactionnel » sur le nœud, jamais compté commercial |
| 20 | Avance seulement sur envoi confirmé (statut `sending`) | `automation_executor.go` `HandleEmailSent` / `HandleEmailFailed` | Non (le « completed » ment, point 0.3) | Distinguer « en file » et « envoyé » |
| 21 | Sortie sur désinscription / rejet de liste | nœud email (statut de liste), garde finale | Non | Sorties par raison normalisée |

**Constat transversal** : aucune de ces règles n'écrit « j'ai décidé X parce que Y » quelque part de lisible. Tout le travail d'observabilité se ramène à : (a) faire produire un verdict structuré par les gates existants, (b) le relier au nœud, (c) l'agréger.

---

## 2. Modèle cible dans l'éditeur d'automations

### 2.1 Ce que l'upstream a déjà (à réutiliser)

- Éditeur et **viewer de graphe** (`AutomationFlowViewer.tsx`, `nodes/StatNode.tsx`) avec compteurs par nœud entered / completed / failed / skipped, alimentés par le schéma analytics `automation_node_executions`.
- `automation_node_executions` : une ligne par passage, avec `output` JSON et `error` ; `GET /api/automations.nodeExecutions` (parcours d'un contact dans UNE automation).
- `automations.exitContact` / `resetContact` (un seul contact), `automations.enroll`, pause/reprise par source de la file.
- Nœuds : trigger, delay, email, branch, filter, add/remove list, ab_test, `webhook`, list_status_branch, et `reply_branch` (ajout Veridian).
- `contact_timeline` : contient déjà les événements `insert_message_history`, `bounce_email`, `email.replied`, `list.*`, `automation.start|end` (mesure 10 jours : 2 043 envois, 54 rejets, 52 réponses).
- Abonnements webhook sortants **signés** (`webhook-signature`, `webhook_delivery_worker.go`) et garde SSRF (`webhook_ssrf_guard.go`, avec épinglage d'IP contre le DNS rebinding).

### 2.2 Les trois options

| Option | Pour | Contre |
|---|---|---|
| **A. Nœuds custom** (« Envoi à froid », « Attendre une réponse », « Sortie si… ») | Lisibles d'un coup d'œil | (1) La politique est TRANSVERSALE (broadcasts et automations, tous profils) : un nœud « Envoi à froid » qui porte fenêtre et plafonds crée un 4e niveau de cascade (broadcast, infra, workspace) et casse la thèse des lots 2 à 4 : une seule vérité par profil. (2) Coût mesuré d'un type de nœud : `reply_branch` = 31 fichiers, 1 729 lignes (8 catalogues .po), 4 fichiers upstream touchés (`automation.go`, `automation_executor.go`, `automation_node_executor.go`, console) : conflit de sync upstream à chaque nœud (fiche 41). (3) Migration des 5 séquences : les contacts parqués référencent `current_node_id` (`j0a`, `j4`…), refaire le graphe = risque sur 27 000 contacts. |
| **B. Calques, badges, panneaux sur l'existant** | Zéro migration ; la règle reste dans le backend ; une seule source de vérité par profil ; réutilisable : tout client voit ses propres plafonds | Moins « stylé » qu'un nœud dédié si on ne soigne pas le rendu |
| **C. Mix** | Garde les nœuds là où il y a du comportement NOUVEAU | Il faut tenir la ligne : un nœud seulement s'il ajoute un comportement |

### 2.3 Recommandation : C, très asymétrique (calques d'abord)

Règle de décision : **un nœud n'existe que s'il ajoute un comportement ; une politique se montre, ne se re-saisit pas.**

| Idée de Robert | Verdict | Comment |
|---|---|---|
| Nœud « Envoi à froid » (profils, fenêtre, plafonds) | **Pas un nœud.** Calque + preset | Le nœud `email` reste. Un panneau « Politique d'envoi » (lecture seule) affiche, pour ce nœud, le profil/pool, la fenêtre, la capacité du jour (2.5), les classes exclues, leur **source** (profil / workspace / défaut) et le lien vers la page Profils pour modifier. Un **preset de palette** « Email à froid » pose un nœud `email` pré-réglé (modèle, pool). Le seul réglage par nœud reste `integration_id` (existant) |
| « Attendre une réponse » | **Pas un nœud.** | `delay` + rail « sort si réponse humaine / rejet / désinscription » (la sortie est GLOBALE, évaluée à chaque tick par `veridianColdExitReason`). Le delay affiche « sort avant l'échéance si… » |
| « Sortie si réponse humaine / rejet / désinscription » | **Rail** (calque) + **un nœud `exit`** | Le rail montre les 3 sorties automatiques, et passe en « suspendue » si un `reply_branch` reprend la main (déjà le comportement, `HasReplyBranchNode`). Le nœud `exit` (nouveau type, terminal, config `{code, label}`) sert aux sorties métier nommées, comptées par raison |
| « Condition : type de réponse » | **Extension additive de `reply_branch`** | Config optionnelle `reply_types` et cibles `auto_node_id`, `challenge_node_id` (human / auto / challenge / aucune). Anciennes configs inchangées |
| « Webhook / script » | **Durcir `webhook`**, pas de nouveau nœud | Voir section 6 |

**Compatibilité** : nouveaux champs de config optionnels (ignorés par l'ancien code, Expand & Contract), aucun id de nœud changé, aucune inscription recréée. Les 5 séquences r2 gagnent badges et rails sans être modifiées.

### 2.4 Anatomie à l'écran

- **Rails (bandeau « Garde-fous de la séquence »)** au-dessus du graphe : puces Profil/pool, Fenêtre, Capacité du jour, Sorties automatiques (réponse humaine, rejet, désinscription), Classes exclues. Chaque puce s'ouvre sur la valeur effective et sa source. Lecture seule, alimentée par `EffectivePlan` (aucun recalcul côté console).
- **Nœud email** : trois chiffres vivants (« 1 670 présents · 574 jamais examinés · 12 envoyés 24 h »), un badge de **raison dominante** (« Fenêtre fermée, réouverture lun. 08:00 »), badge rouge si alerte de famine.
- **Nœud delay** : « 1 581 en attente · prochaine échéance 10/10 12:03 » (raison `followup_not_due`).
- **ab_test** : une ligne par variante (envoyés, rejets, réponses humaines/auto), calculée sur les nœuds enfants désignés par `variants[].next_node_id`.
- **Panneau d'un nœud** (clic) : onglets Présents (contacts et raison), Journal des décisions (8), Sorties par raison, Réglages.

### 2.5 Capacité d'un profil : référencée, non redéfinie

Un autre chantier livre en parallèle la nouvelle formule dans `EffectivePlan` et la page Profils : capacité maximale = somme des capacités configurées par fournisseur destinataire, pondérée par le stock réellement disponible par classe, bornée par la chauffe ; elle remplace le plafond plat de 300. Contrat retenu ici :

- Ce document **lit** les champs du plan tels que livrés (`daily_cap_today`, `remaining_today`, `limiting_gate`, `classes[].remaining`, `blocked_by`) ; aucune formule n'est dupliquée dans le journal, l'explorateur ou les badges. Un changement de formule ne casse donc rien ici.
- La raison de report est nommée **`capacity`** avec, en détail, le nom de la porte donné par le plan (`warmup`, `class_cap`, `profile_capacity`…). Ainsi la formule peut évoluer sans migrer la taxonomie.
- Le « stock disponible par classe » est le même jeu de chiffres que l'explorateur de file (section 4, groupement par classe). Les deux chantiers doivent appeler UNE fonction commune (`VeridianQueueStockByClass`), pas deux requêtes. **À synchroniser avec l'autre agent avant le lot 2.**

---

## 3. Observabilité vivante sur le graphe

### 3.1 API `GET /api/veridian/automations.liveState?workspace_id&automation_id` (permission `automations:read`)

Réponse par nœud :

- `present` : `{total, by_status{active,sending,failed}}` (contact_automations groupé par `current_node_id`).
- `waiting[]` : `{reason, count, next_attempt_min, next_attempt_max, example_entry_ids[≤3]}`.
- `sent_24h`, `sent_total`, `rejected` (hard/soft), `replies{human,auto,challenge}`, `exits{code: n}`.
- `variants[]` pour un `ab_test` (agrégat des enfants).
- `alerts[]` : `starvation`, `never_examined_age`, `orphan_parked`.
- `plan_ref` : profils concernés (pour que la console aille chercher l'`EffectivePlan`).

### 3.2 Taxonomie des raisons (codes stables, partagés worker, API, CLI, UI)

| Code | Sens | Source du verdict |
|---|---|---|
| `not_examined` | jamais lue par le worker (file qui n'arrive pas jusqu'à elle) | `first_examined_at IS NULL` |
| `followup_not_due` | relance : délai du nœud `delay` pas échu | `contact_automations.scheduled_at > now()` |
| `scheduler_lag` | active, échue, pas encore reprise | `scheduled_at <= now()` et `active` |
| `window_closed` | fenêtre d'envoi fermée (réouverture datée) | gate fenêtre |
| `capacity` | capacité du jour atteinte (détail = porte du plan : chauffe, profil, adresse, classe, destinataire) | gates plafond + `EffectivePlan` |
| `class_rate` | débit de la classe (avec facteur de ralentissement) | gate débit |
| `reputation_slowed` / `reputation_stopped` | couple domaine x classe ralenti ou arrêté (taux et seuil) | gate fusible |
| `excluded_class` | classe exclue (sortie permanente si tous les candidats) | gate exclusion |
| `profile_paused`, `no_profile_in_pool` | pause / aucun candidat | sélection de pool |
| `circuit_open` | transport en cooldown | circuit breaker |
| `anchor_wait` | relance qui attend son expéditeur d'origine (continuité 48 h) | `veridianFailoverCandidateIDs` |
| `quota_denied` | réservation atomique refusée | `veridianReserveDailyQuota` |
| `technical_rate` | débit technique SMTP | `rate_limiter` |
| `render_failed`, `guard_retry` | rendu / garde finale en échec transitoire | render at send, garde finale |
| `automation_paused` | l'automation est en pause (entrées `paused`) | `email_queue.status` |
| `orphan_parked` | `sending` sans entrée en file | jointure |

**Le masquage est supprimé** : la raison DOMINANTE d'une entrée n'est pas le premier gate qui refuse mais le gate au délai le plus LONG parmi ceux qui ont refusé pour le candidat le plus prometteur ; la liste complète est gardée dans la trace (8). Le test de non-régression du lot 1 rejoue le cas du samedi (fenêtre fermée + débit de classe) et exige `window_closed`.

### 3.3 Ce que montrerait la vue AUJOURD'HUI (mesure prod `robertbrunon`, 10/10 12h Paris, lecture seule)

`ecomdevenir-j0j4j10-r2` (live) :

| Nœud | Présents | Détail |
|---|---|---|
| `split` (A/B) | – | 2 614 + 33 échecs en A, 2 822 + 39 en B (cumul, mais « completed » = en file) |
| `j0a` / `j0b` | 803 / 867 `sending` | 1 200 reportés < 5 min (raison actuelle inconnue, probablement `window_closed` masquée), 574 `not_examined`, 5 échus ; **89 orphelins** (51 en `j0a`, 38 en `j0b`, 29/09) |
| `wait1` | 1 581 `active` | `followup_not_due`, échéances 10/10 10:03 à 13/10 17:00 |
| `j4` | 199 `sending` | 199 en file, 0 examinée : **famine des relances** ; « completed » = 199 alors que 0 envoyée |
| `wait2`, `j10` | 0 | – |

`ecomscale` : 200 `not_examined` (j0a 95, j0b 105). `ecomvetuste` : 172 `not_examined`. `localpresentiel` (en pause) : 5 273 entrées `paused` + 189 contacts `active` sur `j4` en retard (échéances du 05 au 09/10, automation en pause). `vitrinefrance` (en pause) : 17 160 `paused`. Aucune de ces lignes n'est visible aujourd'hui sans SQL ; le seul signal de la famine du 06/10 était l'absence de `ecom-relance-j4` dans `message_history`.

Envois : 538 le 09/10 (246 A + 292 B), tous des J0 ; 0 relance depuis le début.

### 3.4 Alerte de famine

Deux détecteurs, trois états chacun (OK / ALERTE / NON ÉVALUABLE, jamais deux), calculés **à la lecture** (API, CLI, console) :

1. **Silence de nœud** : `due_entries > 0` ET au moins un profil candidat « envoyable » (`EffectivePlan.sendable_now`) pendant N heures de **fenêtre ouverte** cumulées ET `sent = 0` sur ces N heures. Il faut le temps de fenêtre ouverte, pas le temps mural : un week-end ne déclenche rien. Fonction pure à ajouter (`OpenDuration(from, to)` sur `VeridianSendingWindow`). N par défaut 3 h.
2. **Équité** : plus vieille entrée `not_examined` plus âgée que 2 x le cycle attendu alors que d'autres entrées de la même file partent. C'est exactement la signature du 06/10 (J0 qui passent, relances jamais lues).

Où atterrit le verdict (règle « un détecteur qui ne parle à personne ne sert à rien ») : badge rouge sur le nœud et bandeau de l'automation ; `notifuse automations:observe --check` (codes de sortie 0 / 1 / 2) ; événement `automation.starved` posté vers les abonnements webhook existants (le client s'abonne). **Question à Robert (loi 2)** : l'appel périodique de `--check` par un job `all-cron`/`obs` existant demande son feu vert ; sans lui, aucune tâche nouvelle n'est créée et l'alerte vit à la lecture et dans la CLI.

---

## 4. Explorateur de file

`GET /api/veridian/queue.explain?workspace_id&group_by=automation,node,reason,profile,class&automation_id&node_id&reason&profile_id&class&status` (`automations:read`).

Groupe : `{keys, count, oldest_created_at, never_examined, next_attempt_min/max, sample_entry_ids[3]}`. Source : `email_queue` + colonnes de la section 7 + jointure `contact_automations` (nœud). Page console « File d'envoi » (groupe Envoi, à côté du Journal d'envoi), tableau groupé repliable, filtres, lien vers le panneau du nœud et le parcours du contact.

**Actions sûres** (`automations:write`, journalisées dans le journal des décisions, plafonnées) :

- `queue.recompute` : remet `next_retry_at = NULL` et efface la raison pour les entrées d'un filtre (obligatoire, borné à 5 000, jamais « tout le workspace sans filtre »). Réutilise le principe de `WakePendingByIntegration`. Aucune entrée supprimée, aucune tentative consommée.
- `queue.exitContact` : appelle `automations.exitContact` existant (motif `manual`, code normalisé).
- Pause/reprise d'une source : existe (`PauseBySource`/`ResumeBySource`), simplement exposée.

Pas de suppression brute d'entrées de file, pas d'édition de payload.

---

## 5. Parcours d'un contact

`GET /api/veridian/contacts.journey?workspace_id&email[&automation_id]` (`automations:read` + `contacts:read`). Liste d'événements normalisés `{at, kind, node_id, source, detail}`, fusion de :

| Source existante | Événements |
|---|---|
| `contact_automations` | inscription (`entered_at`), statut, sortie (`exit_code`/`exit_reason`) |
| `automation_node_executions` | chaque nœud traversé, sortie du nœud (branche A/B, délai posé) |
| `email_queue` | entrée courante : statut, raison, prochaine tentative, nombre d'examens |
| **`veridian_send_decisions` (nouveau, 8)** | chaque tentative marquante : profil candidat, gates, décision |
| `message_history` | envoyé (`sent_at`), profil, expéditeur, classe, délivré, ouvert, cliqué, rejet (`bounce_type`), échec |
| `veridian_contact_reply` | réponse et son type (human/auto/challenge) |
| `contact_timeline` | désinscription, `list.*`, `email.replied` |

Rendu : frise verticale par inscription (un contact peut en avoir plusieurs), nœud par nœud, avec « attendu depuis… » et la raison courante. Accès depuis la fiche contact et depuis l'explorateur. Aujourd'hui `nodeExecutions` donne 1 des 7 sources.

---

## 6. Scripts custom sans casser le multi-tenant

**Ce que l'upstream a** : un nœud `webhook` (POST JSON avec le contact, `Bearer`, 30 s, réponse 10 Ko stockée dans le contexte) ; des abonnements webhook SORTANTS déjà signés et gardés SSRF. Le nœud n'a rien de cela.

**Recommandation : le « script » d'un client est un service du client, appelé par un webhook signé.** Pas d'exécution de code fourni par le client dans Notifuse (surface d'attaque, quota, isolement : refus net).

Durcissement du nœud `webhook` (lot 5, additif, config optionnelle) :

1. **Garde SSRF obligatoire** (réutiliser `ssrfSafeDialContext` et `validateWebhookURL`) : aujourd'hui un locataire peut viser `127.0.0.1`, le CGNAT Tailscale de la flotte ou les métadonnées cloud. **Correctif de sécurité à livrer seul, avant toute location** (lot 0 ci-dessous).
2. **Signature** `webhook-signature` HMAC comme les abonnements (secret par nœud, jamais renvoyé par `automations.get` : rédaction à la manière de la loi 9) en plus de l'ancien `Bearer` (compat).
3. Contrat de réponse déclaratif : `{"outcome":"continue|exit|branch","branch":"id","set":{…}}` ; `timeout_s` borné (≤ 10), `on_error: fail|continue|exit`, 2 tentatives avec backoff. Réponses > 4xx tracées dans `node_executions.error`.
4. Quotas par workspace (appels/min) et journal des appels dans le journal des décisions.

**Calculs sans serveur** (« fonctions déclaratives ») : plus tard, un nœud `set_attribute` à expressions bornées (pas de boucle, pas d'I/O, pas d'accès réseau, temps et taille bornés) sur le contact ou le contexte. Non recommandé avant d'avoir un besoin client réel.

---

## 7. Persistance nécessaire

Migration **V62** (additive, `IF NOT EXISTS`, transactionnelle ; le runner migre dans une transaction : pas d'index `CONCURRENTLY`, donc aucun index dans V62 sur table peuplée, cf. `migrations-pending.txt`). Le tag précédent tourne sur ce schéma (Expand & Contract).

**`email_queue`** (24 585 lignes, 91 Mo mesurés, `last_error` toujours NULL) : colonnes nullables sans défaut (opération de métadonnées sous PG 17) :
`node_id`, `contact_automation_id` (posés à la mise en file : lien direct file -> nœud, aujourd'hui déduit par `contact_automations.current_node_id`), `defer_reason varchar(24)`, `deferred_at`, `defer_until`, `defer_count int`, `first_examined_at`, `last_examined_at`. Écrits dans le MÊME `UPDATE` que le report (nouvelle méthode `SetDeferral`, `SetNextRetry` conservée) : coût ajouté nul. Avant la migration, l'explorateur retombe sur la jointure.

**`contact_automations`** : `exit_code varchar(32)` nullable. Aujourd'hui `exit_reason` est du texte libre de 50 caractères (`pre-filtered recipient: undeliverable_domain`, `excluded_provider_class:ionos`, erreurs SMTP tronquées…, 17 variantes rien que sur `ecomdevenir`). Codes : `replied_human`, `bounced`, `unsubscribed`, `excluded_class`, `invalid_recipient`, `send_failed`, `render_failed`, `list_status`, `manual`, `automation_deleted`, `completed`, `custom:<code>`. Les anciennes lignes sont classées à la lecture par un `CASE` sur `exit_reason` (pas de rétro-remplissage).

**`automation_node_executions`** : la sortie du nœud email gagne `phase: queued|sent|failed`, et l'API de stats sépare `queued` de `sent` (corrige 0.3). Aucune colonne.

**`veridian_send_decisions`** (nouvelle table du workspace) : voir 8. Remplace tout « événement de parcours » distinct : un seul journal.

Rétention : détail et volumes en 8. Purge **opportuniste à l'écriture** (suppression de 500 lignes périmées tous les ~1 000 inserts) : aucune tâche planifiée nouvelle (loi 2).

---

## 8. Journal des décisions d'envoi (ajout Robert)

Besoin : « des logs pour contrôler toute la logique et s'assurer que tout est fine ». Un enregistrement structuré par tentative marquante : entrée, contact, nœud, profil candidat, chaque gate avec **valeur, limite, verdict**, décision finale et raison.

### 8.1 Modèle d'enregistrement

Table `veridian_send_decisions` : `id`, `at`, `entry_id`, `message_id`, `contact_email`, `automation_id`, `node_id`, `contact_automation_id`, `outcome` (`sent|deferred|failed|discarded|exited|recomputed`), `reason` (taxonomie 3.2), `until` (report jusqu'à), `profile_id` (gagnant ou meilleur candidat), `sampled bool`, `trace jsonb` :

```json
{"class":"ovh","anchor":{"profile":"5eb5…","available":true},
 "candidates":[{"profile":"5eb5…","from":"…","gates":[
   {"gate":"excluded","limit":["ionos"],"value":"ovh","verdict":"pass"},
   {"gate":"reputation","limit":0.03,"value":0.099,"factor":2,"verdict":"slowed"},
   {"gate":"class_rate","limit":"0.12/min","value":"0 jeton","delay_s":300,"verdict":"block"},
   {"gate":"capacity","limit":300,"value":0,"gate_name":"warmup","verdict":"pass"},
   {"gate":"window","limit":"lun-ven 8-19h Europe/Paris","value":"sam 12:03","delay_s":165600,"verdict":"block"}]}],
 "decision":{"outcome":"deferred","reason":"window_closed","until":"2026-10-12T06:00:00Z"}}
```

Les valeurs et limites sont celles que le gate a **réellement utilisées** : chaque gate gagne une variante `…Verdict` qui rend une structure (`Gate, Value, Limit, Delay, Blocked`) ; la fonction existante `(délai, bool)` devient un simple wrapper. Un **test de parité** rejoue le worker et exige que le verdict de la variante égale le résultat de l'ancienne fonction (épreuve de mutation : une limite faussée doit faire échouer). Pas de seconde implémentation (c'était le défaut de la fiche 53).

### 8.2 Quand écrire (échantillonnage)

Ré-examen à 5 minutes de 1 200 entrées = 14 400 examens/h, 345 000/jour sur ce seul workspace (mesure). Tout journaliser coûterait ~200 Mo/jour : refusé. Politique par défaut `decision_log_level = transitions` :

- **Toujours** : envoi accepté, échec, rejet, sortie, rejet par garde, `recomputed`, force/recalcul manuel.
- **Reports** : première décision d'une entrée, et chaque **changement** de raison ou de profil gagnant ; sinon un battement (« heartbeat ») au plus un par entrée et par 24 h, et un par **groupe** (nœud, raison, profil) et par heure pour la vue agrégée.
- **Échantillon** `sampled=true` : 1 examen sur 200 en trace complète pour les reports récurrents (preuve que le gate continue de tourner).
- Niveaux réglables par workspace : `off` (compteurs seuls), `transitions` (défaut), `all` (borné à 24 h, pour déboguer un cas).

### 8.3 Volume et rétention (estimation à confirmer par mesure au lot 1)

Hypothèses : ligne moyenne 1,2 Ko avec trace (envois, échecs : trace complète) ; 300 octets sans trace (heartbeats, reports répétés, trace réduite aux gates bloquants).

| Poste | Lignes | Taille |
|---|---|---|
| Rattrapage du stock actuel (24 585 entrées x ~3 transitions) | ~75 000, une fois | ~60 Mo |
| Régime établi (≈ 600 envois/j + 2 000 entrées neuves/j x 3 transitions + heartbeats de groupe) | ~7 000/j | ~5 à 8 Mo/j |
| Rétention détail (trace) : 14 jours | | ~100 Mo |
| Rétention résumé (trace élaguée, 300 octets) : jusqu'à 90 jours | ~630 000 | ~190 Mo |

Comparaison : `automation_node_executions` = 95 050 lignes / 49 Mo ; `contact_timeline` = 117 697 lignes / 24 Mo. Le journal reste du même ordre que l'existant. Garde-fou : plafond dur de 500 000 lignes par workspace (suppression des plus anciennes), compteur exposé, niveau repassé à `off` avec alerte si dépassé deux jours de suite. Index : (`contact_email`, `at`), (`automation_id`, `node_id`, `at`), (`entry_id`) ; créés avec la table vide (donc sans `CONCURRENTLY`).

### 8.4 Consultation

- **UI** : explorateur de file (une entrée -> sa dernière décision et la trace dépliable, gate par gate avec valeur/limite/verdict) ; parcours d'un contact (chaque tentative, rejouable) ; panneau d'un nœud, onglet « Journal » (dernières décisions du nœud, filtre par raison/profil).
- **API** : `GET /api/veridian/decisions.list?automation_id&node_id&email&entry_id&reason&outcome&since&limit` (curseur).
- **CLI** : `notifuse logs:decisions [--automation X --node Y --email Z --reason window_closed --since 2h --json]` ; `notifuse queue:explain` affiche aussi la dernière décision d'un groupe ou d'une entrée (`--entry ID`).
- Secrets : la trace ne contient ni mot de passe, ni corps de mail, ni adresse d'un autre locataire ; les adresses expéditrices sont déjà des identifiants de profil publics. Un test vérifie l'absence de champs de secret.

---

## 9. CLI et API (couverture exigée)

Le CLI embarqué (`internal/http/agentcli/notifuse_common.py`) exige une commande par route (`coverage_test.sh`, `WORKSPACE_COMMANDS`, `MANIFEST.sha256`). Toute route ci-dessous arrive avec sa commande dans le MÊME lot.

| Vue | Route | Commande |
|---|---|---|
| État vivant d'une automation | `GET automations.liveState` | `notifuse automations:observe <ws> <automation> [--node N] [--check] [--json]` |
| Explorateur de file | `GET queue.explain` | `notifuse queue:explain <ws> [--group-by node,reason,profile,class] [--automation X] [--entry ID]` |
| Recalcul sûr | `POST queue.recompute` | `notifuse queue:recompute <ws> --automation X --node Y --reason R --limit N [--yes]` |
| Parcours | `GET contacts.journey` | `notifuse contacts:journey <ws> <email> [--automation X]` |
| Journal | `GET decisions.list` | `notifuse logs:decisions <ws> [...]` |
| Rails de politique d'une automation | `GET automations.policy` | `notifuse automations:policy <ws> <automation>` |

Mêmes données, mêmes codes de sortie 0 / 1 / 2 pour `--check`. Les actions d'écriture exigent `--yes` ; les agents lecteurs n'ont que `automations:read`.

---

## 10. Plan de livraison

Chaque lot : livrable seul en prod, avec ses tests (CI par tags, tests colocalisés 1-pour-1, épreuve de mutation), preuve en production mesurée avant clôture (« pas fini tant que pas en prod »).

| Lot | Contenu | Valeur pour Robert | Fichiers touchés (principaux) | Risques |
|---|---|---|---|---|
| **0. Sécurité webhook** (indépendant, ½ journée) | Garde SSRF sur le nœud `webhook`, rédaction du secret dans `automations.get` | Louer sans trou | `service/automation_node_executor.go` (via un wrapper `veridian_*.go`), `http/veridian_workspace_redaction.go` ou équivalent automation | Faible. Aucune automation de `robertbrunon` n'utilise le nœud |
| **1. « Pourquoi ça n'envoie pas »** | V62 (colonnes `email_queue`), verdicts de gates + `SetDeferral`, raison dominante sans masquage, `queue.explain` + `queue:explain`, journal minimal (`veridian_send_decisions`, niveau `transitions`) + `decisions.list` + `logs:decisions`, page « File d'envoi » en lecture | **Il voit en une page pourquoi chaque groupe attend** (fenêtre, capacité, débit, `not_examined`, orphelins) | `queue/veridian_*_gate.go` (variantes `Verdict`), `queue/veridian_pool_failover.go`, `worker.go` (un seul point d'appel), `domain/email_queue.go`, `repository/email_queue_postgres.go`, nouveaux `veridian_queue_explain*`, `veridian_send_decision*`, console `pages/` + `components/queue/` | **Le plus risqué** : touche la cascade. Mitigation : wrappers, test de parité, zéro changement de décision (épreuve : mêmes décisions sur la file rejouée). Charge DB : une écriture dans l'UPDATE existant |
| **2. Graphe vivant** | `automations.liveState`, `automations:observe`, badges (présents, en attente + raison, envoyés/rejets/réponses), séparation `queued`/`sent`, orphelins, alerte de famine (lecture) + `--check` | Le graphe montre la vie ; la famine du 06/10 est détectée | `service/veridian_automation_livestate*`, `repository/veridian_livestate_postgres.go`, console `StatNode.tsx` + `AutomationFlowViewer.tsx` | Moyen : requêtes d'agrégat sur `contact_automations` (≈ 27 000 lignes ici) ; budget < 100 ms, cache 10 s |
| **3. Parcours d'un contact** | `contacts.journey`, `contacts:journey`, onglet dans la fiche contact | Suivre un prospect de bout en bout | `service/veridian_contact_journey*`, `http/`, console `components/contacts/` | Faible (lecture) |
| **4. Rails, politique et sorties normalisées** | `exit_code` + CASE de lecture, bandeau « Garde-fous », panneau d'un nœud, `automations.policy`, sorties par raison | Les règles deviennent lisibles ; sorties comptées proprement | `domain/automation.go` (champ seul), `automation_executor.go` (`markAsExited` pose le code), console rails | Faible à moyen : `exit_reason` consommé par d'autres vues (stats) |
| **5. Extensions de nœuds** | `reply_branch` typé, nœud `exit` nommé, `webhook` signé (contrat de réponse), presets de palette | Séquences plus expressives, scripts client | `automation_node_executor.go`, console `nodes/` + `config/`, 8 catalogues .po | Moyen : 4 fichiers upstream touchés (conflits de sync), i18n ; livrer par PR séparées |
| **6. Alerte poussée et rétention** (conditionnel) | Événement `automation.starved`, banc de purge opportuniste, `--check` par un job existant | Être prévenu sans regarder | `service/veridian_webhook_emitter.go`, job existant | **Loi 2** : l'appel périodique exige l'accord explicite de Robert |

### Parallélisme

- **Indépendants dès maintenant** : lot 0 (fichiers exécuteur seul) ; lot 5 (nœuds, console et exécuteur ; seulement après le lot 4 pour `exit_code`).
- **Lot 1 d'abord, seul sur `queue/` et `email_queue`** (une seule main : c'est le cœur, voir règle 4 du CLAUDE.md).
- **Lots 2 et 3 en parallèle APRÈS le lot 1** : fichiers disjoints (2 : livestate + StatNode/Viewer ; 3 : journey + fiche contact). Leur seul contact est `veridian_send_decisions` (lecture) : figer le schéma au lot 1.
- Lot 4 : après le lot 2 (même composant de graphe), en parallèle du lot 3.
- **Conflit à éviter** : lots 2, 4, 5 touchent `AutomationFlowViewer.tsx`/`StatNode.tsx` -> un seul agent console à la fois sur ces deux fichiers ; les .po (8 langues) se régénèrent en fin de lot pour éviter les conflits.
- **Dépendance externe** : la capacité (2.5). Le lot 1 lit `EffectivePlan` tel que livré ; synchroniser le nom des champs avec l'autre agent avant de coder la raison `capacity`.

### Décisions demandées à Robert

1. Accord pour que `automations:observe --check` soit appelé par un job `all-cron`/`obs` EXISTANT (loi 2). Sinon alerte à la lecture seulement.
2. Niveau par défaut du journal : `transitions` (≈ 5 à 8 Mo/jour sur `robertbrunon`). Reco : oui.

---

## 11. Pièges à ne pas oublier

- Un contrôle d'absence (« rien ne part ») ne vaut qu'après le temps de la fenêtre ouverte : jamais d'alerte de famine un week-end ou hors fenêtre.
- Le « completed » d'un nœud email n'est pas un envoi (0.3) : ne jamais afficher « envoyés » depuis les node_executions.
- Une entrée peut changer de profil entre la mise en file et l'envoi (failover) : le journal enregistre le profil GAGNANT, pas celui de la file.
- `veridian_contact_reply` a pour clé le contact (un seul enregistrement par adresse, tout workspace confondu) : la vue « réponses par nœud » est par contact, pas par inscription.
- Les 89 orphelins `sending` sans file du 29/09 sont à analyser par l'agent du lot 2 (cause probable : `discardAutomationEntry` ou callback perdu), la réparation passe par `resetContact`/`exitContact` existants, pas par SQL.
- Aucune formule de capacité dans ce chantier : lire le plan.
