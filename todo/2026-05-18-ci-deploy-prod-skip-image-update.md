# 2026-05-18 — CI Deploy prod ne push pas la nouvelle image

> **Priorité** : 🔴 P0 — **faux positif majeur**. La CI dit "deploy prod success", mais le container prod reste sur l'ancienne image.
> **Origine** : observation directe Robert Brunon — ship V31+V32 (commits `a2bcbc02` + `eb7a88e2`) reportés "deploy prod success" + "e2e prod success", mais `docker inspect notifuse-prod` 2026-05-18 12:50 UTC montre toujours `v30.1-veridian.f36db0e1` (image d'il y a 5h).
> **Fixé manuellement** : oui (compose.update Dokploy + compose.redeploy → v32.0-veridian.eb7a88e2). Voir backup `/tmp/dokploy-backups/notifuse-prod-env-1779108170.txt`.

## Cause racine

Job `deploy-prod` dans `.github/workflows/veridian-ci.yml` (lignes ~662-668) appelle uniquement :

```bash
curl -fsS -X POST -H "x-api-key: ${{ secrets.DOKPLOY_API_TOKEN }}" \
  -H "Content-Type: application/json" \
  "${{ secrets.DOKPLOY_URL }}/api/compose.redeploy" \
  -d '{"composeId":"${{ secrets.DOKPLOY_NOTIFUSE_PROD_COMPOSE_ID }}"}'
```

**Manque** : la mise à jour de `NOTIFUSE_IMAGE_TAG` et `NOTIFUSE_IMAGE_DIGEST` côté env Dokploy via `POST /api/compose.update`. Sans cet update, Dokploy redéploie avec les vars d'env existantes, qui pointent toujours vers l'ancien tag.

Le compose lui-même pin l'image via `${NOTIFUSE_IMAGE_TAG:-saas-v1.0.3}@sha256:${NOTIFUSE_IMAGE_DIGEST:-...}`, donc l'image effectivement tirée dépend 100% des vars d'env Dokploy.

## Pourquoi e2e-prod a passé

- `/api/setup.status` répond 200 sur l'ancien container (le service tourne, juste avec du vieux code).
- Les tests Playwright `@prod-safe` sont read-only (login, navigation), n'ont pas de méthode pour détecter "est-ce que le nouveau code est en prod".
- Conséquence : faux positif vert sur les 2 derniers ships qui touchaient des migrations.

## Fix proposé

Ajouter un step **avant** `Trigger Dokploy redeploy (prod)` :

```yaml
- name: Update Dokploy env (image tag + digest)
  env:
    DOKPLOY_URL: ${{ secrets.DOKPLOY_URL }}
    DOKPLOY_TOKEN: ${{ secrets.DOKPLOY_API_TOKEN }}
    COMPOSE_ID: ${{ secrets.DOKPLOY_NOTIFUSE_PROD_COMPOSE_ID }}
    NEW_TAG: ${{ needs.build.outputs.image_tag }}
    NEW_DIGEST: ${{ needs.build.outputs.image_digest }}  # à exposer côté build job
  run: |
    set -euo pipefail
    # 1. Get current env
    CUR=$(curl -fsS -H "x-api-key: $DOKPLOY_TOKEN" \
            "$DOKPLOY_URL/api/compose.one?composeId=$COMPOSE_ID" \
            | jq -r '.env')
    # 2. Patch les 2 lignes
    NEW=$(echo "$CUR" | sed \
            -e "s|^NOTIFUSE_IMAGE_TAG=.*|NOTIFUSE_IMAGE_TAG=$NEW_TAG|" \
            -e "s|^NOTIFUSE_IMAGE_DIGEST=.*|NOTIFUSE_IMAGE_DIGEST=$NEW_DIGEST|")
    # 3. Push update
    jq -n --arg cid "$COMPOSE_ID" --arg env "$NEW" \
       '{composeId: $cid, env: $env}' \
      | curl -fsS -X POST -H "x-api-key: $DOKPLOY_TOKEN" \
              -H "Content-Type: application/json" \
              "$DOKPLOY_URL/api/compose.update" -d @-
    echo "✓ Dokploy env updated to tag=$NEW_TAG digest=$NEW_DIGEST"
```

Puis le step `Trigger Dokploy redeploy (prod)` existant prend l'effet attendu.

### Pré-requis côté job `build`

Exposer `image_digest` en output (le job a déjà `image_tag`) :

```yaml
outputs:
  image_tag: ${{ steps.meta.outputs.tag }}
  image_digest: ${{ steps.meta.outputs.digest }}  # à ajouter
```

Le `digest` est dispo en sortie de `docker/build-push-action@v6` via `${{ steps.push.outputs.digest }}`.

### Renforcement post-deploy (anti-faux-positif futur)

Ajouter un check de version dans le `Wait for prod healthy` :

```yaml
- name: Verify prod runs new code
  run: |
    EXPECTED="${{ needs.build.outputs.image_tag }}"
    for i in {1..30}; do
      VER=$(curl -sf https://notifuse.app.veridian.site/api/version 2>/dev/null | jq -r '.image_tag // empty')
      if [ "$VER" = "$EXPECTED" ]; then
        echo "✓ Prod runs $VER"
        exit 0
      fi
      sleep 10
    done
    echo "::error::Prod still on '$VER' after 5min, expected '$EXPECTED'"
    exit 1
```

Endpoint `/api/version` à ajouter côté Notifuse (lit `os.Getenv("IMAGE_TAG")` injecté par compose). Trivial. **C'est le défense en profondeur** qui aurait évité ce faux positif.

## Impact business

- Les 2 fixes shippés aujourd'hui (V31 health backfill + V32 hide api_key) étaient invisibles en prod jusqu'à la correction manuelle ~12h50 UTC.
- Si un agent Hub avait branché son cron health entretemps, il aurait continué de faire des 404 sur les 9 tenants legacy car la migration V31 n'avait pas tourné.

## Hors-scope

- Endpoint `/api/version` peut être livré séparément si plus pratique.
- Le ticket assume que `compose.update` Dokploy accepte le payload `{composeId, env}` — confirmé par la session manuelle. Vérifier si d'autres champs sont nécessaires (e.g. `composeFile`).
- Memory `project_dokploy_improvements` à updater une fois fixé.
