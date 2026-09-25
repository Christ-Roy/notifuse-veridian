# Rate-limit GLOBAL de l'API (OWASP API4:2023, 2026-06-15)

Le `pkg/ratelimiter` upstream n'était appliqué qu'À LA MAIN, handler par
handler, et UNIQUEMENT sur les endpoints publics non-auth (subscribe /
preferences). TOUS les endpoints JWT/HMAC (dont le custom Veridian cold :
breakdown, attach-member, sso, automations.*) étaient sans rate-limit → trou
API4:2023 (Unrestricted Resource Consumption), critique vu que Notifuse va être
peuplé en prod. Fermé par un middleware GLOBAL en defense-in-depth.

- **Fichier veridian** : `internal/http/middleware/veridian_rate_limit.go`
  (`VeridianAPIRateLimitMiddleware`). Wrappe le handler racine → couvre TOUTES
  les routes du mux `a.mux` par construction. Limite par 2 dimensions
  indépendantes : **IP** (X-Forwarded-For premier hop, défaut 300/min) +
  **identité authentifiée best-effort** (user_id décodé du Bearer JWT SANS
  valider la signature, ou `x-veridian-app`+workspace HMAC ; défaut 600/min).
  L'identité est une clé de bucketing, PAS une frontière de sécu (un user_id
  forgé se fait juste limiter sur une autre clé ; la dimension IP reste). 429 +
  header `Retry-After`. Réutilise le RateLimiter PARTAGÉ `a.rateLimiter` (pas de
  2e goroutine de cleanup). Best-effort : `rl==nil` ou désactivé = passthrough.
- **Exemptions** : `/api/health`, `/api/version`, `/api/tenants/{id}/health`
  (observabilité + smoke CI) + flux HMAC Hub (`X-Veridian-Hub-Signature`
  présent : déjà protégé signature+anti-replay, et le cron reconcile Hub fait
  des rafales légitimes — limite basse = faux positifs `tenant_missing_app`).
  Tout ce qui n'est pas sous `/api/` (console SPA, assets, `/subscribe`,
  `/preferences`, `/health`, `/healthz`) n'est pas inspecté.
- **Config** : ENV `VERIDIAN_API_RATE_LIMIT_{ENABLED,PER_IP,PER_IDENTITY}`, lue
  AU CÂBLAGE dans le middleware (pattern `VeridianSecurityHeadersMiddleware` qui
  lit `CORS_ALLOW_ORIGIN`) — **PAS** d'ajout au struct `config.Config` upstream.
  Défaut activé + seuils sains ; valeur invalide → défaut (best-effort boot).
- **Garde-fou Husky/CI** : `scripts/ci/check-rate-limit-coverage.sh` (appelé par
  `.husky/pre-push` + step CI dans le job `test-mapping`). BLOQUE si le
  middleware global est dé-câblé d'app.go (règle 1). ALERTE non bloquant sur un
  `http.NewServeMux()` hors allowlist (`app.go` + `pkg/tracing` metrics) — un mux
  parallèle servant de l'API la bypasserait. Le script documente honnêtement ses
  limites (pas d'analyse de flot, ne vérifie pas les seuils).

⚠️ **Diffs INLINE supplémentaires** (rate-limit global) :

| Fichier upstream | Diff Veridian |
|---|---|
| `internal/app/app.go` | +câblage `middleware.VeridianAPIRateLimitMiddleware(a.rateLimiter, ...)` dans `Start()`, entre graceful-shutdown et tracing (rejet 429 avant les lookups DB paywall/frozen) |
| `.husky/pre-push` | +étape appelant `check-rate-limit-coverage.sh` (câblage bloquant, mux parallèle informatif) |
| `.github/workflows/veridian-ci.yml` | +step `Run check-rate-limit-coverage.sh` dans le job `test-mapping` (transverse, tous events) |

---

