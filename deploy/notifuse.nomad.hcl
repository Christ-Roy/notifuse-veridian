# notifuse (PROD) — job Nomad Veridian, source de vérité GitOps de CE repo.
#
# Sert les vrais clients sur https://notifuse.app.veridian.site (Model B : ingress
# Traefik sur le bastion termine le TLS, cert Let's Encrypt DNS-01). Notifuse Go
# port 8081, health /healthz. DB postgres:17 co-localisée dans le group (127.0.0.1:5432,
# db notifuse_system), volume bind /opt/veridian-lab/notifuse sur le bastion → job
# STATEFUL épinglé provider=contabo, PAS de reschedule (le volume ne suit pas l'alloc).
# Secrets = Nomad Variable `nomad/jobs/notifuse` (JAMAIS en clair ici).
#
# ⚠️ DÉPLOIEMENT — canon SSH-bastion (cf veridian-prospection/deploy/README.md,
#    décision Robert 2026-07-11) : la CI (`veridian-ci.yml` job deploy-prod →
#    scripts/ci/nomad-ssh-deploy.sh) SSH vers le bastion, pré-pull l'image ghcr
#    (auth du nœud), scp CE fichier, puis `nomad job run -var image_tag=<TAG>`.
#    Le NOMAD_TOKEN ne quitte JAMAIS le bastion. Déployer TOUJOURS depuis CE HCL
#    (il déclare `variable image_tag`), jamais la copie ~/nomad-veridian/jobs/.
# ⚠️ DB mono-instance sans HA (reschedule OFF, bind bastion) — migration Patroni HA =
#    chantier infra séparé (backlog nomad-veridian). Ne PAS reschedule ce job stateful.
variable "image_tag" {
  type        = string
  default     = "v54.0-veridian.7ff43498"
  description = "Tag GHCR de l'image notifuse à déployer (passé par la CI via -var)."
}

job "notifuse" {
  datacenters = ["veridian-eu"]
  type        = "service"

  group "stack" {
    count = 1

    # Épinglé au bastion : db/data bind sur /opt/veridian-lab/notifuse du bastion uniquement.
    constraint {
      attribute = "${meta.provider}"
      value     = "contabo"
    }

    # Deadlines étendues : le 1er pull d'image sur un nœud sans cache dépasse les
    # 5min par défaut → deployment marqué failed prématurément (piège prospection
    # 2026-07-11). auto_revert = filet de sécurité (retour à la version saine).
    update {
      healthy_deadline  = "15m"
      progress_deadline = "20m"
      auto_revert       = true
    }

    restart {
      attempts = 10
      interval = "10m"
      delay    = "15s"
      mode     = "delay"
    }

    network {
      mode = "bridge"
      port "http" { to = 8081 }
    }

    service {
      name     = "notifuse"
      provider = "nomad"
      port     = "http"
      tags = [
        "traefik.enable=true",
        "traefik.http.routers.notifuse.rule=Host(`notifuse-lab.veridian.site`)",
        "traefik.http.routers.notifuse.entrypoints=web",
        "traefik.http.routers.notifusesec.rule=Host(`notifuse-lab.veridian.site`)",
        "traefik.http.routers.notifusesec.entrypoints=websecure",
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
        image   = "postgres:17-alpine"
        command = "postgres"
        args    = ["-c", "max_wal_size=1GB", "-c", "checkpoint_timeout=10min"]
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
EOH
      }
      resources {
        cpu    = 300
        memory = 512
      }
    }

    # ---- notifuse (Go, port 8081) ----
    task "notifuse" {
      driver = "docker"
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
        cpu    = 400
        memory = 384
      }
    }
  }
}
