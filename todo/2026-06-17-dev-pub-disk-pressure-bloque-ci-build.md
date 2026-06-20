# dev-pub disque/RAM saturés → build CI Notifuse self-hosted échoue (push GHCR timeout)

> **Sévérité** : 🔴 P0 (BLOQUANT DUR au 2026-06-20 : aucun build Notifuse ne passe,
> ni staging ni promo prod — timeout push GHCR `context deadline exceeded`)
> **Owner** : agent notifuse-veridian (scope large — le dev server fait partie de
> notre périmètre, pas de tiers à qui router)
> **Créé** : 2026-06-17 (trouvé pendant la batterie garde-fous)
> **Escaladé** : 2026-06-20 (RAM dev-pub à 165Mi libre → build timeout systématique)

## Symptôme

Le job CI `Build & push image (self-hosted)` (runner self-hosted sur dev-pub) a
échoué 2× de suite sur le push d'image, log coupé juste après "Compute image tags"
(le `docker build`/buildx meurt sans message clair). Cause racine : **disque
dev-pub à 100 % / 133 Mo libres** au moment du build. Le buildx n'a plus de place
pour les couches → mort du runner mid-build.

## Ce qui a débloqué (fait en session, réversible)

- `docker image prune -af` + `docker builder prune -af` → ~10 Go récupérés au
  total sur 2 passes → disque revenu à ~93 % (5-6 Go libres) → le rerun du build
  a réussi + deploy staging OK.
- `journalctl --vacuum-size=50M` + `apt-get clean` (gain marginal).

## Demande (durable)

- **Cron de GC docker** sur dev-pub : `docker image prune -af --filter until=48h`
  + `docker builder prune -af` hebdo (ou quotidien), via `~/all-cron/`.
- **Alerte disque** dev-pub > 85 % (Telegram) — le build CI échoue silencieusement
  sinon, on ne le voit qu'en lisant les logs.
- Investiguer les gros postes hors docker : `/home/ubuntu/odh-scrape` (3.8 G),
  `actions-runner*` (5.8 G cumulés), divers `*-ui-dev` (rsync hot-reload UI).
- Lié : les 716 bases `notifuse_ws_*` orphelines (ticket Notifuse
  `2026-06-17-orphan-workspaces-staging-db-starvation.md`) occupent aussi du
  disque sur le volume `notifuse-staging-db`.

## Impact si non traité

Déploiements staging Notifuse (et autres apps buildant sur dev-pub) échouent au
hasard quand le disque sature, sans message clair → perte de temps de diagnostic.

## Action 2026-06-20 — débloqué (par agent notifuse, scope large)

Cause racine RÉELLE découverte : ce n'était pas que le disque/build. Le pool DB
staging saturait (**348/350 connexions**) à cause des **bases workspace orphelines
qui gardent des connexions idle** + le bug wipe-recrée qui les régénère. Symptômes
en cascade : build GHCR timeout (RAM 165Mi) ET E2E cassés (`connection limit
reached: 348/350, cannot create pool`).

**Fait moi-même (pas de tiers à qui router)** :
- Terminé les connexions idle des bases orphelines (`pg_terminate_backend` sur
  `state='idle'`) → pool 354→24.
- GC en masse via l'endpoint `gc-orphan-workspace-dbs` : ~540 bases orphelines
  droppées (630→137).
- Supprimé 5 builders buildx zombies (`docker buildx rm` + prune).
- **Cron de cleanup durable posé** : `/home/ubuntu/notifuse-staging-gc.sh`
  (crontab dev-pub `*/30 * * * *`) → termine les connexions idle + GC orphelines.
  Secret HMAC en `/home/ubuntu/.notifuse-gc-secret` (0600). Log
  `/home/ubuntu/notifuse-staging-gc.log`. Maintient le pool sain automatiquement.

**Reste (vrai fond)** : le bug wipe-recrée (fix `514e6c12` record-first, sur
staging) juge la régénération (94 records vs ex-630 bases). À PROMOUVOIR EN PROD
avec preuve on-premise. Le cron est le garde-fou en attendant que la cause soit
100% éteinte. Piste durable : réduire l'idle-timeout des connexions worker
(config pool) pour que le worker ne garde pas une connexion ouverte par base.
