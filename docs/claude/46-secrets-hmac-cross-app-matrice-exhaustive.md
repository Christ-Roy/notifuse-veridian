# Secrets HMAC cross-app — matrice exhaustive

> **But** : éviter qu'un agent perde 30 min à chercher quel secret /
> header / canonical-string utiliser pour un endpoint HMAC donné. Tout
> nouveau endpoint HMAC cross-app DOIT étendre cette table.
>
> Symétrie : un secret HMAC est partagé entre les 2 parties. Le **même
> matériel cryptographique** vit sous des **noms d'env divergents** côté
> Notifuse vs côté Hub. La colonne "même valeur" l'indique explicitement.

### Vue d'ensemble par flux

| Sens du flux | Env Notifuse | Env Hub | Header signature | Canonical string | Endpoint(s) |
|---|---|---|---|---|---|
| Hub → Notifuse (mutations + reads admin + cron reconcile GET) | `HUB_API_SECRET` | `NOTIFUSE_HUB_API_SECRET` (= même valeur) | `X-Veridian-Hub-Signature` | **Toujours `${ts}.${rawBody}`** quel que soit la méthode HTTP. Pour un GET (body vide), `rawBody = ""` donc canonical = **`${ts}.`** (timestamp + point + chaîne vide). Pattern volontairement "pixel-parfait" côté Hub (`lib/sync/discovery.ts:signGet` lignes 105-114 : "On garde le format `${ts}.${rawBody}` ici aussi (body=''), pour rester pixel-parfait avec les clients existants") afin d'éviter d'avoir 2 conventions HMAC à gérer côté app. Validé par smoke prod 2026-05-25 : `${ts}.` → 200 ; `${ts}.GET.${path}?${query}` → 401. | `/api/tenants/*`, `/api/veridian/admin/*`, `/api/veridian/workspaces/{id}/attach-member`, `/api/sso/issue-magic-link`, **`POST /api/users/by-email`** ET **`GET /api/users/by-email`** (les 2 routées — cf. piège catchall plus bas) |
| Notifuse → Hub (discovery user, GET au login user) | `HUB_API_SECRET` (même secret) | `NOTIFUSE_HUB_API_SECRET` (même) | `x-veridian-hub-signature` (lowercase pour outbound, accepté côté Hub) | **`${ts}.${METHOD}.${pathname}?${sortedQuery}`** — pattern OWASP "HMAC Signing for GET requests" (cf. `veridian-hub/lib/discovery/hmac.ts:buildCanonicalGetString` lignes 80-100). METHOD en majuscule. Tri alphabétique des clés de query (anti-malléabilité proxy). Encodage `encodeURIComponent`-compatible (PAS `url.QueryEscape` côté Go — diverge sur espaces). Réf code Notifuse outbound : `pkg/hub_discovery/client.go:buildCanonicalGetString` lignes 293-302. ⚠️ **Convention différente du flux Hub→Notifuse** : ce sens-là utilise bien `${ts}.METHOD.path?query`, contrairement au flux inverse qui reste sur `${ts}.${rawBody}`. | `GET <hub>/api/users/by-email?email=...` |
| Notifuse → Hub (invitation cross-app) | `HUB_INVITATION_SECRET_NOTIFUSE` | `HUB_INVITATION_SECRET_NOTIFUSE` (même nom) | `x-veridian-invitation-signature` | `${ts}.${rawBody}` | `POST <hub>/api/invitations/create` |
| Notifuse → Hub (webhooks lifecycle) | `HUB_WEBHOOK_SECRET` (+ `HUB_WEBHOOK_URL`) | `NOTIFUSE_HUB_WEBHOOK_SECRET` (= même valeur) | `X-Veridian-Notifuse-Signature` | `${ts}.${rawBody}` | `POST ${HUB_WEBHOOK_URL}` (cf. §7.1 events `tenant.*`, `email.*`) |

### Headers communs à toutes les requêtes HMAC

- **`x-veridian-app`** : nom canonique de l'app caller (`notifuse`,
  `prospection`, `analytics`, `cms`). Côté Hub, sélectionne le bon
  secret. Côté Notifuse, pas vérifié (un seul secret possible).
- **`x-veridian-timestamp`** : Unix epoch en **millisecondes**. Drift
  max anti-replay : **5 minutes** (constante `MaxClockDrift` côté
  Notifuse, équivalent côté Hub). Body lu avec `MaxBodySize = 1 MiB`.

### Où trouver les valeurs

- **Source de vérité dev** : `~/credentials/.all-creds.env` (noms
  canoniques `NOTIFUSE_HUB_API_SECRET`, `NOTIFUSE_HUB_WEBHOOK_SECRET`,
  `HUB_INVITATION_SECRET_NOTIFUSE`, etc.)
- **Prod/Staging** (Dokploy décommissionné 2026-07-10) : les secrets vivent
  dans les **Nomad Variables** du job (`nomad/jobs/notifuse`,
  `nomad/jobs/notifuse-staging`, `nomad/jobs/hub`), injectés au conteneur via
  `template { env = true }`.
- **Inspection live** : `nomad var get nomad/jobs/notifuse` (valeurs de la
  Variable) ou `nomad-v exec <alloc> printenv | grep HUB` (ENV réelles du
  conteneur), plutôt que l'ancienne `POST /api/compose.one` de l'API Dokploy.

### Conventions code (où regarder)

- `internal/http/middleware/veridian_hmac.go` — middleware **inbound**
  (Hub → Notifuse), header `X-Veridian-Hub-Signature`, canonical
  `${ts}.${rawBody}`. 503 si `HUB_API_SECRET` vide (pas 401). Sait
  gérer les deux modes : si body absent (GET), `rawBody = ""` et le
  canonical devient `${ts}.` (timestamp + point sec).
- `internal/http/veridian_discovery_handler.go` — handlers **inbound**
  jumeaux `handleDiscovery` (POST) et `handleDiscoveryGET` (GET) pour
  `/api/users/by-email`. Les deux sont nécessaires : POST sert le
  SDK/UI Notifuse, GET sert le cron reconcile Hub. Si un seul est
  routé, l'autre tombe dans le catchall SPA (cf. piège).
- `pkg/hub_discovery/client.go` — client **outbound** GET signé pour
  `/api/users/by-email`. Constantes `AppHeaderName`,
  `TimestampHeaderName`, `SignatureHeaderName` exportées. **encodage
  `encodeURIComponent`-compatible** (PAS `url.QueryEscape` — diverge sur
  espaces et caractères réservés). Tri alphabétique des clés impératif
  pour matcher `lib/discovery/hmac.ts` côté Hub.
- `internal/service/veridian_hub_invitation_client.go` — client
  **outbound** POST signé pour `/api/invitations/create`. Headers
  lowercase. Mode "disabled" silencieux si `HUB_INVITATION_SECRET_NOTIFUSE`
  vide (retourne `ErrHubInvitationDisabled`).
- `internal/service/veridian_webhook_emitter.go` — emitter **outbound**
  POST best-effort vers `HUB_WEBHOOK_URL`. Header `X-Veridian-Notifuse-Signature`
  (note : **différent** du header inbound, car le Hub a besoin de
  distinguer l'origine du signal). Noop si URL ou secret manquants.

### Pièges historiques (vécus, pas hypothétiques)

- **Catchall `root_handler.go` qui mange les méthodes HTTP non routées**
  (incident 2026-05-25, P0 prod) : Go 1.22+ exige la méthode HTTP
  explicite dans `mux.Handle("METHOD /path", ...)`. Si tu route
  uniquement `POST /api/foo` et qu'un caller appelle `GET /api/foo`, le
  mux **ne match pas** → la requête tombe sur le catchall
  `root_handler.go` qui sert la **SPA console** (HTML 200) → le caller
  parse le body comme JSON et lit "body vide" silencieusement. Aucun log
  d'erreur côté Notifuse, aucun 404, juste un 200 trompeur. Vécu :
  17 faux positifs `tenant_missing_app` côté Hub reconcile cron parce
  que `GET /api/users/by-email` n'était pas routé. **Règle** : pour tout
  endpoint HMAC critique, router POST **ET** GET explicitement (même si
  un seul est utilisé aujourd'hui — l'autre cause un crash silencieux le
  jour où un nouveau caller arrive). Smoke test obligatoire : `curl -X
  <METHOD>` chaque méthode déclarée dans la matrice ci-dessus.
- **`HUB_INVITATION_SECRET_NOTIFUSE` absent des composes Dokploy prod**
  jusqu'au 2026-05-23 (corrigé en session). Toujours vérifier les ENV
  des **DEUX** composes (Notifuse `WN0jglLj5bDIrXUFZHNmw` ET Hub
  `_kxAHDCv1LhvsdwNRX3Vk`) en parallèle quand un nouveau secret HMAC
  est introduit — sinon HMAC marche en staging et plante en prod.
  À noter : ce secret n'est PAS dans `infra/compose/{staging,prod}.yml`
  du repo Notifuse, il est injecté directement via l'UI/API Dokploy.
- **Canonical string ASYMÉTRIQUE selon le sens du flux pour le MÊME
  secret** (`HUB_API_SECRET` / `NOTIFUSE_HUB_API_SECRET`) :
  - **Hub → Notifuse** (inbound côté app, y compris GET cron reconcile) :
    `${ts}.${rawBody}` toujours. GET = `${ts}.` (body vide).
    Réf : `internal/http/middleware/veridian_hmac.go` (Notifuse inbound)
    + `veridian-hub/lib/sync/discovery.ts:signGet` (Hub outbound).
  - **Notifuse → Hub** (outbound discovery) :
    `${ts}.${METHOD}.${pathname}?${sortedQuery}`.
    Réf : `pkg/hub_discovery/client.go:buildCanonicalGetString` (Notifuse
    outbound) + `veridian-hub/lib/discovery/hmac.ts:buildCanonicalGetString`
    (Hub inbound).

  Coller le mauvais format = `401 Invalid signature` opaque. Le smoke
  prod 2026-05-25 a confirmé l'asymétrie : un canonical
  `${ts}.GET.${path}?${query}` envoyé sur le flux Hub→Notifuse retourne
  401 ; seul `${ts}.` (body vide) passe.
- **Tri alphabétique des query params obligatoire** sur la signature GET
  côté Notifuse→Hub uniquement (le flux Hub→Notifuse ne signe pas le path).
  Cf. `encodeSortedQuery` dans `pkg/hub_discovery/client.go`.
- **Ne JAMAIS écrire une cellule de cette matrice sans avoir lu le code
  source des DEUX parties (client signataire + serveur vérificateur)**.
  Cette matrice a été corrigée en v3 (2026-05-25) après que le team-lead
  vague 4 ait perdu plusieurs minutes en validation P0 parce que la v2
  documentait `${ts}.GET.${path}?${sortedQuery}` côté flux Hub→Notifuse
  alors que le vrai code Hub `lib/sync/discovery.ts:signGet` (lignes
  105-114) signe `${ts}.`. Réflexe minimum avant d'éditer cette section :
  `grep -rn "createHmac\|hmac.New" <repo>/{lib,pkg,internal}/` côté
  caller ET côté receiver, et coller la ligne de code exacte dans la
  cellule (pas une paraphrase). Une doc qui contredit le code est pire
  qu'une doc absente — elle envoie les agents dans le mur avec
  confiance.
- **Header webhook ≠ header inbound** : outbound webhook utilise
  `X-Veridian-Notifuse-Signature`, inbound mutations utilise
  `X-Veridian-Hub-Signature`. Symétrie volontaire : le destinataire sait
  à quel secret matcher.
- **Validation UUID stricte côté Notifuse `hub_user_id`** (V46) : si
  payload Hub envoie un non-UUID, stocké comme NULL silencieusement
  (cf. fix `7d5b352d`). HMAC valide mais data perdue.
- **Casse des headers HTTP** : Go normalise via `r.Header.Get` (case
  insensitive) donc lowercase outbound et CamelCase inbound coexistent
  sans problème. Ne pas s'alarmer si on voit les deux dans le code.

### Vue Hub-side

La VUE Hub (quels endpoints exposent l'inbound HMAC pour chaque app,
quelles ENV `<APP>_HUB_API_SECRET` sont définies) est maintenue côté
Hub. Voir `../veridian-hub/CLAUDE.md` section équivalente — ticket
ouvert `../veridian-hub/todo/2026-05-25-secrets-hmac-cross-app-doc-CLAUDE.md`
pour la créer en miroir.

---

