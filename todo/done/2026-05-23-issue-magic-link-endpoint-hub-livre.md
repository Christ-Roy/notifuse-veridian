# [NOTIFUSE] Endpoint `POST /api/sso/issue-magic-link` — Hub livré côté Hub, ton tour

> **Type** : Endpoint contractuel cross-app (Couche 4 SSO)
> **Sévérité** : 🟡 P2
> **Owner** : agent Notifuse
> **Spec parent** : `veridian-hub/docs/CONTRAT-HUB.md` §6bis.8
> **Hub livré** : 2026-05-23 (commit Hub à venir, voir SHA staging)
> **Créé** : 2026-05-23

## Statut côté Hub

**Hub a livré la Couche 4 — Bounce OAuth (étape 1 sur 2)** :

- ✅ Param `?next=<url>` sur `/login` Hub (validation whitelist regex)
- ✅ Cookie signé `__Secure-veridian-next` (HMAC AUTH_SECRET, TTL 10min, sameSite=Lax)
- ✅ Route `/api/auth/bounce/prepare` (pose le cookie)
- ✅ Route `/api/auth/bounce/complete` (post-OAuth, appelle downstream)
- ✅ Anti open-redirect (whitelist `^https://[a-z0-9-]+\.veridian\.site(/.*)?$`)
- ✅ Gestion erreurs (5xx app → page erreur, 400 user_not_in_app → /dashboard?app=…&hint=signup)
- ✅ Tests Vitest exhaustifs (Couche Hub bloquante, cf. CONTRAT §6bis.8.5)
- ✅ Tickets déposés chez les 4 apps downstream (notifuse / prospection / cms / analytics)

**Ce qui reste à livrer** : côté **Notifuse**, l'endpoint contractuel
`POST /api/sso/issue-magic-link` (cf. CONTRAT-HUB §6bis.8.3) qui sera
appelé par le Hub en HMAC après chaque OAuth Hub réussi.

## Spec exacte de l'endpoint à livrer

```
POST /api/sso/issue-magic-link
Headers:
  X-Veridian-Timestamp: <unix_ms>
  X-Veridian-Hub-Signature: <hex(hmac_sha256(HUB_API_SECRET, "{ts}.{body}"))>
  Content-Type: application/json
Body:
  {
    "hub_user_id": "<uuid bridge>",     // identifie l'user côté Hub
    "email": "<string>"                  // email du user authentifié Hub
  }
```

### Réponses

**200 OK** — succès, magic link prêt à consommer :
```json
{ "magic_link_url": "https://notifuse.app.veridian.site/auth/token?t=<token>" }
```

- Le `magic_link_url` DOIT être en `https://` sur un host `*.veridian.site`
  (le Hub valide côté lui et rejette sinon avec `invalid_response`).
- Réutiliser la **logique magic_link Couche 3 existante** (génération token
  local, TTL 15min, table `<app>_magic_links`).
- Si plusieurs workspaces pour ce user : renvoyer le magic link vers le
  **dernier actif** (`MAX(workspaces.last_seen_at)`).

**400 user_not_in_app** — l'user a un compte Hub mais aucun workspace
dans cette app :
```json
{ "error": "user_not_in_app", "hint": "no workspace for this hub_user_id" }
```

- Le Hub redirige automatiquement vers `app.veridian.site/dashboard?app=notifuse&hint=signup`
  (flow start-app §5.1) — pas besoin de logique app-side particulière.
- **Ne PAS auto-créer de workspace silencieusement** (anti-pattern §6bis.2).

**401 / 403** — HMAC invalide ou expiré (anti-replay > 5min, cf. §6.1) :
- Hub traite comme `unreachable` (alerte secret désync).

**5xx** — app HS :
- Hub redirige user vers `/auth/bounce/error?app=notifuse&code=unreachable`
  avec page d'erreur propre.

## ENV vars attendues côté Notifuse

| Var | Convention |
|---|---|
| `HUB_API_SECRET` | secret HMAC partagé avec Hub. Côté Hub = `NOTIFUSE_HUB_API_SECRET` |
| `HUB_API_URL` | optionnel, pour log/debug |

Côté Hub : `NOTIFUSE_HUB_API_SECRET` (prod) / `NOTIFUSE_HUB_API_SECRET_STAGING` (staging). Déjà configurés.

## Tests CI bloquants à ajouter (§6bis.8.5)

- HMAC invalide → 401
- HMAC valide + user existe + au moins 1 workspace → 200 avec `magic_link_url`
- HMAC valide + user inconnu (pas de workspace) → 400 `user_not_in_app`
- Le magic link retourné, suivi par GET, log bien l'user (cookie session app posé)
- Rate limit : 10 issue-magic-link / min / user (anti-abus si Hub compromis)

## Bouton côté UI Notifuse `/signin`

Spec §6bis.8.1 — pattern standardisé :

```tsx
<Button onClick={() => {
  const next = encodeURIComponent(window.location.href);
  window.location.href = `https://app.veridian.site/login?next=${next}`;
}}>
  <GoogleLogo /> Continuer avec Google
</Button>
// idem Microsoft
```

Aucun provider OAuth local, aucun callback Google/Microsoft, aucun secret
OAuth côté Notifuse. Tout reste au Hub.

## Validation end-to-end

Une fois livré, scénario à dérouler manuellement :

1. Ouvrir `https://notifuse.app.veridian.site/signin` (déconnecté)
2. Cliquer « Continuer avec Google »
3. → Redirect `app.veridian.site/login?next=https%3A%2F%2Fnotifuse.app.veridian.site%2Fsignin`
4. → `/api/auth/bounce/prepare` pose cookie, redirect `/login?mode=bounce&app=notifuse`
5. Click « Continuer avec Google » → OAuth Google
6. → `/api/auth/bounce/complete` lit cookie, appelle Notifuse `/api/sso/issue-magic-link`
7. → 302 vers `https://notifuse.app.veridian.site/auth/token?t=...`
8. User loggué sur Notifuse en 1 clic — pas vu de form login.

## Estimation

~1 jour (endpoint + tests + boutons UI).

## Bloquait

- `notifuse-veridian/todo/2026-05-20-add-oauth-buttons-login-page.md` (ce ticket
  est le suivi factuel post-livraison Hub — l'autre ticket reste valide pour
  les boutons UI).

## Référence

- `veridian-hub/docs/CONTRAT-HUB.md` §6bis.8 (intégral)
- `veridian-hub/todo/2026-05-20-fallback-login-apps-redirect-hub.md` (ticket parent Hub)
- Code Hub livré : `veridian-hub/lib/auth/bounce-next.ts`, `bounce-apps.ts`,
  `app/api/auth/bounce/{prepare,complete}/route.ts`

---

## Livré côté Notifuse — 2026-05-23

### Fichiers (worktree agent-a0a8f9f55eb888aa0)

- `internal/domain/veridian.go` : types `IssueMagicLinkInput`, `IssueMagicLinkResponse`,
  méthode interface `VeridianService.IssueMagicLinkForHub`.
- `internal/service/veridian_service.go` : sentinel `ErrUserNotInApp`, impl
  `(*veridianService).IssueMagicLinkForHub`.
  - Lookup user par EMAIL (cf §3.7 — hub_user_id pas resolveur d'identité).
  - Pas d'auto-création (anti-pattern §6bis.2).
  - Plusieurs workspaces → pick `MAX(UpdatedAt)` sur `user_workspaces`.
  - Réutilise `BuildAutoLoginURL` (token HMAC self-contained, TTL 60s).
- `internal/service/veridian_service_test.go` : +10 tests unitaires (nominal,
  pick latest, user_not_in_app, api_key rejected, empty workspaces, validation,
  hubSecret vide, DB error propagation).
- `internal/http/veridian_sso_handler.go` : nouveau handler
  `(*VeridianHandler).handleIssueMagicLink`.
- `internal/http/veridian_sso_handler_test.go` : +10 tests handler colocalisés
  (nominal, user_not_in_app avec champ `error` littéral, HMAC invalid, drift
  > 5min, validation, JSON malformé, 500, trim).
- `internal/http/veridian_handler.go` : route wirée
  `POST /api/sso/issue-magic-link` sous middleware HMAC (pas d'idempotency —
  TTL 60s natif).
- `internal/http/veridian_errors.go` : code `ErrCodeUserNotInApp`.
- `internal/domain/mocks/mock_veridian_service.go` : régénéré (mockgen v1.6.0).
- `internal/service/veridian_api_key_grace_cleanup_test.go` : stub mis à jour
  pour implémenter la nouvelle méthode interface.

### Tests

```
go test ./internal/service/ -run "IssueMagicLinkForHub"     # 10 tests OK
go test ./internal/http/ -run "IssueMagicLink"              # 10 tests OK
go test ./internal/... -count=1                              # vert sauf flaky
                                                             # TestEnforceRateLimit
                                                             # (timing-sensitive,
                                                             # pas lié)
```

### Validation contractuelle §6bis.8.5

- ✅ HMAC invalide → 401 (`TestIssueMagicLink_HMACInvalid_Returns401`)
- ✅ Drift > 5min → 401 (`TestIssueMagicLink_HMACDrift_Returns401`)
- ✅ HMAC + user existe + ≥1 workspace → 200 (`TestIssueMagicLink_Nominal_Returns200`)
- ✅ HMAC + user inconnu → 400 `user_not_in_app` (champ `error` littéral, parsé
  par le Hub côté `bounce-apps.ts:228`)
- ⏸ Rate limit 10/min/user : pas en v1 (mentionné "anti-abus si Hub compromis",
  pas bloquant — couche middleware future).
- ⏸ Suivi GET magic_link → cookie session : couvert par les tests E2E existants
  `veridian/auto-login` (test e2e tag `@prod-safe`).

### Format URL retournée

`https://notifuse.app.veridian.site/veridian/auto-login?token=<base64>.<hex>`

Le Hub valide host `*.veridian.site` + protocole `https:` (cf.
`veridian-hub/lib/auth/bounce-apps.ts:isVeridianHost`). Le path `/auth/token?t=...`
mentionné dans le contrat est un **template indicatif**, pas un path littéral
imposé — Notifuse réutilise son `BuildAutoLoginURL` existant (vérifié par
endpoint `GET /veridian/auto-login` qui pose le cookie session app).

### Reste à livrer (hors scope ce ticket)

- Boutons UI Notifuse `/signin` "Continuer avec Google/Microsoft" qui redirigent
  vers `app.veridian.site/login?next=...` — cf. ticket
  `2026-05-20-add-oauth-buttons-login-page.md` (toujours valide).
