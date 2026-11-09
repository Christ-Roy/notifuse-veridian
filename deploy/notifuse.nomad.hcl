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
#    scripts/ci/nomad-ssh-deploy.sh) pousse CE fichier au bastion via le verbe
#    `put-job prod`, puis `deploy prod <TAG>`. Le pré-pull ghcr authentifié sur
#    ovh-prod et le `nomad job run -var image_tag=<TAG>` tournent côté bastion, sous
#    commande forcée (constat C4 : la clé CI n'y ouvre plus de shell).
#    Le NOMAD_TOKEN ne quitte JAMAIS le bastion. Déployer TOUJOURS depuis CE HCL
#    (il déclare `variable image_tag`), jamais la copie ~/nomad-veridian/jobs/.
# ⚠️ DB mono-instance sans HA (reschedule OFF, bind ovh-prod) — migration Patroni HA =
#    chantier infra séparé (backlog nomad-veridian). Ne PAS reschedule ce job stateful.
variable "image_tag" {
  type        = string
  # Recale sur ce qui tourne reellement en prod : le defaut retardait de
  # deux versions majeures et un deploiement hors CI aurait retrograde Notifuse.
  # Recale le 2026-10-07 (calibrage memoire I07) : mesure sur le job Nomad
  # vivant = v60.0-veridian.9449a2e7, juste avant ce commit (nomad job
  # inspect notifuse). Le defaut precedent (v57.0) aurait retrograde l'image
  # si deploye hors CI avec ce commit.
  # Recale le 2026-11-08 : la prod sert v62.0-veridian.9a516835 (nomad job inspect), le defaut v60 l'aurait retrogradee.
  default     = "v62.0-veridian.fc61df4a"
  description = "Tag GHCR de l'image notifuse à déployer (passé par la CI via -var)."
}

job "notifuse" {
  datacenters = ["veridian-eu"]
  type        = "service"
  priority    = 80

# veridian-contract:start
# veridian.contract.version=1
# veridian.managed_by=repo
# veridian.environment=production
# veridian.tier=saas-prod
# veridian.criticality=B
# veridian.owner=messaging
# veridian.objective=availability-99.9
# veridian.rto_minutes=5
# veridian.rpo_minutes=15
# veridian.state=local-state
# veridian.mobility=local-gap
# veridian.preemptible=false
# veridian.staging_job=notifuse-staging
# veridian.promotion_policy=staging-required
# veridian-contract:end

  group "stack" {
    count = 1

    # Épinglé à ovh-prod : db/data bind sur /opt/veridian-lab/notifuse d'ovh-prod (migré
    # du bastion le 2026-07-15). Le volume ne suit pas l'alloc → NE PAS changer de nœud.
    constraint {
      attribute = "${meta.provider}"
      value     = "ovh-prod"
    }

# veridian.db_max_connections=300 (base sur pg-saas depuis le 09/11 : a changer des deux cotes ensemble)
    # Bleu-vert (08/11) : le canari demarre a cote de l'ancienne, Traefik et l'entree SMTP les servent toutes deux, la
    # promotion arrete l'ancienne apres son shutdown_delay. Possible depuis que le relais SMTP a un port dynamique : le
    # 587 fixe est tenu par le job notifuse-smtp-entree (nomad-veridian), qui suit le service `notifuse-smtp` au
    # catalogue. Deux instances en meme temps sont sures : les files (emails, taches, automatisations) se prennent en
    # FOR UPDATE SKIP LOCKED. La base vit sur pg-saas : un deploiement ne la touche pas.
    update {
      max_parallel     = 1
      canary           = 1
      auto_promote     = true
      min_healthy_time = "15s"
      healthy_deadline = "5m"
      auto_revert      = true
    }

    restart {
      attempts = 10
      interval = "10m"
      delay    = "2s"
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
      # Relais SMTP de soumission (fiche 71) : port DYNAMIQUE, tailnet seul. Les clients gardent
      # relais-smtp.veridian.site:587, tenu par le job notifuse-smtp-entree qui transmet ici. Pas de port public.
      port "smtp" {
        to           = 587
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
        # Controle actif de Traefik : une instance qui ne repond plus sort du lot en 2 s, sans attendre le catalogue.
        "traefik.http.services.notifuse.loadbalancer.healthcheck.path=/healthz",
        "traefik.http.services.notifuse.loadbalancer.healthcheck.interval=2s",
        "traefik.http.services.notifuse.loadbalancer.healthcheck.timeout=1s",
      ]
      check {
        type     = "http"
        path     = "/healthz"
        interval = "15s"
        timeout  = "5s"
      }
    }

    # Le relais SMTP au catalogue : notifuse-smtp-entree le lit pour savoir ou transmettre le 587.
    service {
      name     = "notifuse-smtp"
      provider = "nomad"
      port     = "smtp"
      tags     = ["traefik.enable=false"]
      check {
        type     = "tcp"
        interval = "10s"
        timeout  = "3s"
      }
    }

    # ---- notifuse (Go, port 8081) ----
    task "notifuse" {
      driver         = "docker"
      user           = "nobody"
      # Traefik relit le catalogue toutes les 15 s : l'allocation sortante sert encore pendant qu'il l'oublie.
      shutdown_delay = "30s"
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
            limit           = 6 # 90 s : une bascule de la grappe (30 a 45 s) ne redemarre plus la tache, le redemarrage reste le dernier filet
            grace           = "90s"
            ignore_warnings = false
          }
        }
      }
      config {
        # Durcissement Unix : empeche un processus non privilegie d'elever ses
        # droits via un binaire setuid. C'est le maillon entre « shell dans le
        # conteneur » et « root sur l'hote ». N'affecte PAS un processus qui
        # ABANDONNE ses droits au demarrage, seulement celui qui en gagne.
        security_opt    = ["no-new-privileges:true"]
        readonly_rootfs = true
        cap_drop        = ["ALL"]

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
          "site.veridian.tier"  = "saas-prod"
          "site.veridian.app"   = "notifuse"
        }
        image = "ghcr.io/christ-roy/notifuse-veridian:${var.image_tag}"
        ports = ["http", "smtp"]
        volumes = [
          "/opt/veridian-lab/notifuse/data:/app/data",
        ]

        # 🔴 L'image ne porte aucun utilisateur applicatif nomme (Alpine
        # minimal, /etc/passwd = root/postgres/nobody). Tournait en root
        # jusqu'ici. uid 65534 (nobody) choisi ; le dossier hote
        # /opt/veridian-lab/notifuse/data a ete rechown en 65534:65534
        # AVANT ce deploiement (verifie : contenu vide, aucune perte).
        mount {
          type     = "tmpfs"
          target   = "/tmp"
          readonly = false
          tmpfs_options { size = 67108864 }
        }
      }
      template {
        destination = "secrets/app.env"
        perms       = "600"   # secret: lisible du seul proprietaire
        env         = true
        data        = <<EOH
SERVER_PORT=8081
# Relais SMTP (fiche 71). Certificat GÉRÉ par l'application (Let's Encrypt, DNS Cloudflare) : aucun certificat
# copié ici. VERIDIAN_RELAY_CERT_MANAGED=true est l'amorçage : le bridge démarre en starttls, le certificat
# géré est relu depuis le stockage au démarrage ; sans certificat rendu, aucun AUTH n'est annoncé (fail closed).
# Port 587 dans le conteneur ; dehors, port dynamique tailnet derriere notifuse-smtp-entree.
SMTP_BRIDGE_ENABLED=true
SMTP_BRIDGE_PORT=587
SMTP_BRIDGE_DOMAIN=relais-smtp.veridian.site
SMTP_BRIDGE_TLS=starttls
VERIDIAN_RELAY_CERT_MANAGED=true
SERVER_HOST=0.0.0.0
ENVIRONMENT=production
DEPLOY_ENV=prod
DB_HOST=172.26.64.1
DB_PORT=15432
DB_USER=notifuse
DB_PREFIX=notifuse
DB_NAME=notifuse_system
DB_SSLMODE=disable
# Plafond TOTAL de l'appli (systeme + tous les workspaces) : pg-saas sert 5 apps avec max_connections=100 ;
# Notifuse en tenait 25 au repos et PostgreSQL refusait des connexions (53300, 09/11). Connexions inactives
# fermees apres 15 s (le planificateur repasse toutes les 20 s : une connexion chaude par workspace n'a pas de sens).
DB_MAX_CONNECTIONS=30
DB_MAX_CONNECTIONS_PER_DB=2
DB_CONNECTION_MAX_IDLE_TIME=15s
DB_CONNECTION_MAX_LIFETIME=5m
TASK_SCHEDULER_ENABLED=true
TASK_SCHEDULER_INTERVAL=20s
TASK_SCHEDULER_MAX_TASKS=100
TELEMETRY=false
CHECK_FOR_UPDATES=false
API_ENDPOINT=https://notifuse.app.veridian.site
INTERNAL_API_ENDPOINT=http://localhost:8081
VERIDIAN_DEFAULT_PLAN=free
# IP tailnet du bastion (100.108.136.89) et de app-01 (100.81.160.83, relai ASD) autorisees pour les integrations IMAP/SMTP seulement (fiche 67).
NOTIFUSE_TRUSTED_INTERNAL_HOSTS=100.108.136.89,100.81.160.83

{{ with nomadVar "nomad/jobs/notifuse" }}
DB_PASSWORD={{ .PG_SAAS_PASSWORD }}
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
        # Pic 30j (obs-nomad pic, 2026-10-07) : 58 Mo pour une reservation de
        # 128 Mo (ratio 45%, >2x le pic). Rapproche a 100 Mo (ratio ~1.7x,
        # toujours sain) pour liberer de la marge SCHED sur ovh-prod (I07).
        # 08/11 : reserve = memoire non reprenable mesuree (54 Mio) + marge, plafond = pointe (101 Mio) x 7, de quoi tenir une
        # campagne. Avant : 7000 Mio, une fuite de Notifuse pouvait avaler ovh-prod et toutes les apps qui y tournent.
        memory     = 128
        memory_max = 768
      }
    }
  }
}
