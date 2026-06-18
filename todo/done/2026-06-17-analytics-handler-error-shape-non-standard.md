# Analytics handler renvoie un error-shape non-standard (`{error: true, message}`)

> **Sévérité** : 🔵 P3
> **Owner** : agent notifuse-veridian
> **Créé** : 2026-06-17 (trouvé en fixant le P0 dashboard 500)

## Contexte

En diagnostiquant le P0 dashboard 500 (`bounce_type`), j'ai trouvé que
`internal/http/analytics_handler.go:writeErrorResponse` renvoie un corps
d'erreur **non-standard** :

```go
map[string]interface{}{ "error": true, "message": message }   // error = BOOLÉEN
```

Tout le RESTE de l'API renvoie `{"error": "<message string>"}` (cf.
`internal/http/utils.go:WriteJSONError`, `veridian_errors.go`). Le client front
(`console/src/services/api/client.ts`) faisait historiquement
`errorData?.error || 'An error occurred'` → sur une erreur analytics, `error`
valait `true` (booléen) → `ApiError("true")` → l'UI affichait « ApiError: true »
au lieu du vrai message.

## Ce qui est déjà fait (ce commit)

- **Front rendu robuste** : `client.ts` extrait désormais un message lisible
  (string `error` sinon `message` sinon générique). Le symptôme UI est donc
  CORRIGÉ quelle que soit la forme renvoyée. → pas urgent.

## Demande (cleanup propre, non urgent)

`analytics_handler.go` est un fichier **upstream-pur**. Deux options :
1. (préféré) Aligner sur le standard via un wrapper veridian : router
   `/api/analytics.query` (+ `analytics.schemas`) à travers un handler veridian
   qui réutilise `WriteJSONError` (forme `{error: "<string>"}`), SANS patcher
   le fichier upstream (convention `veridian_*.go`).
2. Laisser tel quel (le front est déjà robuste) et juste DOCUMENTER l'écart
   dans le tableau « Diffs INLINE » si on patche un jour le fichier upstream.

Impact : purement cosmétique (cohérence des erreurs API). Le bug visible est
déjà fermé côté front. À traiter quand un agent passe sur les handlers
analytics ou au prochain sync upstream.

## Fichiers concernés

- `internal/http/analytics_handler.go` (writeErrorResponse, upstream)
- `console/src/services/api/client.ts` (déjà durci, ce commit)
