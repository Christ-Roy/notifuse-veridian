# [NOTIFUSE] BUG PROD — GET /api/users/by-email retourne 200 + body vide

> **Type** : Bug runtime prod — discovery cross-app cassée
> **Sévérité** : 🔴 P0 — bloque la couche discovery Hub + futur Mail Gateway
> **Owner** : agent Notifuse
> **Créé** : 2026-05-25 par team-lead Hub
> **Découvert via** : audit tenant drift cross-app (commit Hub `92a4cfe`)
> **Refs** :
> - Audit Hub : `veridian-hub/docs/AUDIT-TENANT-DRIFT-2026-05-25.md`
> - Contrat : `veridian-hub/docs/CONTRAT-HUB.md` §6bis (discovery cross-app)

---

## Symptôme

Le cron reconcile Hub (`lib/sync/reconcile.ts`) a tourné en prod le 2026-05-25
et a détecté **17 faux positifs `tenant_missing_app`** côté Notifuse :

```
GET https://notifuse.app.veridian.site/api/users/by-email?email=<user>
  Headers HMAC Pattern A (NOTIFUSE_HUB_API_SECRET)
  → HTTP 200
  → Body: <EMPTY>
```

Le reconcile parse `body.user` qui est `undefined` → conclut que l'user
n'existe pas chez Notifuse → classe le tenant en `tenant_missing_app`
faux positif.

## Impact

1. **17 tenants Hub correctement provisionnés Notifuse** classés à tort
   en drift dans le rapport audit
2. **Couche 4 OAuth bounce** (Hub → Notifuse via discovery) peut casser
   silencieusement si un user veut bounce vers Notifuse
3. **Futur Mail Gateway Hub** (cf
   `veridian-hub/todo/2026-05-25-mail-gateway-hub-multi-provider.md`)
   sera bloqué — il a besoin de discovery pour vérifier que l'user a
   bien un compte Notifuse avant de router

## Diagnostic à faire

1. Reproduire en prod : `curl -X GET 'https://notifuse.app.veridian.site/api/users/by-email?email=<un_user_réel>' -H "<HMAC headers>"`
2. Inspecter logs Notifuse prod pour cet endpoint
3. Vérifier qu'il y a bien un handler `GET /api/users/by-email` et pas
   juste un stub 200
4. Si handler existe : vérifier le shape de réponse attendu vs ce qui
   est retourné (`{user: {...}}` vs body vide vs `{user: null}`)

## Contrat attendu (Hub side)

Le Hub attend, d'après `lib/sync/discovery.ts:queryAppForUserByEmail` :

```json
// Si user existe côté Notifuse
{
  "user": {
    "id": "...",
    "email": "...",
    "supabase_user_id": "...",
    // autres champs metadata
  },
  "tenants": [...]
}

// Si user n'existe pas
HTTP 404 + { "error": "not_found" }
// OU
HTTP 200 + { "user": null }
```

**Body vide HTTP 200 = non conforme au contrat**.

## Fix attendu

Soit :
- (A) Le handler n'existe pas en prod → l'implémenter
- (B) Le handler existe mais a un bug → corriger le shape de réponse
- (C) Différence build dev vs prod → debug ENV/router

## Coordination

Une fois fix livré, ping team-lead Hub pour re-run du cron reconcile et
vérifier que les 17 faux positifs disparaissent.

## Note

Le bug est **silencieux** : Notifuse retourne 200, le Hub considère que
c'est un user inconnu (pas une erreur), donc personne n'a alerté. C'est
exactement le genre de bug que les MEGA-E2E doivent attraper.

---

## ✅ Archivé — 2026-05-25 (team-lead)
Livré en prod sur SHA 8f90b538 (giga E2E 226/241 passed). Voir notes ci-dessus.
