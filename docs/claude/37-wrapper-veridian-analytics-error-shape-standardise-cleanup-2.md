# Wrapper veridian analytics — error-shape standardisé (cleanup, 2026-06-18)

Aligne les routes analytics sur l'error-shape standard `{"error":"<string>"}`. Le
handler upstream `internal/http/analytics_handler.go:writeErrorResponse` renvoyait
`{"error": true, "message": "..."}` (error = BOOLÉEN), seul endroit de l'API à
diverger de `internal/http/utils.go:WriteJSONError`. Le front était déjà durci
(symptôme `ApiError: true` fermé par le P0 dashboard ci-dessus) → ce lot est du PUR
alignement de cohérence API. Spec : ticket
`todo/done/2026-06-17-analytics-handler-error-shape-non-standard.md`.

- **Convention respectée — ZÉRO patch upstream** : `analytics_handler.go` reste
  intact. Nouveau handler veridian `internal/http/veridian_analytics_handler.go`
  (`VeridianAnalyticsHandler`) qui prend le **MÊME** `domain.AnalyticsService` que
  l'upstream (aucune nouvelle DI), réplique la fine couche transport HTTP et émet
  les erreurs via `WriteJSONError`. Aucune logique métier dupliquée (le service est
  partagé). Réutilise les types `AnalyticsQueryRequest`/`AnalyticsSchemasRequest`
  upstream (même package `http`).
- **Routage (app.go)** : `analyticsHandler := httpHandler.NewVeridianAnalyticsHandler(...)`
  REMPLACE `NewAnalyticsHandler(...)` au point de câblage existant ; la ligne
  `analyticsHandler.RegisterRoutes(a.mux)` est inchangée (le wrapper expose la même
  méthode). Le handler upstream n'est PLUS enregistré dans le mux (sinon panic
  pattern dupliqué). `POST /api/analytics.query` + `POST /api/analytics.schemas`
  routés explicitement (Go 1.22 method routing ; le front ne fait que des POST).
- **NON-RÉGRESSION garantie** : les réponses 200 sont STRICTEMENT identiques à
  l'upstream — `handleQuery` renvoie le `*analytics.Response` brut (data+meta
  top-level, consommé direct par `console/src/services/api/analytics.ts`),
  `handleGetSchemas` renvoie `{"schemas": ...}`. SEULE la forme d'erreur change
  (booléen → string lisible). Test colocalisé `veridian_analytics_handler_test.go`
  prouve les deux : erreur = `{error:"<string>"}` jamais `{error:true}` + succès
  inchangé.

Aucun fichier upstream modifié (le wrapper se substitue au point de routage seul).

