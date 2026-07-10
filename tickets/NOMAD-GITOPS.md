# TICKET — Passer notifuse en GitOps Nomad propre

> Ticket standard déposé par l'infra (repo `nomad-veridian`). À traiter par l'agent dédié de
> CE repo quand il se réveille. Objectif : niveau de propreté "boîte établie" — le déploiement
> de notifuse vit DANS ce repo (gitops), tourne en **job Nomad** (plus de compose/Dokploy), et
> respecte les règles strictes de l'infra. **Le terrain a raison** : vérifie l'état réel avant d'agir.

> ⚠️ **Spécifique Notifuse (multi-DB par workspace)** : chaque workspace a sa propre base Postgres. La bascule HA Patroni (§4) doit couvrir l'ENSEMBLE des bases workspace, pas seulement la base système. Recense les DB avant de planifier la fenêtre.

## Contexte (déjà fait par l'infra)
- Migration Dokploy → **cluster Nomad 3 nœuds** TERMINÉE. notifuse tourne DÉJÀ en job Nomad
  (`~/nomad-veridian/jobs/notifuse.nomad.hcl`, déployé, sain). Dokploy est décommissionné.
- Contrôle-plane sur le bastion Contabo. Pilotage : `source ~/credentials/nomad-bastion.env;
  export NOMAD_ADDR NOMAD_TOKEN` puis `nomad ...` / wrapper `nomad-v` / `nomad-v state` (dashboard).
- Conventions + invariants : `~/nomad-veridian/CLAUDE.md`. Templates : `~/nomad-veridian/jobs/templates/`.

## Ce qu'on veut (Definition of Done)
1. **Le job Nomad de notifuse vit dans CE repo** (ex `deploy/notifuse.nomad.hcl`), source de vérité gitops —
   pas seulement dans `nomad-veridian`. Aligne-le sur le job réellement déployé (copie-le, adapte).
2. **CI de déploiement** (GitHub Actions) : sur push de la branche de prod (après build+tests+scan
   sécu existants), la CI fait `nomad job run` du job (via NOMAD_ADDR/NOMAD_TOKEN en secrets repo,
   Tailscale runner ou token). Comme l'ancien gitops Dokploy, mais vers Nomad. `nomad job plan` avant.
3. **Job conforme aux règles infra** (NON négociables) :
   - **Réseau privé par défaut** : tout port en `host_network = "tailscale"` (bind Tailscale-only).
     **JAMAIS `0.0.0.0` / port public arbitraire** — un hook harness bloque déjà `docker -p 0.0.0.0`.
     Seul l'ingress (Traefik, 80/443) est public ; notifuse est joignable UNIQUEMENT via l'ingress.
   - Exposition web via **tags Traefik** (routers web+websecure, `tls.certresolver=letsencrypt`) :
     public client → `crowdsec@file`+`securityheaders@file` (hérités) ; interne/staging → middleware
     `internal-only@nomad` (ipAllowList 100.64/10 → 403 hors tailnet).
   - **Secrets** : Nomad Variable `nomad/jobs/notifuse` + `template{env=true}`. JAMAIS en clair dans le HCL/repo.
   - Tout job déclare `resources` (jamais unbounded), `restart`+`reschedule`, un `check`, une `priority`,
     un placement (`constraint ${meta.provider}` + node pool). Cf `jobs/templates/README.md`.
4. **DB en HA (si notifuse a une base Postgres)** : cible = cluster **Patroni** (failover auto via Consul),
   plus de postgres co-localisé mono-instance. Gabarit prouvé bout-en-bout :
   `~/nomad-veridian/jobs/hub-staging-db.nomad.hcl` (cluster) + `hub-staging.nomad.hcl` (app + sidecar
   HAProxy qui suit le leader, DATABASE_URL `@127.0.0.1:5432` inchangé). Pièges dans la mémoire
   `patroni-failover-proto` (dump via `docker exec`+`docker cp` PAS `nomad alloc exec` qui tronque ;
   stripper `\restrict` ; prouver le failover via psql dans le netns, pas `/api/health`).
   ⚠️ Migration d'une DB PROD = **downtime court + backup all-cron FRAIS vérifié avant + rollback prêt**.
   Coordonne avec l'infra / Robert pour la fenêtre si notifuse est critique/client-payant.

## Comment procéder
1. Lis `~/nomad-veridian/CLAUDE.md` + `jobs/templates/README.md` + le job actuel `jobs/notifuse.nomad.hcl`.
2. Rapatrie le job dans ce repo (`deploy/`), vérifie conformité (règles ci-dessus), `nomad job validate`.
3. Câble la CI de déploiement (secrets repo, plan→run).
4. Si DB → planifie la bascule Patroni (staging d'abord si tu en as un, prod sur fenêtre).
5. Teste, documente, ferme ce ticket.

## Garde-fous
- Ne casse pas la prod : `nomad job plan` avant tout `run`, vérifie `nomad-v state` après.
- Rien en clair (secrets). Rien de public arbitraire (Tailscale only). `git` : PR plutôt que push direct sur main si CI de déploiement.
- En cas de doute sur un arbitrage prod / dépense / fenêtre → remonte, ne devine pas.
