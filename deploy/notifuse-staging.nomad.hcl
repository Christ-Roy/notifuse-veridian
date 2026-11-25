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
#    (job deploy-staging → scripts/ci/nomad-ssh-deploy.sh) pousse CE fichier au bastion
#    via le verbe `put-job staging`, puis `deploy staging <TAG>`. Le pré-pull authentifié
#    sur ovh-dev et le `nomad job run -var image_tag=<TAG>` tournent côté bastion, sous
#    commande forcée. Déployer TOUJOURS depuis CE HCL (variable image_tag), jamais la
#    copie ~/nomad-veridian/jobs/.
variable "image_tag" {
  type        = string
  default     = "v57.0-veridian.cc942a35"
  description = "Tag GHCR de l'image notifuse à déployer (passé par la CI via -var)."
}

job "notifuse-staging" {
  datacenters = ["veridian-eu"]
  type        = "service"
  priority    = 30 # bande 30 : staging, jetable sous contention. cf plan/POLICY-PRIORITES.md (nomad-veridian).
  # 50 = valeur du defaut Nomad, donc le niveau d'un client payant : a 50 ce banc
  # d'essai n'etait jamais preempte par la prod (Nomad ne preempte qu'a 10 points
  # d'ecart). Corrige le 2026-09-07 (ROB-60).

# veridian-contract:start
# veridian.contract.version=1
# veridian.managed_by=repo
# veridian.environment=staging
# veridian.tier=saas-staging
# veridian.criticality=C
# veridian.owner=messaging
# veridian.objective=internal-99.0
# veridian.rto_minutes=30
# veridian.rpo_minutes=1440
# veridian.state=local-state
# veridian.mobility=sablier
# veridian.preemptible=true
# veridian.production_job=notifuse
# veridian.promotion_policy=non-production
# veridian-contract:end

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
      delay    = "2s"
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
      # Relais SMTP de soumission (fiche 71) : port 587 STATIQUE, tailnet seul (relais-smtp.veridian.site).
      port "smtp" {
        static       = 587
        to           = 587
        host_network = "tailscale"
      }
      # IMAP des boîtes louées (fiche 84) : port DYNAMIQUE, tailnet seul. Aucun port public.
      port "imap" {
        to           = 1993
        host_network = "tailscale"
      }
      # Récepteur de courrier des boîtes louées (fiche 84) : port DYNAMIQUE, tailnet seul. Rien de public.
      port "mx" {
        to           = 2525
        host_network = "tailscale"
      }
    }

    # L'IMAP des boîtes louées au catalogue : l'entrée publique le suivra quand le conteneur bouge (fiche 84).
    service {
      name     = "notifuse-staging-imap"
      provider = "nomad"
      port     = "imap"
      tags     = ["traefik.enable=false"]
    }

    service {
      name     = "notifuse-staging-mx"
      provider = "nomad"
      port     = "mx"
      tags     = ["traefik.enable=false"]
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
    task "notifuse-staging-db" {
      driver = "docker"
      config {
        # Durcissement Unix : empeche un processus non privilegie d'elever ses
        # droits via un binaire setuid. C'est le maillon entre « shell dans le
        # conteneur » et « root sur l'hote ». N'affecte PAS un processus qui
        # ABANDONNE ses droits au demarrage, seulement celui qui en gagne.
        security_opt = ["no-new-privileges:true"]

        # Identification lisible du conteneur (2026-09-07). Nomad ne pose que
        # `com.hashicorp.nomad.alloc_id` : rien ne disait a quelle application
        # appartenait un conteneur. Les quatre premieres valeurs sont
        # interpolees par Nomad a l'execution, les deux dernieres sont des
        # proprietes du fichier (dossier jobs/<tier>/ et table du script
        # scripts/poser-labels-conteneurs.py).
        labels = {
          "site.veridian.job"   = "${NOMAD_JOB_NAME}"
          "site.veridian.group" = "${NOMAD_GROUP_NAME}"
          "site.veridian.task"  = "${NOMAD_TASK_NAME}"
          "site.veridian.node"  = "${node.unique.name}"
          "site.veridian.tier"  = "saas-staging"
          "site.veridian.app"   = "notifuse"
        }
        image = "postgres:17-alpine"
        # Le plafond de l'appli (DB_MAX_CONNECTIONS) doit tenir dans celui de PostgreSQL (defaut 100 sinon).
        args  = ["-c", "max_connections=300"]
        volumes = [
          "/opt/veridian-staging/notifuse/db:/var/lib/postgresql/data",
        ]
      }
      template {
        destination = "secrets/pg.env"
        perms       = "600"   # I07 : sans ca Nomad ecrit en 644, lisible par tout compte local sur le noeud (mesure du 2026-10-07)
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
    task "notifuse-staging" {
      driver = "docker"
      config {
        # Durcissement Unix : empeche un processus non privilegie d'elever ses
        # droits via un binaire setuid. C'est le maillon entre « shell dans le
        # conteneur » et « root sur l'hote ». N'affecte PAS un processus qui
        # ABANDONNE ses droits au demarrage, seulement celui qui en gagne.
        security_opt = ["no-new-privileges:true"]

        # Identification lisible du conteneur (2026-09-07). Nomad ne pose que
        # `com.hashicorp.nomad.alloc_id` : rien ne disait a quelle application
        # appartenait un conteneur. Les quatre premieres valeurs sont
        # interpolees par Nomad a l'execution, les deux dernieres sont des
        # proprietes du fichier (dossier jobs/<tier>/ et table du script
        # scripts/poser-labels-conteneurs.py).
        labels = {
          "site.veridian.job"   = "${NOMAD_JOB_NAME}"
          "site.veridian.group" = "${NOMAD_GROUP_NAME}"
          "site.veridian.task"  = "${NOMAD_TASK_NAME}"
          "site.veridian.node"  = "${node.unique.name}"
          "site.veridian.tier"  = "saas-staging"
          "site.veridian.app"   = "notifuse"
        }
        image = "ghcr.io/christ-roy/notifuse-veridian:${var.image_tag}"
        ports = ["http", "smtp", "imap", "mx"]
      }
      # Garde reseau (fiche 64) : le smtp-sink E2E est en loopback dans l'allocation. Exception EXACTE 127.0.0.1:1025,
      # staging seulement (jamais dans notifuse.nomad.hcl), requise par scripts/e2e/cold-garde-fous.py.
      template {
        destination = "secrets/app.env"
        perms       = "600"   # I07 : sans ca Nomad ecrit en 644, lisible par tout compte local sur le noeud (mesure du 2026-10-07)
        env         = true
        data        = <<EOH
SERVER_PORT=8081
# Relais SMTP (fiche 71). Certificat GÉRÉ par l'application (Let's Encrypt, DNS Cloudflare) : pas de
# certificat copié ici. L'interrupteur du relais et le contact ACME vivent dans les réglages système
# (VERIDIAN_SMTP_RELAY_ENABLED n'est plus posé : le réglage s'applique). VERIDIAN_RELAY_CERT_MANAGED=true
# est l'amorçage qui permet au bridge de démarrer en starttls avant l'émission (aucun AUTH annoncé sans TLS).
SMTP_BRIDGE_ENABLED=true
SMTP_BRIDGE_PORT=587
SMTP_BRIDGE_DOMAIN=relais-smtp.veridian.site
# Boîtes louées (fiche 84) : IMAP en TLS avec le certificat du relais, tailnet seul.
VERIDIAN_IMAP_ENABLED=true
# Même garde qu'en prod (fiche 91) ; date plus tardive, pour éprouver les deux sens sur des comptes de test.
VERIDIAN_ACCOUNT_GATE_SINCE=2026-11-25T16:15:00Z
VERIDIAN_IMAP_PORT=1993
# Récepteur : adresses de profil de ces domaines seulement. Aucun MX ne pointe ici sans l'oui de Robert.
VERIDIAN_MX_DOMAINS=boites-test.veridian.site
VERIDIAN_MX_HOST=mx-staging.veridian.site
SMTP_BRIDGE_TLS=starttls
VERIDIAN_RELAY_CERT_MANAGED=true
SERVER_HOST=0.0.0.0
ENVIRONMENT=staging
DEPLOY_ENV=staging
DB_HOST=127.0.0.1
DB_PORT=5432
DB_USER=postgres
DB_PREFIX=notifuse
DB_NAME=notifuse_system
DB_SSLMODE=disable
DB_MAX_CONNECTIONS=250
DB_MAX_CONNECTIONS_PER_DB=3
TASK_SCHEDULER_ENABLED=true
TASK_SCHEDULER_INTERVAL=20s
TASK_SCHEDULER_MAX_TASKS=100
TELEMETRY=false
CHECK_FOR_UPDATES=false
API_ENDPOINT=https://notifuse.staging.veridian.site
INTERNAL_API_ENDPOINT=http://localhost:8081
VERIDIAN_DEFAULT_PLAN=free
# Provision sans personne du Hub refusée (400), staging seulement tant que le Hub n'a pas donné son accord pour la prod (fiche 89).
VERIDIAN_PROVISION_REQUIRE_HUB_PERSON=1
# 1025 : puits SMTP. 1026 : port fermé, pour prouver le disjoncteur (cold-garde-fous.py, G1).
NOTIFUSE_OUTBOUND_INTERNAL_ALLOW=127.0.0.1:1025,127.0.0.1:1026

# Invitation d'un membre par le Hub (fiches 46 et 86) : secret perdu à la bascule vers Nomad, reposé le 24/11/2026. Variable absente = route 503.
HUB_BASE_URL=https://hub.staging.veridian.site
# Hôte interne admis pour LIVRER un webhook (fiche 61) : le Hub de staging est sur le tailnet (F3).
NOTIFUSE_WEBHOOK_TRUSTED_HOSTS=hub.staging.veridian.site
# Jetons du Hub de staging (MCP fiche 83, actions fiche 87) : jamais ceux de prod.
VERIDIAN_HUB_JWKS_URL=https://hub.staging.veridian.site/.well-known/jwks.json
VERIDIAN_HUB_ISSUER=https://hub.staging.veridian.site
VERIDIAN_MCP_AUDIENCE=https://notifuse.staging.veridian.site/api/mcp
VERIDIAN_ACTIONS_AUDIENCE=https://notifuse.staging.veridian.site/api/veridian/actions
{{ if nomadVarExists "nomad/jobs/notifuse-staging/stack" }}{{ with nomadVar "nomad/jobs/notifuse-staging/stack" }}HUB_INVITATION_SECRET_NOTIFUSE={{ .HUB_INVITATION_SECRET_NOTIFUSE }}{{ end }}{{ end }}
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
    task "notifuse-staging-smtp-sink" {
      driver = "docker"
      config {
        # Durcissement Unix : empeche un processus non privilegie d'elever ses
        # droits via un binaire setuid. C'est le maillon entre « shell dans le
        # conteneur » et « root sur l'hote ». N'affecte PAS un processus qui
        # ABANDONNE ses droits au demarrage, seulement celui qui en gagne.
        security_opt = ["no-new-privileges:true"]

        # Identification lisible du conteneur (2026-09-07). Nomad ne pose que
        # `com.hashicorp.nomad.alloc_id` : rien ne disait a quelle application
        # appartenait un conteneur. Les quatre premieres valeurs sont
        # interpolees par Nomad a l'execution, les deux dernieres sont des
        # proprietes du fichier (dossier jobs/<tier>/ et table du script
        # scripts/poser-labels-conteneurs.py).
        labels = {
          "site.veridian.job"   = "${NOMAD_JOB_NAME}"
          "site.veridian.group" = "${NOMAD_GROUP_NAME}"
          "site.veridian.task"  = "${NOMAD_TASK_NAME}"
          "site.veridian.node"  = "${node.unique.name}"
          "site.veridian.tier"  = "saas-staging"
          "site.veridian.app"   = "notifuse"
        }
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
