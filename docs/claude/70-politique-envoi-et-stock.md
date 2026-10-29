# 70 Politique d'envoi par classe en données, priorité des campagnes, stock (lot L3, 29/10/2026)

Demande de Robert : des règles d'envoi par défaut intelligentes, et un envoi optimisé selon le stock réel de
chaque fournisseur destinataire. Module : `internal/veridian/policy`. Migration : V66 (registre séparé,
`internal/migrations/veridian/v66_politique_stock.go`, tables du workspace, aucune table upstream touchée).

## 1. Règles par classe en données

- **Source unique : `internal/domain/veridian_class_presets.json`** (embarqué). Par classe : plafond/jour,
  cadence/min, part de la chauffe, seuil de ralentissement, ce qui a été observé (envois, rebonds, réponses
  humaines, daté et sourcé) et la raison en français. Plafonds et débits = ceux de la fiche 69, repris à
  l'identique (test témoin `TestClassPresets_ReproduceLesDefautsDeLaFiche69`). Les anciennes tables Go
  (`veridianDefaultClassDailyCap`, `...RatePerMin`) en sont dérivées.
- **Un workspace neuf les reçoit sans rien régler.** Une surcharge par workspace vit dans
  `veridian_policy_override` (`policy.set`, `policy.reset`), prise en compte par le worker en quelques secondes.
- **Priorité des niveaux, du plus spécifique au plus général** : payload du broadcast, profil d'envoi, réglage
  workspace historique (`veridian_provider_class_*`), surcharge de politique, preset. Une valeur posée à la
  main n'est jamais écrasée (`policy.get` indique `shadowed_by`).
- **Quatre règles** : `daily_cap`, `rate_per_min` (portes existantes), `warmup_share` (tant qu'un domaine d'envoi
  chauffe, la classe reçoit au plus cette part du plafond de chauffe du jour ; ne s'applique pas si le plafond de la
  classe est posé à la main), `slowdown_threshold` (taux de rebonds durs ou de refus de politique sur 7 jours qui
  divise le débit de la classe par 2, par 4 au double ; le seuil posé sur un PROFIL prime).
- **Crochets, pas de logique dans la file** : `internal/service/queue/veridian_policy_hook.go` est la seule
  jonction (une ligne dans `veridian_gate_limits.go`, `veridian_provider_throttle.go`, `veridian_reputation_gate.go`,
  `veridian_effective_plan.go`). `worker.go` n'est pas touché. Interface : `domain.VeridianPolicyProvider`,
  implémentée par `policy.Resolver` (cache 30 s, base illisible = dernière valeur connue puis presets).
- Coupés par `veridianClassDefaultsEnabled` comme les autres défauts (TestMain historique uniquement).

## 2. Priorité d'envoi par campagne ou automation

- Table `veridian_send_priority` (type, id, priorité 2 à 9). **Défaut 5 = pas de ligne = comportement d'avant.**
  1 reste réservé au transactionnel de séquence.
- Lue à l'enqueue par `domain.VeridianSendPriority` (deux lignes : `queue_message_sender.go` et
  `automation_node_executor.go`), écrite dans `email_queue.priority`. La file dépile déjà `priority ASC`.
- `policy.priority.set` réécrit aussi les mails déjà en file (`pending` et `paused`, `priority >= 2`), jamais
  ceux déjà partis ni le priorité 1.
- **Limite à connaître** : la priorité ne change l'ordre que si la file est engorgée entre campagnes. Si le plafond
  par expéditeur ou par fournisseur est la limite, elle ne fait pas partir plus de mails.

## 3. API et CLI

| Route | CLI | Droit |
|---|---|---|
| `policy.get` | `policy:show [--class C] [--explain]` | workspace:read |
| `policy.set` / `policy.reset` | `policy:set` / `policy:reset` | workspace:write |
| `policy.priority.list` / `.set` / `.clear` | `policy:priority [--broadcast ID \| --automation ID] [--set N \| --clear]` | broadcasts + automations |
| `stock.overview` | `stock:overview [--days N] [--enrolled-list L] [--reserve-list L]` | contacts:read + message_history:read |
| `stock.config` / `stock.config.set` | `stock:config [options]` | workspace:read / write |
| `policy.suggest-enrollment` | `policy:suggest-enrollment [--days N] [--emails] [--limit N]` | contacts:read + message_history:read |

Doc OpenAPI : `openapi/paths/veridian-policy.yaml`. Tests CLI : `~/.claude/skills/notifuse-cli/tests/test_policy.py`.
Le CLI embarqué (`internal/http/agentcli`) est resynchronisé par `scripts/veridian/sync-agent-cli.sh`, pas à la main.

## 4. Stock par classe (`stock.overview`)

- **Reproduit `~/asd-prospection/stock-par-classe.py`** : mêmes colonnes (inscrits, réserve, total, plafond/jour, jours),
  même ordre (jours croissants, classes sans plafond en fin), mêmes filtres (adresses internes, `custom_string_1` vide ou
  `cart_`, étiquette absente = classe `?`, appartenance supprimée, statut de liste quelconque), même arrondi au dixième.
  `plafond/jour` = somme des `capacity_configured` des profils commerciaux actifs (ni pause, ni hors rotation, classes non
  exclues) lue dans le service de `emailProfiles.overview` : aucun second calcul de plafond. Elle baisse avec l'heure et
  s'annule hors fenêtre d'envoi : comparer le script et l'API à la même minute.
- **En plus** : `unsent` (jamais contactés), `capacity_nominal` (une journée d'envoi entière, stable), `days_unsent`,
  `status` (ok, bas, critique, sans_plafond, inactive), `restock_need` (contacts à aller chercher pour tenir `target_days`).
- **Définition du stock** : `stock.config` (table `veridian_policy_settings`, clé `stock`) : liste des inscrits, liste de réserve,
  automation d'inscription, colonne de classe (`custom_string_1` à `5`, liste blanche), regex d'adresses écartées, colonnes
  obligatoires, préfixes interdits, couverture visée. Sans définition : les listes des automations actives. Chaque champ se
  surcharge dans la requête (lecture seule). Valeurs d'animalsd : inscrits `prospectionasd`, réserve `stockodhmasse`,
  `custom_string_1` obligatoire sans préfixe `cart_`, regex `brunon5robert|veridian\.site|animalsd\.fr|zz-test|animal-services-distribution`.

## 5. Suggestion d'inscription (`policy.suggest-enrollment`)

- Par classe : capacité/jour nominale x N jours d'envoi, moins les **relances** de contacts déjà en séquence qui échoient dans
  l'horizon (elles passent avant), moins les inscrits jamais contactés = le manque. `suggest_enroll` = min(manque, réserve
  éligible) ; `shortfall` = ce que la réserve ne couvre pas, classé dans `restock_priority` (taux de réponse lissé d'abord).
- Réserve éligible = jamais contactée, ni désabonnée, rejetée ou plainte, pas dans la liste des inscrits, pas dans l'automation
  d'inscription. Adresses (`include_emails`) ordonnées par priorité (`custom_string_2`, P1 d'abord) puis score (`custom_number_1`).
- **N'inscrit personne** : `applied` vaut toujours false, le module n'a aucun code d'écriture dans les automations. Le CLI
  affiche les commandes `automations:enroll` par lots, à lancer à la main (Robert alimente à la main).

## 6. Valeurs observées

Catalogue au 29/10/2026, mesuré sur animalsd (tout l'historique, `messages.engagementByClass`) : 1 107 envois,
36 réponses humaines, 28 rebonds. L'échantillon par classe est petit : le taux de réponse "lissé" ramène chaque
classe vers la moyenne (poids 30 envois). Un workspace qui a envoyé vers une classe voit SES chiffres
(`observed.basis = workspace`), sinon ceux du catalogue (`catalog`).
