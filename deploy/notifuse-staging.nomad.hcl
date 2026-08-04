# notifuse-staging — job Nomad Veridian, source de vérité GitOps de CE repo.
#
# Staging privé sur https://notifuse.staging.veridian.site, placé sur ovh-dev (== dev-pub),
# servi via l'ingress bastion (cross-node Tailscale). PRIVÉ : port host_network="tailscale"
# (bind IP tailnet uniquement) + middleware internal-only@nomad (ipAllowList 100.64/10) →
# public = 403. Notifuse Go port 8081, health /healthz. DB postgres:17 co-localisée
# (127.0.0.1:5432), volume bind /opt/veridian-staging/notifuse/db → STATEFUL épinglé
# provider=ovh-dev, PAS de reschedule. Secrets = Nomad Variable `nomad/jobs/notifuse-staging`.
#
# ⚠️ DÉPLOIEMENT — canon SSH-bastion (cf veridian-prospection/deploy/README.md) : la CI
#    (job deploy-staging → scripts/ci/nomad-ssh-deploy.sh) SSH vers le bastion, pré-pull
#    l'image sur ovh-dev (`ssh -n dev-pub docker pull`), scp CE fichier, puis
#    `nomad job run -var image_tag=<TAG>`. Déployer TOUJOURS depuis CE HCL (variable
#    image_tag), jamais la copie ~/nomad-veridian/jobs/.
variable "image_tag" {
  type        = string
  default     = "v54.0-veridian.6a397c98"
  description = "Tag GHCR de l'image notifuse à déployer (passé par la CI via -var)."
}

job "notifuse-staging" {
  datacenters = ["veridian-eu"]
  type        = "service"
  priority    = 50

  group "stack" {
    count = 1

    # Le routeur permanent Sablier de l'ingress pointe sur le port fixe 19095.
    # Cette meta autorise Sablier à endormir/réveiller le job staging.
    meta = { "sablier.enable" = "true" }

    # Épinglé à ovh-dev : la DB bind sur /opt/veridian-staging du nœud ovh-dev uniquement.
    constraint {
      attribute = "${meta.provider}"
      value     = "ovh-dev"
    }

    # Deadlines étendues (1er pull lent) + auto_revert — cf piège prospection 2026-07-11.
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
      # host_network tailscale : le port CNI bind sur l'IP Tailscale du nœud uniquement
      # → app injoignable en public (bypass ipAllowList impossible), Traefik route via Tailscale.
      port "http" {
        static       = 19095
        to           = 8081
        host_network = "tailscale"
      }
    }

    service {
      name     = "notifuse-staging"
      provider = "nomad"
      port     = "http"
      # Le routing vit dans ingress.nomad.hcl avec un service @file permanent.
      # Un second routeur @nomad contournerait Sablier et recréerait du drift.
      tags = ["traefik.enable=false"]
      check {
        type     = "http"
        path     = "/healthz"
        interval = "5s"
        timeout  = "5s"
      }
    }

    # ---- notifuse-staging-db (postgres:17, données staging migrées) ----
    task "db" {
      driver = "docker"
      config {
        image = "postgres:17-alpine"
        volumes = [
          "/opt/veridian-staging/notifuse/db:/var/lib/postgresql/data",
        ]
      }
      template {
        destination = "secrets/pg.env"
        env         = true
        data        = <<EOH
TZ=UTC
POSTGRES_USER=postgres
POSTGRES_DB=notifuse_system
{{ with nomadVar "nomad/jobs/notifuse-staging" }}
POSTGRES_PASSWORD={{ .POSTGRES_PASSWORD }}
{{ end }}
EOH
      }
      resources {
        cpu        = 250
        # Pic 7 j observé : 239 MiB. La réserve garde 34 % de marge et le
        # fusible permet toujours restore/maintenance sans menacer la VM.
        memory     = 320
        memory_max = 2048
      }
    }

    # ---- notifuse (Go, port 8081) ----
    task "notifuse" {
      driver = "docker"
      config {
        image = "ghcr.io/christ-roy/notifuse-veridian:${var.image_tag}"
        ports = ["http"]
      }
      template {
        destination = "secrets/app.env"
        env         = true
        data        = <<EOH
SERVER_PORT=8081
SERVER_HOST=0.0.0.0
ENVIRONMENT=staging
DEPLOY_ENV=staging
DB_HOST=127.0.0.1
DB_PORT=5432
DB_USER=postgres
DB_PREFIX=notifuse
DB_NAME=notifuse_system
DB_SSLMODE=disable
DB_MAX_CONNECTIONS=600
DB_MAX_CONNECTIONS_PER_DB=3
TASK_SCHEDULER_ENABLED=true
TASK_SCHEDULER_INTERVAL=20s
TASK_SCHEDULER_MAX_TASKS=100
TELEMETRY=false
CHECK_FOR_UPDATES=false
API_ENDPOINT=https://notifuse.staging.veridian.site
INTERNAL_API_ENDPOINT=http://localhost:8081
VERIDIAN_DEFAULT_PLAN=free

{{ with nomadVar "nomad/jobs/notifuse-staging" }}
DB_PASSWORD={{ .POSTGRES_PASSWORD }}
SECRET_KEY={{ .NOTIFUSE_SECRET_KEY }}
ROOT_EMAIL={{ .NOTIFUSE_ROOT_EMAIL }}
HUB_API_SECRET={{ .HUB_API_SECRET }}
HUB_WEBHOOK_URL={{ .HUB_WEBHOOK_URL }}
HUB_WEBHOOK_SECRET={{ .HUB_WEBHOOK_SECRET }}
SMTP_HOST={{ .SMTP_HOST }}
SMTP_PORT={{ .SMTP_PORT }}
SMTP_USERNAME={{ .SMTP_USERNAME }}
SMTP_PASSWORD={{ .SMTP_PASSWORD }}
SMTP_FROM_EMAIL={{ .SMTP_FROM_EMAIL }}
SMTP_FROM_NAME={{ .SMTP_FROM_NAME }}
{{ end }}
EOH
      }
      resources {
        # Pics 7 j observés : 252 MHz / 66 MiB.
        cpu        = 300
        memory     = 96
        memory_max = 512
      }
    }

    # ---- smtp-sink (cul-de-sac E2E cold, aucun port hote) ----
    # Partage le namespace reseau du groupe avec Notifuse : les integrations de
    # test ciblent exclusivement 127.0.0.1:1025. aiosmtpd Debugging imprime le
    # message puis le jette, sans resolver ni contacter le MX du destinataire.
    task "smtp-sink" {
      driver = "docker"
      config {
        image   = "python:3.12-alpine"
        command = "/bin/sh"
        args = [
          "-c",
          "pip install --no-cache-dir aiosmtpd==1.4.6 >/dev/null && exec python /local/sink.py",
        ]
      }
      template {
        destination = "local/sink.py"
        data        = <<EOH
import sys
from threading import Event
from aiosmtpd.controller import Controller

class Sink:
    async def handle_DATA(self, server, session, envelope):
        print("---------- MESSAGE FOLLOWS ----------", flush=True)
        print(f"sender: {envelope.mail_from}", flush=True)
        for recipient in envelope.rcpt_tos:
            print(f"recip: {recipient}", flush=True)
            print(f"RCPT TO:<{recipient}>", flush=True)
        sys.stdout.buffer.write(envelope.original_content)
        sys.stdout.buffer.write(b"\n------------ END MESSAGE ------------\n")
        sys.stdout.buffer.flush()
        return "250 Message accepted for delivery"

controller = Controller(Sink(), hostname="0.0.0.0", port=1025)
controller.start()
try:
    Event().wait()
finally:
    controller.stop()
EOH
      }
      resources {
        cpu        = 25
        memory     = 24
        memory_max = 64
      }
    }
  }
}
