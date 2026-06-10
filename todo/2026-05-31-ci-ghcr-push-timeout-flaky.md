# CI : push image GHCR timeout récurrent (DeadlineExceeded)

> **Sévérité** : 🟡 P1
> **Owner** : agent notifuse-veridian / infra
> **Créé** : 2026-05-31

## Problème

Le job "Build & push image (self-hosted)" échoue de façon **récurrente** sur le
push vers GHCR :

```
buildx failed with: ERROR: failed to build: failed to solve: DeadlineExceeded:
failed to push ghcr.io/christ-roy/notifuse-veridian:<tag>:
no active session for <id>: context deadline exceeded
```

Observé 3 fois sur 4 builds le 2026-05-30/31 (runs 26694073507, 26697397495).
Le build dure jusqu'à 31-39 min avant de timeout sur le PUSH (le build image
lui-même réussit, c'est le push layer vers ghcr.io qui meurt). Bloque toute
promotion prod ; chaque échec = rerun manuel.

## Pistes

1. **Timeout buildx trop court vs réseau du runner self-hosted** : le runner
   (dev-pub OVH) a peut-être un débit upload limité vers ghcr.io. Augmenter le
   timeout push buildx / ajouter `--push` avec retry, ou
   `BUILDKIT_STEP_LOG_MAX_SIZE` / timeout dédié.
2. **Layers trop gros** : vérifier la taille de l'image (multi-stage bien fait ?
   cache layers ?). Un `.dockerignore` agressif + cache registry réduirait le
   volume poussé.
3. **Session buildkit qui expire** ("no active session") : possible bug
   buildx/buildkit version sur le runner — bump buildkit, ou
   `docker buildx create --driver-opt` avec keepalive.
4. **Retry automatique du push** : wrapper le step push dans nick-fields/retry
   (déjà utilisé ailleurs dans le workflow) avec 2-3 tentatives.

## Impact

Aberration d'infra (pas du code applicatif). A bloqué le déploiement de fixes
prod CRITIQUES ce soir (fix HubSyncDead qui débloque les broadcasts). À
stabiliser en priorité car ça gangrène toutes les promos.

## Workaround actuel

`gh run rerun <id> --failed` (le build repasse souvent au 2e essai).
