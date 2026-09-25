# FIX P0 — dashboard 500 `bounce_type` absent + garde-fous CI (2026-06-17)

Le KPI bounce hard/soft (commit `5414a3d7`, promu prod) filtrait `bounce_type
ILIKE 'hard%'` sur `message_history` en supposant « la colonne existe depuis
v8 ». **C'était FAUX** : la seule déclaration `bounce_type` dans `init.go`
(ligne 246) appartient à `inbound_webhook_events`, PAS à `message_history` ; le
bloc CREATE TABLE `message_history` ne l'a JAMAIS eue et aucune migration ne
l'ajoutait. Vérifié sur la DB staging réelle : colonne ABSENTE sur TOUS les
workspaces (dasherr873/coldtunnel/canary*). → `POST /api/analytics.query` = 500
`pq: column "bounce_type" does not exist`, tout l'Email Metrics tombe. Le chemin
d'ÉCRITURE du KPI (`SetStatusesIfNotSet` posant `bounce_type`) était aussi cassé.

- **Fix racine (R0, voie A)** : **migration V54** (`internal/migrations/v54.go`,
  workspace-only, additive, idempotente) `ALTER TABLE message_history ADD COLUMN
  IF NOT EXISTS bounce_type VARCHAR(100)` → rattrape les workspaces existants.
  PAS d'index (les mesures filtrent déjà `bounced_at IS NOT NULL` + `created_at`
  indexé ; volume bouncé faible). PAS dans `migrations-pending.txt` (aucun CREATE
  INDEX → safety §12 ne le flag pas). `config.VERSION` 53→54 + fixture
  `manager_test` (AddRow "54"). `init.go` : colonne ajoutée au CREATE TABLE
  `message_history` (cohérence nouveaux workspaces). Le KPI bounce hard/soft
  devient enfin FONCTIONNEL (avant V54 il était mort/cassé).
- **Front — état d'erreur propre** : `console/src/services/api/client.ts` —
  l'`AnalyticsHandler.writeErrorResponse` (upstream) renvoie `{error: true,
  message: "..."}` (`error` = BOOLÉEN), donc `errorData.error` valait `true` →
  `ApiError("true")` → l'écran affichait « ApiError: true ». Fix : extraction
  robuste (string `error` sinon `message` sinon générique). `EmailMetricsChart`
  affiche désormais un bandeau lisible (« Unable to load email metrics ») + le
  détail technique + bouton **Retry**, jamais une valeur brute.
- **Garde-fou CI #1 (smoke E2E dashboard, BLOQUANT)** :
  `tests/e2e-veridian/specs/dashboard-smoke.spec.ts` — provisionne un workspace
  NEUF (HMAC), auto-login, CHARGE le dashboard headless contre le VRAI schéma DB,
  ASSERTE 0 réponse ≥500 sur analytics.query/replyStats/engagementByClass/
  providerBreakdown + 0 erreur console (ApiError/Error) + graphique rendu (carte
  « Sent »). C'est le chaînon manquant : les unit tests mockent la DB et ne
  voient pas un schéma cassé. Tourne dans le job `e2e-staging` existant (pas de
  nouveau job — il fait tourner tout `specs/`). Skip en prod (pas de tenant).
- **Garde-fou CI #2 (Husky test-par-composant analytics)** :
  `scripts/ci/check-test-mapping.sh` — un `console/src/components/analytics/*.tsx`
  modifié sans `.test.tsx` colocalisé BLOQUE le pre-push (états loading/error/
  data, retry, rendu). Vitest mocke la DB → complémentaire du smoke E2E, pas un
  substitut. Test colocalisé livré : `EmailMetricsChart.test.tsx`.

⚠️ **Diffs INLINE supplémentaires** (fix dashboard 500) :

| Fichier upstream | Diff Veridian |
|---|---|
| `internal/database/init.go` | +colonne `message_history.bounce_type VARCHAR(100)` dans le CREATE TABLE (était absente ; la mesure analytics + le write path la supposaient) |
| `config/config.go` | `VERSION` 53.0 → 54.0 |
| `internal/migrations/manager_test.go` | fixture sqlmock `db_version` 53 → 54 |
| `console/src/services/api/client.ts` | extraction d'erreur robuste : string `error` sinon `message` sinon générique (gère `{error: true, message}` d'analytics_handler.go) |
| `console/src/components/analytics/EmailMetricsChart.tsx` | bandeau d'erreur lisible + bouton Retry (jamais de valeur brute « true ») |
| `scripts/ci/check-test-mapping.sh` | +règle front : `analytics/*.tsx` modifié exige `.test.tsx` colocalisé |

Fichiers veridian dédiés : `internal/migrations/v54.go` (+test),
`tests/e2e-veridian/specs/dashboard-smoke.spec.ts`,
`console/src/components/analytics/EmailMetricsChart.test.tsx`.

