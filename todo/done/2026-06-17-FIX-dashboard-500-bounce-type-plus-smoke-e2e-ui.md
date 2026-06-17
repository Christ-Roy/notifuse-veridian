# 🔴 P0 — Dashboard analytics 500 (bounce_type absent) + smoke E2E dashboard CI

> **Sévérité** : 🔴 P0 — le dashboard Email Metrics est CASSÉ (staging ET prod), graphique disparu
> **Owner** : agent notifuse-veridian
> **Créé** : 2026-06-17 (Robert a vu "Error: true" sur le dashboard staging ; diagnostic lead)

## Bug racine (prouvé en conditions réelles)

`POST /api/analytics.query` retourne **500** : `pq: column "bounce_type" does not exist`.
Conséquence : tout l'Email Metrics du dashboard tombe (graphique + KPI), le front affiche
`ApiError: true` (cf. console `AnalyticsPage` : "Failed to fetch email metrics: ApiError: true").

CAUSE : le lot dashboard-kpi (commit `5414a3d7`, EN PROD) a ajouté à `internal/domain/analytics.go`
les mesures `count_bounced_hard` / `count_bounced_soft` avec `{SQL: "bounce_type ILIKE 'hard%'"}`.
MAIS la colonne `message_history.bounce_type` n'est **PAS garantie présente** : déclarée dans
`internal/database/init.go:246` mais **AUCUNE migration ne l'ajoute** (vérifié : grep ADD COLUMN
bounce_type = vide). Sur un workspace neuf (`dasherr873` provisionné en test), la colonne est
ABSENTE → la requête analytics plante. Prod touchée (même lot promu).

## Fix backend (R0, propre — choisir la bonne voie)

Deux options, l'agent tranche selon le terrain :
- **(A) Garantir la colonne** : migration additive `ADD COLUMN IF NOT EXISTS message_history.bounce_type VARCHAR(100)` (workspace-level, idempotente, config.VERSION bump, fixture manager_test). C'est la voie propre si la colonne DOIT exister (les triggers v7/v8/v16/v19 la référencent déjà → elle est censée exister, donc une migration de réconciliation est légitime). **Probablement la bonne voie** car les triggers la supposent déjà.
- **(B) Mesure défensive** : rendre `count_bounced_hard/soft` robuste à l'absence (COALESCE sur colonne optionnelle / guard). Moins propre car les triggers attendent déjà la colonne.
→ Vérifier d'abord POURQUOI la colonne manque sur les workspaces récents alors que init.go la déclare (init.go pas appliqué ? ordre migration ?). Fixer à la racine, pas en surface. Tester sur workspace neuf provisionné : dashboard charge sans 500.

## Garde-fou CI (Robert : "il faut des tests UI imposés par Husky") — LES DEUX

### 1. PRIORITAIRE — Smoke E2E dashboard en CI (aurait attrapé CE bug)
Spec qui : provisionne un workspace NEUF + auto-login + charge le dashboard via navigateur
headless + ASSERTE : 0 réponse HTTP 500 sur les appels analytics (analytics.query, replyStats,
engagementByClass, providerBreakdown) ET 0 erreur console "ApiError"/"Error". C'est le chaînon
qui manquait : rendu réel contre vrai schéma DB. Câbler dans le workflow CI (bloquant) sur push
qui touche console/ OU analytics.go OU les endpoints dashboard. Réutiliser le pattern auto-login
+ wipe des specs e2e-veridian existantes.

### 2. COMPLÉMENT — règle Husky test-par-composant dashboard
Étendre le mapping 1-pour-1 (check-test-mapping) au front : un composant
`console/src/components/analytics/*.tsx` modifié sans son `.test.tsx` colocalisé → pre-push
bloque. Vérifie les états loading/error/data. NB : les mocks ne voient pas le schéma DB (d'où la
priorité au smoke E2E), mais ça attrape les régressions de rendu.

## Front (mineur, inclure dans le fix)
`AnalyticsPage` / `EmailMetricsChart` : afficher un état d'erreur PROPRE (message lisible +
retry) au lieu de `error: true` brut. Ne JAMAIS afficher la valeur booléenne d'erreur à l'écran.

## DoD
- [x] Dashboard charge sans 500 sur workspace neuf — preuve SQL réelle staging :
      la requête `bounce_type ILIKE 'hard%'` reproduisait le 500 sur dasherr873,
      après `ADD COLUMN IF NOT EXISTS bounce_type` (V54) elle renvoie 0|0. Smoke
      E2E (dashboard-smoke.spec.ts) valide le rendu navigateur en CI e2e-staging.
- [x] Migration/fix bounce_type à la racine — V54 (`internal/migrations/v54.go`,
      additive idempotente) + `init.go` (colonne ajoutée au CREATE TABLE) +
      `config.VERSION` 53→54 + fixture manager_test. Cause racine identifiée :
      `bounce_type` n'était déclaré NULLE PART pour message_history (l'init.go:246
      cité partout appartient à inbound_webhook_events).
- [x] Smoke E2E dashboard bloquant — `tests/e2e-veridian/specs/dashboard-smoke.spec.ts`
      (provision→auto-login→charge dashboard headless→0 réponse ≥500→0 erreur
      console→graphique rendu). Tourne dans le job e2e-staging existant (BLOQUANT).
- [x] Règle Husky test colocalisé analytics/ — `scripts/ci/check-test-mapping.sh`
      (negative-test prouvé : ChartVisualization.tsx modifié sans test → bloqué).
      Test livré : `EmailMetricsChart.test.tsx` (3 tests : data/error/retry).
- [x] Front état d'erreur propre — `client.ts` extraction robuste (plus de
      « ApiError: true »), `EmailMetricsChart` bandeau lisible + bouton Retry.
- [ ] Promo prod (tier 🔴) — délégué au team-lead après e2e-staging vert
      (test on-premise = dashboard-smoke charge réellement le dashboard).

## Résolution — 2026-06-17 (agent fix-dashboard)

SHA `b9b3b012` (branche `veridian`). Cause racine : la colonne
`message_history.bounce_type` n'a JAMAIS existé (ni init.go — qui ne la déclarait
que pour `inbound_webhook_events` — ni aucune migration). Le KPI bounce hard/soft
(5414a3d7) la filtrait → 500 sur tous les workspaces (read ET write cassés).
Fix racine voie A : migration V54 + init.go. Garde-fous CI livrés (smoke E2E
dashboard BLOQUANT + règle Husky front analytics). Front durci.
Ticket spin-off : `2026-06-17-analytics-handler-error-shape-non-standard.md` (P3).
