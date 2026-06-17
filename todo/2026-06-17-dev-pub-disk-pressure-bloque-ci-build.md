# dev-pub disque à 92-98 % → build CI Notifuse self-hosted échoue (silencieux)

> **Sévérité** : 🟡 P1 (bloque les déploiements staging Notifuse de façon intermittente)
> **Owner** : à router vers agent veridian-infra (dev server)
> **Créé** : 2026-06-17 (trouvé pendant la batterie garde-fous)

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
