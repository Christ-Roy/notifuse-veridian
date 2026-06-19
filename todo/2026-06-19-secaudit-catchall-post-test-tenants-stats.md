# POST /api/veridian/admin/test-tenants-stats tombe dans le catchall SPA (200 body vide)

> **Sévérité** : 🔵 P3 (robustesse, PAS un trou de sécu — read-only, non-destructif)
> **Owner** : agent notifuse
> **Créé** : 2026-06-19
> **Auteur** : audit sécu + délivrabilité

## Constat (observé en prod)

`internal/http/veridian_handler.go:196` ne route que la méthode **GET** :

```go
mux.Handle("GET /api/veridian/admin/test-tenants-stats", hmac(http.HandlerFunc(h.handleTestTenantsStats)))
```

Aucune route `POST`. Go 1.22+ exige la méthode explicite dans le pattern → un
`POST /api/veridian/admin/test-tenants-stats` ne matche AUCUNE route et tombe
dans le catchall `root_handler.go` (SPA console).

Smoke prod réel (2026-06-19, v54.0-veridian.3bbc1cce) :

```
POST /api/veridian/admin/test-tenants-stats (no HMAC) → 200, body vide (0 octet), pas de Content-Type JSON
GET  /api/veridian/admin/test-tenants-stats (no HMAC) → 401  (HMAC OK)
```

C'est le MÊME piège que l'incident P0 2026-05-25 (`GET /api/users/by-email` non
routé → catchall → 17 faux positifs `tenant_missing_app` côté Hub), documenté
dans CLAUDE.md « Pièges historiques » et « Catchall root_handler.go ».

## Pourquoi ce n'est PAS critique ici

- Le handler `handleTestTenantsStats` est **read-only** (stats cron) et **n'est
  jamais atteint** par le POST (il tombe dans le catchall AVANT).
- Le catchall renvoie une réponse SPA inerte (200 vide), **aucune donnée ne
  fuit, aucune exécution**.
- Le vrai handler GET renvoie de toute façon **503 en prod** (cron non injecté,
  staging-only de fait).
- Les deux autres endpoints staging-only DESTRUCTIFS sont propres :
  `cold-simulate` (POST routé via writeRoute=HMAC) et `gc-orphan-workspace-dbs`
  (POST **et** GET routés) renvoient **503 staging-only avec HMAC valide en
  prod** (vérifié au smoke).

## Risque résiduel

Un futur caller (cron Hub, script de monitoring) qui appellerait ce path en
POST recevrait un **200 trompeur à body vide** au lieu d'un 405/401 clair, et
parserait « stats vides » silencieusement — exactement le scénario du faux
positif 2026-05-25. Dette de robustesse à fermer avant que quelqu'un construise
un client POST dessus.

## Fix proposé (NON appliqué — ticket d'audit)

Router explicitement le POST (même handler, idempotent read-only), aligné sur
la règle CLAUDE.md « pour tout endpoint HMAC, router POST ET GET » :

```go
mux.Handle("GET /api/veridian/admin/test-tenants-stats",  hmac(http.HandlerFunc(h.handleTestTenantsStats)))
mux.Handle("POST /api/veridian/admin/test-tenants-stats", hmac(http.HandlerFunc(h.handleTestTenantsStats)))
```

(read-only → pas besoin d'Idempotency, `hmac` suffit, pas `writeRoute`.)

Bonus : un garde-fou CI pourrait grep les `mux.Handle("GET /api/veridian/...` et
alerter si le POST jumeau n'est pas routé (anti-catchall systématique).
