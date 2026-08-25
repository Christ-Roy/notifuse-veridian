# notifuse (PROD) — job Nomad Veridian, source de vérité GitOps de CE repo.
#
# Sert les vrais clients sur https://notifuse.app.veridian.site (Model B : ingress
# Traefik sur le bastion termine le TLS, cert Let's Encrypt DNS-01). Notifuse Go
# port 8081, health /healthz. DB postgres:17 co-localisée dans le group (127.0.0.1:5432,
# db notifuse_system), volume bind /opt/veridian-lab/notifuse sur OVH-PROD → job
# STATEFUL épinglé provider=ovh-prod, PAS de reschedule (le volume ne suit pas l'alloc).
# Secrets = Nomad Variable `nomad/jobs/notifuse` (JAMAIS en clair ici).
#
# ⚠️ PLACEMENT : migré du bastion (contabo) vers ovh-prod le 2026-07-15 par l'infra
#    (commit nomad-veridian 82a79dc, bastion saturé). La DB (données) vit sur
#    ovh-prod:/opt/veridian-lab/notifuse — NE PAS remettre provider=contabo (la copie
#    du bastion est FIGÉE au 07-15 → servir une DB périmée). memory_max=7000 = fusible 60% VM.
# ⚠️ DÉPLOIEMENT — canon SSH-bastion (cf veridian-prospection/deploy/README.md,
#    décision Robert 2026-07-11) : la CI (`veridian-ci.yml` job deploy-prod →
#    scripts/ci/nomad-ssh-deploy.sh) SSH vers le bastion, pré-pull l'image ghcr
#    (auth du nœud), scp CE fichier, puis `nomad job run -var image_tag=<TAG>`.
#    Le NOMAD_TOKEN ne quitte JAMAIS le bastion. Déployer TOUJOURS depuis CE HCL
#    (il déclare `variable image_tag`), jamais la copie ~/nomad-veridian/jobs/.
# ⚠️ DB mono-instance sans HA (reschedule OFF, bind ovh-prod) — migration Patroni HA =
#    chantier infra séparé (backlog nomad-veridian). Ne PAS reschedule ce job stateful.
variable "image_tag" {
  type        = string
  # Recale sur ce qui tourne reellement en prod : le defaut retardait de
  # deux versions majeures et un deploiement hors CI aurait retrograde Notifuse.
  default     = "v57.0-veridian.cc942a35"
  description = "Tag GHCR de l'image notifuse à déployer (passé par la CI via -var)."
}

job "notifuse" {
  datacenters = ["veridian-eu"]
  type        = "service"
  priority    = 80

# veridian-contract:start
  meta = {
    "veridian.contract.version"  = "1"
    "veridian.managed_by"        = "repo"
    "veridian.environment"       = "production"
    "veridian.tier"              = "saas-prod"
    "veridian.criticality"       = "B"
    "veridian.owner"             = "messaging"
    "veridian.objective"         = "availability-99.9"
    "veridian.rto_minutes"       = "5"
    "veridian.rpo_minutes"       = "15"
    "veridian.state"             = "local-state"
    "veridian.mobility"          = "local-gap"
    "veridian.preemptible"       = "false"
    "veridian.staging_job"       = "notifuse-staging"
    "veridian.promotion_policy"  = "staging-required"
  }
# veridian-contract:end

  group "stack" {
    count = 1

    # Épinglé à ovh-prod : db/data bind sur /opt/veridian-lab/notifuse d'ovh-prod (migré
    # du bastion le 2026-07-15). Le volume ne suit pas l'alloc → NE PAS changer de nœud.
    constraint {
      attribute = "${meta.provider}"
      value     = "ovh-prod"
    }

    update {
      max_parallel     = 1
      min_healthy_time = "15s"
      healthy_deadline = "5m"
      auto_revert      = true
    }

    restart {
      attempts = 10
      interval = "10m"
      delay    = "15s"
      mode     = "delay"
    }

    network {
      mode = "bridge"
      # L'ingress primaire tourne sur un autre nœud : annoncer le backend sur
      # le tailnet évite le hairpin vers l'IP publique ovh-prod (504 depuis
      # Traefik) tout en gardant l'application exposée uniquement via Traefik.
      port "http" {
        to           = 8081
        host_network = "tailscale"
      }
    }

    service {
      name     = "notifuse"
      provider = "nomad"
      port     = "http"
      tags = [
        "traefik.enable=true",
        "traefik.http.routers.notifuse.rule=Host(`notifuse-lab.veridian.site`)",
        "traefik.http.routers.notifuse.entrypoints=web",
        "traefik.http.routers.notifuse.middlewares=internal-only@nomad",
        "traefik.http.routers.notifusesec.rule=Host(`notifuse-lab.veridian.site`)",
        "traefik.http.routers.notifusesec.entrypoints=websecure",
        "traefik.http.routers.notifusesec.middlewares=internal-only@nomad",
        "traefik.http.routers.notifusesec.tls=true",
        "traefik.http.routers.notifuseprod.rule=Host(`notifuse.app.veridian.site`)",
        "traefik.http.routers.notifuseprod.entrypoints=websecure",
        "traefik.http.routers.notifuseprod.tls=true",
        "traefik.http.routers.notifuseprod.tls.certresolver=letsencrypt",
      ]
      check {
        type     = "http"
        path     = "/healthz"
        interval = "15s"
        timeout  = "5s"
      }
    }

    # ---- notifuse-db (postgres:17, frais, interne) ----
    task "notifuse-db" {
      driver = "docker"
      config {
        # Image officielle postgres:17-alpine + pgBackRest epingle. La BASE est
        # identique au bit pres : changer d'image de base changerait la
        # collation (musl/glibc) et fausserait silencieusement les index.
        image   = "ghcr.io/christ-roy/veridian-postgres-pgbackrest:17-alpine@sha256:2b6c8861f48116efaf58ea786e78590f42afd9b06073683bf44ea99681dfc653"
        command = "postgres"
        args = [
          "-c", "max_wal_size=1GB", "-c", "checkpoint_timeout=10min",
          # --- Archivage continu des WAL vers le depot pgBackRest ---
          # C'est CE reglage, et non la sauvegarde nocturne, qui borne la perte
          # de donnees : chaque segment de journal part vers R2 des qu'il est
          # clos. archive_timeout force cette cloture toutes les 5 minutes quand
          # il y a eu de l'ecriture, donc RPO = 5 min.
          # Modifier archive_mode exige un REDEMARRAGE de PostgreSQL (ce n'est
          # pas rechargeable a chaud) : c'est la seule interruption qu'impose la
          # mise en place.
          # pgBackRest ne joint le cluster QUE par socket Unix ; il n'a aucune
          # option de connexion TCP pour un cluster local. La tache annexe vit
          # dans un autre espace de montage et ne voit donc pas
          # /var/run/postgresql. On publie une seconde socket dans /alloc, le
          # repertoire que Nomad partage entre les taches d'un meme groupe.
          # L'ancienne reste en place : `docker exec ... psql` continue de marcher.
          "-c", "unix_socket_directories=/var/run/postgresql,/alloc",
          "-c", "archive_mode=on",
          "-c", "archive_command=pgbackrest --stanza=notifuse archive-push %p",
          "-c", "archive_timeout=300",
          "-c", "wal_level=replica",
        ]
        volumes = [
          "/opt/veridian-lab/notifuse/db:/var/lib/postgresql/data",
        ]
      }
      template {
        destination = "secrets/pg.env"
        env         = true
        data        = <<EOH
TZ=UTC
POSTGRES_USER=postgres
POSTGRES_DB=notifuse_system
{{ with nomadVar "nomad/jobs/notifuse" }}
POSTGRES_PASSWORD={{ .POSTGRES_PASSWORD }}
{{ end }}
# --- pgBackRest : configuration par variables d'environnement ---
# Aucun fichier de configuration : les identifiants R2 et la phrase de
# chiffrement ne sont jamais ecrits sur le disque de l'allocation. pgBackRest
# lit toute option sous la forme PGBACKREST_<OPTION>.
PGBACKREST_REPO1_TYPE=s3
PGBACKREST_REPO1_PATH=/pgbackrest/notifuse
PGBACKREST_REPO1_S3_REGION=auto
# path : R2 accepte les deux styles, celui-ci ne depend pas d'un DNS par bucket.
PGBACKREST_REPO1_S3_URI_STYLE=path
PGBACKREST_REPO1_CIPHER_TYPE=aes-256-cbc
PGBACKREST_COMPRESS_TYPE=zst
PGBACKREST_COMPRESS_LEVEL=6
PGBACKREST_REPO1_BUNDLE=y
PGBACKREST_REPO1_BLOCK=y
PGBACKREST_LOG_LEVEL_CONSOLE=info
PGBACKREST_LOG_LEVEL_FILE=off
PGBACKREST_PG1_PATH=/var/lib/postgresql/data
PGBACKREST_PG1_PORT=5432
PGBACKREST_PG1_USER=postgres
PGBACKREST_PG1_DATABASE=notifuse_system
{{ with nomadVar "nomad/jobs/notifuse" }}
PGBACKREST_REPO1_S3_BUCKET={{ .R2_BUCKET }}
PGBACKREST_REPO1_S3_ENDPOINT={{ .R2_ENDPOINT }}
PGBACKREST_REPO1_S3_KEY={{ .R2_ACCESS_KEY_ID }}
PGBACKREST_REPO1_S3_KEY_SECRET={{ .R2_SECRET_ACCESS_KEY }}
# ATTENTION : PERDRE CETTE PHRASE = PERDRE TOUTES LES SAUVEGARDES. Copie de
# secours dans ~/credentials/.all-creds.env (PGBACKREST_CIPHER_NOTIFUSE).
PGBACKREST_REPO1_CIPHER_PASS={{ .PGBACKREST_CIPHER_PASS }}
{{ end }}
EOH
      }
      resources {
        cpu        = 300
        memory     = 384
        memory_max = 7000
      }
    }

    # ---- pgBackRest : sauvegarde continue vers R2 ----
    # Tache annexe du MEME groupe, donc : meme espace reseau (elle joint
    # PostgreSQL par la socket publiee dans /alloc, authentification `trust`
    # locale, aucun mot de passe a promener) et meme bind mount de PGDATA (elle
    # lit les pages directement). Elle SUIT l'allocation : si Nomad replace le
    # groupe, la sauvegarde repart sans qu'on touche a un script.
    task "pgbackrest" {
      driver = "docker"
      config {
        image      = "ghcr.io/christ-roy/veridian-postgres-pgbackrest:17-alpine@sha256:2b6c8861f48116efaf58ea786e78590f42afd9b06073683bf44ea99681dfc653"
        entrypoint = ["/usr/local/bin/pgbackrest-scheduler"]
        command    = ""
        volumes = [
          "/opt/veridian-lab/notifuse/db:/var/lib/postgresql/data",
        ]
      }
      user = "postgres"

      template {
        destination = "secrets/pgbackrest.env"
        env         = true
        data        = <<EOH
TZ=UTC
PGBR_STANZA=notifuse
# Socket partagee avec la tache postgres via le repertoire d'allocation.
PGBACKREST_PG1_SOCKET_PATH=/alloc
# Complete le dimanche, differentielle les autres jours, incrementale toutes les
# 6 h. 30 : creneau propre a cette stanza pour ne pas taper R2 en meme
# temps que les autres bases du parc.
PGBR_FULL_DOW=0
PGBR_DAILY_HOUR=3
PGBR_DAILY_MINUTE=30
PGBR_INCR_EVERY_H=6
# Base de PRODUCTION cliente : 8 semaines de completes conservees. Les WAL
# retenus couvrent la meme profondeur, donc on peut viser n'importe quelle
# seconde des deux derniers mois.
PGBACKREST_REPO1_RETENTION_FULL=8
PGBACKREST_REPO1_RETENTION_DIFF=7
PGBACKREST_PROCESS_MAX=2
PGBACKREST_START_FAST=y
# --- pgBackRest : configuration par variables d'environnement ---
# Aucun fichier de configuration : les identifiants R2 et la phrase de
# chiffrement ne sont jamais ecrits sur le disque de l'allocation. pgBackRest
# lit toute option sous la forme PGBACKREST_<OPTION>.
PGBACKREST_REPO1_TYPE=s3
PGBACKREST_REPO1_PATH=/pgbackrest/notifuse
PGBACKREST_REPO1_S3_REGION=auto
# path : R2 accepte les deux styles, celui-ci ne depend pas d'un DNS par bucket.
PGBACKREST_REPO1_S3_URI_STYLE=path
PGBACKREST_REPO1_CIPHER_TYPE=aes-256-cbc
PGBACKREST_COMPRESS_TYPE=zst
PGBACKREST_COMPRESS_LEVEL=6
PGBACKREST_REPO1_BUNDLE=y
PGBACKREST_REPO1_BLOCK=y
PGBACKREST_LOG_LEVEL_CONSOLE=info
PGBACKREST_LOG_LEVEL_FILE=off
PGBACKREST_PG1_PATH=/var/lib/postgresql/data
PGBACKREST_PG1_PORT=5432
PGBACKREST_PG1_USER=postgres
PGBACKREST_PG1_DATABASE=notifuse_system
{{ with nomadVar "nomad/jobs/notifuse" }}
PGBACKREST_REPO1_S3_BUCKET={{ .R2_BUCKET }}
PGBACKREST_REPO1_S3_ENDPOINT={{ .R2_ENDPOINT }}
PGBACKREST_REPO1_S3_KEY={{ .R2_ACCESS_KEY_ID }}
PGBACKREST_REPO1_S3_KEY_SECRET={{ .R2_SECRET_ACCESS_KEY }}
# ATTENTION : PERDRE CETTE PHRASE = PERDRE TOUTES LES SAUVEGARDES. Copie de
# secours dans ~/credentials/.all-creds.env (PGBACKREST_CIPHER_NOTIFUSE).
PGBACKREST_REPO1_CIPHER_PASS={{ .PGBACKREST_CIPHER_PASS }}
{{ end }}
EOH
      }

      resources {
        cpu        = 100
        memory     = 64
        memory_max = 512
      }
    }

    # ---- notifuse (Go, port 8081) ----
    task "notifuse" {
      driver         = "docker"
      shutdown_delay = "10s"
      kill_timeout   = "30s"
      service {
        name     = "notifuse-selfheal"
        provider = "nomad"
        port     = "http"
        tags     = ["traefik.enable=false"]
        check {
          type     = "http"
          path     = "/healthz"
          interval = "15s"
          timeout  = "5s"
          check_restart {
            limit           = 4
            grace           = "90s"
            ignore_warnings = false
          }
        }
      }
      config {
        image = "ghcr.io/christ-roy/notifuse-veridian:${var.image_tag}"
        ports = ["http"]
        volumes = [
          "/opt/veridian-lab/notifuse/data:/app/data",
        ]
      }
      template {
        destination = "secrets/app.env"
        env         = true
        data        = <<EOH
SERVER_PORT=8081
SERVER_HOST=0.0.0.0
ENVIRONMENT=production
DEPLOY_ENV=prod
DB_HOST=127.0.0.1
DB_PORT=5432
DB_USER=postgres
DB_PREFIX=notifuse
DB_NAME=notifuse_system
DB_SSLMODE=disable
TASK_SCHEDULER_ENABLED=true
TASK_SCHEDULER_INTERVAL=20s
TASK_SCHEDULER_MAX_TASKS=100
TELEMETRY=false
CHECK_FOR_UPDATES=false
API_ENDPOINT=https://notifuse.app.veridian.site
INTERNAL_API_ENDPOINT=http://localhost:8081
VERIDIAN_DEFAULT_PLAN=free

{{ with nomadVar "nomad/jobs/notifuse" }}
DB_PASSWORD={{ .POSTGRES_PASSWORD }}
SECRET_KEY={{ .NOTIFUSE_SECRET_KEY }}
ROOT_EMAIL={{ .NOTIFUSE_ROOT_EMAIL }}
HUB_API_SECRET={{ .NOTIFUSE_HUB_API_SECRET }}
HUB_WEBHOOK_URL={{ .NOTIFUSE_HUB_WEBHOOK_URL }}
HUB_WEBHOOK_SECRET={{ .NOTIFUSE_HUB_WEBHOOK_SECRET }}
SMTP_HOST={{ .SMTP_HOST }}
SMTP_PORT={{ .SMTP_PORT }}
SMTP_USERNAME={{ .SMTP_USER }}
SMTP_PASSWORD={{ .SMTP_PASS }}
SMTP_FROM_EMAIL={{ .SMTP_ADMIN_EMAIL }}
SMTP_FROM_NAME={{ .SMTP_SENDER_NAME }}
{{ end }}
EOH
      }
      resources {
        cpu        = 400
        memory     = 128
        memory_max = 7000
      }
    }
  }
}
