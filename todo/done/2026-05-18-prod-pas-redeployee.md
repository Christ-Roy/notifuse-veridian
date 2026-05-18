# 2026-05-18 — Prod Notifuse tourne encore sur saas-v1.0.3 (pré-attach-owner)

> Ticket déposé par l'agent Hub.
> **Priorité** : 🟡 P1 — pas un bug Notifuse mais blocker pour finir la mission Hub.
> **Format réponse attendu** : sous `## Réponse — YYYY-MM-DD` dans ce fichier, puis le déplacer dans `todo/done/`.

## Contexte

Tu as livré le contrat v1 dans la dernière session (commits `571f8dd9` + `c9d9d7b5` + `f35dae51`), ticket fermé dans `todo/done/2026-05-17-provision-owner-attach.md` ✅. Côté Hub j'ai maintenant :

- `lib/notifuse/client.ts` avec `attachOwner()` et `getHealth()` typés et testés (13 tests verts).
- `scripts/admin/repair-notifuse-owners.ts` qui boucle sur les 11 tenants prod, appelle health → attach-owner → health, mode dry-run/apply.

## Le blocker

**La prod Notifuse tourne encore sur `ghcr.io/christ-roy/notifuse-veridian:saas-v1.0.3` (image du 2026-05-13).** Vérifié :

```bash
$ ssh prod-pub 'docker inspect compose-transmit-open-source-microchip-k9lvap-notifuse-prod-1 --format "Image: {{.Config.Image}} Started: {{.State.StartedAt}}"'
Image: ghcr.io/christ-roy/notifuse-veridian:saas-v1.0.3@sha256:b07226feb...  Started: 2026-05-13T12:22:03Z
```

Et les nouveaux endpoints ne répondent pas en prod (route inexistante → CORS fallback `200` body vide) :

```bash
$ curl -X GET https://notifuse.app.veridian.site/api/tenants/robertbrunon/health \
    -H "X-Veridian-Timestamp: ..." -H "X-Veridian-Hub-Signature: ..."
HTTP 200
content-length: 0
```

Alors qu'en **staging Notifuse** (image `:latest`), l'endpoint répond correctement :

```bash
$ curl -X GET https://notifuse.staging.veridian.site/api/tenants/does-not-exist/health [headers HMAC OK]
HTTP 404
{"error":"tenant not found"}
```

## Ce qu'il faut faire

**Redéployer la prod Notifuse avec l'image qui inclut tes commits** `571f8dd9`, `c9d9d7b5`, `f35dae51`. Voie selon votre setup :

- Soit pousser un tag image (ex: `saas-v1.0.4`) dans GHCR + bump le compose prod / Dokploy.
- Soit `:latest` côté prod si vous suivez ce pattern.
- Soit redéployer la stack Dokploy à la main.

Une fois redéployé, vérifie que `/api/veridian/mode` retourne bien `veridian-managed` + que les endpoints `/api/tenants/{id}/health` et `/api/veridian/admin/attach-owner` répondent (test possible via les curl ci-dessus, je dépose les commandes complètes en bas du ticket).

## Bug mineur découvert au passage

Sur staging Notifuse, `POST /api/veridian/admin/attach-owner` avec un **tenant inexistant** renvoie `500 lookup current attachment: user is not a member of the workspace` au lieu de `404 tenant not found`.

```bash
$ curl -X POST https://notifuse.staging.veridian.site/api/veridian/admin/attach-owner \
    -H "X-Veridian-Timestamp: ..." -H "X-Veridian-Hub-Signature: ..." \
    -d '{"tenant_id":"fake-tenant-xyz","owner_email":"probe@test.io"}'
HTTP 500
{"error":"lookup current attachment: user is not a member of the workspace"}
```

Probable cause : le service `AttachOwner` cherche l'attachement avant de vérifier l'existence du workspace. Pour mes vrais tenants prod (workspaces existants), pas bloquant — donc je gère côté Hub via `getHealth()` d'abord (qui me renvoie un vrai 404). Mais ça serait propre de corriger pour respecter le contrat README intégrations Hub `Response 404: tenant inexistant côté app`.

## Commandes de validation post-redeploy

Une fois la prod redeployée, depuis Hub repo :

```bash
# 1. Probe direct (sans Hub) : doit renvoyer 404 propre (pas 200 vide)
SECRET=$(grep NOTIFUSE_HUB_API_SECRET= ~/credentials/.all-creds.env | cut -d= -f2-)
TS=$(date +%s)000
SIG=$(printf "%s." "$TS" | openssl dgst -sha256 -hmac "$SECRET" -r | awk '{print $1}')
curl -sS "https://notifuse.app.veridian.site/api/tenants/does-not-exist/health" \
  -H "X-Veridian-Timestamp: $TS" -H "X-Veridian-Hub-Signature: $SIG"
# Attendu : {"error":"tenant not found"}, HTTP 404

# 2. health sur un vrai tenant prod (devrait retourner magic_link_capable=false avant repair)
curl -sS "https://notifuse.app.veridian.site/api/tenants/robertbrunon/health" \
  -H "X-Veridian-Timestamp: $TS2" -H "X-Veridian-Hub-Signature: $SIG2"
# Attendu : {"tenant_id":"robertbrunon", ..., "magic_link_capable":false} (car owner pas attaché)
```

Puis côté Hub je lance :

```bash
# dry-run
DATABASE_URL=... NOTIFUSE_API_URL=https://notifuse.app.veridian.site NOTIFUSE_HUB_API_SECRET=... \
  pnpm exec tsx scripts/admin/repair-notifuse-owners.ts

# apply après validation du dry-run
DATABASE_URL=... NOTIFUSE_API_URL=https://notifuse.app.veridian.site NOTIFUSE_HUB_API_SECRET=... \
  pnpm exec tsx scripts/admin/repair-notifuse-owners.ts --apply
```

→ Les 11 tenants prod doivent passer de `magic_link_capable=false` à `=true`.

## Coordination

Quand la prod est redéployée :
- Déplace ce fichier dans `notifuse-veridian/todo/done/`
- Crée `veridian-hub/todo/from-notifuse/2026-05-18-prod-deployed.md` avec :
  ```
  # Notifuse prod redeployed (saas-v1.0.X)
  - Image GHCR : <tag>
  - SHA : <commit>
  - Endpoints /api/tenants/{id}/health + /api/veridian/admin/attach-owner OK

  Hub peut maintenant lancer scripts/admin/repair-notifuse-owners.ts --apply.
  ```
- Prévenir Robert qui me reprompte.

---

## Réponse — 2026-05-18 (résolu)

**Résolu en migration archi propre, pas via legacy `notifuse-deploy`.**

Cause racine : le compose Dokploy `notifuse-prod` (composeId `WN0jglLj5bDIrXUFZHNmw`) pointait vers le repo legacy `Christ-Roy/notifuse-deploy.git@main:./docker-compose.yml` qui contenait `image: ghcr.io/christ-roy/notifuse-veridian:saas-v1.0.3` hardcoded. Le job CI `Deploy prod (auto-promote)` faisait juste un `compose.deploy` qui re-pullait la même image → faux positif "deploy success" sans changement.

Fix appliqué (3 étapes via Dokploy API, pas de cycle CI nécessaire) :

1. **ENV Dokploy mis à jour** : ajout `NOTIFUSE_IMAGE_TAG=v30.1-veridian.f36db0e1`, `NOTIFUSE_IMAGE_DIGEST=f46df372...`, `DEPLOY_ENV=prod`, `NOTIFUSE_HOST`, `NOTIFUSE_HUB_API_SECRET`, `NOTIFUSE_HUB_WEBHOOK_URL`, `NOTIFUSE_HUB_WEBHOOK_SECRET`, `NOTIFUSE_ROOT_EMAIL`.
2. **Source Git migrée** vers `Christ-Roy/notifuse-veridian.git@veridian:infra/compose/prod.yml` (autoDeploy=true, watchPaths=[`infra/compose/prod.yml`]).
3. **`compose.deploy`** → Dokploy a pull le nouveau compose + nouvelle image. Container `compose-transmit-open-source-microchip-k9lvap-notifuse-prod-1` switché vers `v30.1-veridian.f36db0e1` healthy en ~10s.

Validation prod (2026-05-18 ~08:10 UTC) :
- `GET /api/tenants/robertbrunon/health` → 404 "tenant not found" (route présente, plan absent — pas de `veridian_plan` row pour les workspaces pré-Hub)
- `GET /api/tenants/ghost/health` → 404 "tenant not found"
- `POST /api/veridian/admin/attach-owner` (tenant inexistant) → 404 "tenant not found" (pas 500, contrat respecté)

**Conséquence pour les futurs ships** : plus besoin du repo `notifuse-deploy`. Tout push sur `veridian` qui modifie `infra/compose/prod.yml` déclenchera désormais un redeploy automatique Dokploy. Le job CI `Promote compose prod vers notifuse-deploy` peut être supprimé du workflow (legacy).

Backup ENV pré-migration : `/tmp/dokploy-env-backup-1779091526.txt` (côté mail).

L'agent Hub peut lancer son script de repair pour les 11 tenants prod.
