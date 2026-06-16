# KPI manquant : taux de RÉPONSE (reply) absent du dashboard mails

> **Sévérité** : 🔴 P0
> **Owner** : agent notifuse
> **Créé** : 2026-06-16
> **Axe audit** : KPI / Dashboard (audit cohérence post-sprint cold)

## Contexte

Le sprint cold a livré la détection de **réponse prospect** via IMAP (stop-on-reply,
Lot 3, 2026-06-15) : quand un prospect répond, le signal est posé durablement et le
contact sort de la séquence. La donnée EST stockée et auditée, **mais elle n'est
exposée NULLE PART en KPI**. Or, en cold outreach, le taux de réponse est LE KPI #1
(c'est la conversion réelle d'une campagne — bien plus que open/clic qui sont bruités
par les bots de sécurité et le MPP Apple). Robert l'a explicitement signalé : *« le
dashboard principal avec les KPI des mails — il va manquer la metric reply qu'on
vient d'ajouter grâce à IMAP »*.

### Preuve du trou (code lu des deux bouts)

**Backend — où vit la donnée reply** :
- Table dédiée `veridian_contact_reply` (migration `internal/migrations/v51.go`,
  + `internal/database/init.go:486-493` pour les nouveaux workspaces). Colonnes :
  `contact_email` (PK, lowercase), `replied_at TIMESTAMPTZ`, `match_type TEXT`
  (`message_id` | `sender_fallback`), `matched_message_id TEXT` (nullable),
  `created_at TIMESTAMPTZ`. **1 ligne par contact ayant répondu au moins une fois.**
- Repo `internal/repository/veridian_contact_reply_postgres.go` : expose
  UNIQUEMENT `MarkReplied` (insert idempotent) et `HasReplied` (EXISTS O(1) par
  contact). **AUCUNE méthode de comptage / agrégation** → impossible de produire
  un total reply sans ajouter une méthode repo.
- Interface `internal/domain/veridian_contact_reply.go` (`VeridianContactReplyRepository`)
  → idem, pas de Count.
- Un event timeline `email.replied` EST posé (`internal/service/veridian_reply_service.go:284-308`,
  table `contact_timeline`, kind `email.replied`) — mais le timeline n'est pas
  agrégeable via l'analytics (pas de schéma analytics `contact_timeline`).

**Backend — pourquoi le dashboard ne peut PAS l'afficher tel quel** :
- Le dashboard interroge le schéma analytics `message_history`
  (`internal/domain/analytics.go:13-204`). Ses mesures : `count_sent`,
  `count_delivered`, `count_bounced`, `count_complained`, `count_opened`,
  `count_clicked`, `count_unsubscribed`, `count_failed`. **Il n'existe AUCUNE
  mesure `count_replied`** (grep `count_replied` sur tout le repo = vide).
- La table `message_history` n'a PAS de colonne `replied_at` (vérifié
  `internal/database/init.go:199` = `opened_at` etc., pas de `replied_at` ;
  le `replied_at` à init.go:490 appartient à la table SÉPARÉE
  `veridian_contact_reply`). Le reply est donc structurellement hors du schéma
  `message_history` → on ne peut pas se contenter d'ajouter une mesure.

**Front — où le KPI devrait apparaître** :
- `console/src/components/analytics/EmailMetricsChart.tsx` : la rangée de
  `Statistic` Ant Design affiche Sent / Delivered / Opens / Clicks / Bounced /
  Complaints / Unsub / Failed (8 cartes, `count_*`). **Aucune carte Reply.**
- `console/src/components/analytics/AnalyticsDashboard.tsx` monte ce composant ;
  rendu via `console/src/pages/AnalyticsPage.tsx:111`.

## Demande précise

Exposer le **taux de réponse** (et le compte brut) dans le dashboard mails, segmenté
par plage de dates comme les autres KPI. Reply rate = `replied_contacts / count_sent`
sur la fenêtre.

### Chemin recommandé (le plus propre, réutilise un pattern existant)

NE PAS tenter de greffer reply dans le schéma analytics `message_history` (la donnée
est dans une autre table, pas de colonne, pas de jointure dans le moteur analytics
générique). Créer un **endpoint stats dédié Veridian**, calqué pixel sur le breakdown
contacts R1 (même structure handler/service/repo, même auth JWT console).

**1. Repo — ajouter une méthode de comptage** (`veridian_contact_reply_postgres.go`
   + interface `internal/domain/veridian_contact_reply.go`) :
   ```
   CountRepliedSince(ctx, workspaceID string, since, until time.Time) (int, error)
   // SELECT COUNT(*) FROM veridian_contact_reply WHERE replied_at >= $1 AND replied_at < $2
   ```
   Régénérer le mock `internal/domain/mocks/mock_veridian_contact_reply_repository.go`
   (mockgen v1.6.0 `github.com/golang/mock`, cf. CLAUDE.md).
   ⚠️ La PK est `contact_email` seul → pas d'index sur `replied_at`. Sur le volume
   cold quotidien d'un workspace c'est un seq-scan négligeable ; si ça devient chaud,
   ajouter un index `(replied_at)` dans une migration additive (décision lead, pas
   avant mesure — même posture que le daily cap V49).

**2. Service + handler stats** — créer les fichiers veridian flat (préfixe respecté) :
   - `internal/http/veridian_reply_stats_handler.go` — route
     `POST` **ET** `GET /api/veridian/messages.replyStats` (⚠️ router les DEUX
     méthodes explicitement, piège catchall `root_handler.go` documenté CLAUDE.md).
     Auth = `middleware.RequireAuth()` + `AuthenticateUserForWorkspace` +
     permission `contacts:read` (copier `veridian_contact_breakdown_handler.go`).
     Params : `workspace_id` (requis), `start` + `end` (ISO, fenêtre dashboard).
     Réponse : `{"replied": N, "sent": M, "reply_rate": 0.XX}` (sent repris du
     COUNT message_history pour calculer le ratio côté backend, OU laisser le front
     diviser par le `count_sent` qu'il a déjà — au choix de l'implémenteur ; le
     plus simple = renvoyer `replied` brut et laisser le front faire le ratio avec
     le `count_sent` déjà chargé par `EmailMetricsChart`).
   - `internal/service/veridian_reply_stats_service.go` — gardien d'appartenance +
     permission, appelle le repo. (Modèle :
     `internal/service/veridian_contact_breakdown_service.go`.)
   - Câbler dans `internal/app/app.go` à côté du bloc « R1 breakdown contacts »
     (~ligne 1495).
   - Tests colocalisés (`*_test.go`) pour chaque fichier critique (règle 1-pour-1
     Constitution CI §1/§4).

**3. Front — ajouter la carte Reply** :
   - `console/src/services/api/messages_history.ts` (ou un nouveau
     `console/src/services/api/veridian_reply_stats.ts`) : client
     `replyStats({ workspace_id, start, end })`.
   - `console/src/components/analytics/EmailMetricsChart.tsx` : ajouter une 9e
     carte `Statistic` « Replies » (icône type `faReply`/`faComments`), affichant
     `getRate(replied, stats.count_sent)` + tooltip avec le compte brut. La
     fetch reply suit la même fenêtre `timeRange`/`timezone` que le reste.
   - ⚠️ Lingui : les libellés sont des littéraux `` t`...` `` DANS le composant React
     (piège extraction `t` hors composant documenté — memory
     `feedback_lingui_t_param_hors_composant`). Valider par RENDU réel, pas
     `bodyText.length`.

## Fichiers concernés (exacts)

| Rôle | Fichier |
|---|---|
| Donnée (table) | `internal/migrations/v51.go`, `internal/database/init.go:486` |
| Repo (à étendre) | `internal/repository/veridian_contact_reply_postgres.go` |
| Interface (à étendre) | `internal/domain/veridian_contact_reply.go` |
| Mock (régénérer) | `internal/domain/mocks/mock_veridian_contact_reply_repository.go` |
| Handler stats (créer) | `internal/http/veridian_reply_stats_handler.go` (+ test) |
| Service stats (créer) | `internal/service/veridian_reply_stats_service.go` (+ test) |
| Câblage | `internal/app/app.go` (bloc « R1 breakdown contacts » ~L1495) |
| Modèle à copier | `internal/http/veridian_contact_breakdown_handler.go`, `internal/service/veridian_contact_breakdown_service.go`, `internal/repository/veridian_contact_breakdown_postgres.go` |
| Front KPI chart | `console/src/components/analytics/EmailMetricsChart.tsx` |
| Front API client | `console/src/services/api/messages_history.ts` |
| Front montage | `console/src/components/analytics/AnalyticsDashboard.tsx`, `console/src/pages/AnalyticsPage.tsx` |

## Impact tunnel de vente

Sans ce KPI, Robert pilote ses campagnes cold à l'aveugle sur la SEULE métrique qui
compte vraiment (réponses = leads chauds). Open/clic sont pollués (MPP Apple ouvre
tout, gateways de sécu cliquent les liens) ; le reply rate est le signal de
conversion réel. C'est le trou explicitement nommé par Robert. **Priorité 🔴 :
livrer d'abord celui-ci.**

## Notes pour l'implémenteur

- Tier de risque : 🟡 MOYEN (nouvelle route non-auth-critique + nouvelle méthode
  repo lecture seule, additif). Push sans `[risk:low]`, puis reco/promo selon §20.
  Si l'index `(replied_at)` est ajouté → migration = tier 🔴, staging vert exigé.
- Endpoint console interne (JWT) — PAS de HMAC Hub (même posture que le breakdown R1).
- Le reply est **par contact** (1 ligne = 1 contact qui a répondu ≥1 fois), pas par
  message. Le ratio `replied/sent` est donc une approximation acceptable (un contact
  peut avoir reçu plusieurs envois) — documenter ce choix dans le tooltip si besoin.
  Pour un ratio exact « réponses / contacts contactés uniques » il faudrait
  `COUNT(DISTINCT contact_email)` sur message_history ; à trancher par le lead, le
  ratio simple suffit pour un dashboard de pilotage.

---

## ✅ Résolu — 2026-06-16 (agent reply-kpi)

**SHA** : `dec1df0d` (poussé sur `veridian`, staging only, SANS `[risk:low]` —
tier 🟡 : nouvelle route JWT lecture seule + carte UI. Promo prod par le lead
après E2E on-premise staging).

**Choix d'archi retenu** (le plus propre) : **endpoint dédié**
`POST + GET /api/veridian/messages.replyStats`, calqué pixel sur le breakdown
contacts R1. PAS de greffe dans le moteur analytics générique `message_history`
(la donnée reply vit dans la table SÉPARÉE `veridian_contact_reply`, pas de
colonne `count_replied`, pas de jointure dans le moteur → greffe = invasif et
faux). Le backend renvoie `{"replied": N}` brut ; le ratio `replied/sent` est
calculé côté front avec le `count_sent` déjà chargé par `EmailMetricsChart`
(pas de 2e source de vérité pour "sent").

**Pas de migration** : table `veridian_contact_reply` existante (V51), méthode
de COUNT lecture seule additive. PK `contact_email` seul → seq-scan négligeable
sur le volume cold quotidien (index `(replied_at)` à ajouter SI/quand mesuré
chaud, posture daily cap V49). `config.VERSION` NON bumpé.

**Fichiers créés** :
- `internal/domain/veridian_reply_stats.go` (+ `_test.go`) — types + interface
  service + doc du choix d'archi. Test : interface satisfiable + pin JSON shape
  (contrat front `{"replied":N}`).
- `internal/service/veridian_reply_stats_service.go` (+ `_test.go`) — gardien
  auth + permission `contacts:read` (le reply est une donnée contact).
- `internal/http/veridian_reply_stats_handler.go` (+ `_test.go`) — POST+GET
  routés explicitement (piège catchall), parsing start/end ISO → `[since, until[`
  end inclusif (borne exclusive au lendemain, aligné dateRange dashboard).
- `internal/domain/mocks/mock_veridian_reply_stats_service.go` — mock service.
- `console/src/services/api/veridian_reply_stats.ts` (+ `.test.ts`) — client GET.

**Fichiers modifiés** :
- `internal/domain/veridian_contact_reply.go` — +méthode interface
  `CountRepliedSince(ctx, ws, since, until)`.
- `internal/repository/veridian_contact_reply_postgres.go` (+ `_test.go`) —
  impl Postgres (COUNT bornes optionnelles via Squirrel).
- `internal/domain/mocks/mock_veridian_contact_reply_repository.go` — mock régénéré.
- `internal/domain/veridian_contact_reply_test.go` — stub étendu (CountRepliedSince).
- `internal/app/app.go` — câblage du handler (bloc à côté de R1 breakdown).
- `console/src/components/analytics/EmailMetricsChart.tsx` — carte KPI "Replies"
  (icône faComments, `getRate(replied, count_sent)`, tooltip avec compte brut +
  note "#1 cold outreach KPI"). Fetch reply en parallèle, même fenêtre dateRange.
  Best-effort (échec endpoint reply → replied=0, dashboard non cassé).
- `console/src/i18n/locales/*.{po,js}` — extraction + compilation Lingui (msgids
  `Replies` + tooltip présents dans en.po, vérifié → pas de label vide runtime).

**Build/tests verts** :
- `GOMAXPROCS=2 go build ./...` → exit 0.
- `go test ./internal/{domain,repository,service,http}/` → 4/4 packages ok.
- `npx tsc --noEmit` console → exit 0.
- `npx vitest run veridian_reply_stats.test.ts` → 2/2 pass.
- Pre-push : check-test-mapping 100% (mapping 1-pour-1 OK, routes API 100%),
  rate-limit coverage OK, console build (lingui compile + tsc + vite build) OK,
  intégrité React OK, catalogues i18n à jour.

**Reste (lead)** : promo prod après E2E on-premise staging (worker + DB staging
réels). Validation rendu réel de la carte par `?cachebust=` (piège SW cache).
