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

Doc OpenAPI : `openapi/paths/veridian-policy.yaml`. Tests CLI : `~/.claude/skills/notifuse-cli/tests/test_policy.py`.
Le CLI embarqué (`internal/http/agentcli`) est resynchronisé par `scripts/veridian/sync-agent-cli.sh`, pas à la main.

## 4. Valeurs observées

Catalogue au 29/10/2026, mesuré sur animalsd (tout l'historique, `messages.engagementByClass`) : 1 107 envois,
36 réponses humaines, 28 rebonds. L'échantillon par classe est petit : le taux de réponse "lissé" ramène chaque
classe vers la moyenne (poids 30 envois). Un workspace qui a envoyé vers une classe voit SES chiffres
(`observed.basis = workspace`), sinon ceux du catalogue (`catalog`).
