# notifuse (PROD) — job Nomad Veridian, source de vérité GitOps de CE repo.
#
# Sert les vrais clients sur https://notifuse.app.veridian.site (ingress Traefik, TLS Let's Encrypt). Notifuse Go
# port 8081, health /healthz. Base sur la grappe pg-saas (Patroni), jointe par pg-saas-entree, présente sur chaque nœud
# app (172.26.64.1:15432 route vers le primaire) : aucun état sur le nœud, le job n'est plus épinglé.
# Secrets = Nomad Variable `nomad/jobs/notifuse` (JAMAIS en clair ici).
#
# DEUX EXEMPLAIRES (VER-270, 27/11/2026), un par zone des nœuds `app` (ovh-prod, app-01) : la perte d'un nœud ne coupe
# plus Mail. Le HTTP, le relais SMTP, l'IMAP et le MX des boîtes sont servis par les deux. L'envoi, la relève IMAP, les
# webhooks et le renouvellement du certificat ne tournent que sur l'exemplaire qui MÈNE (verrou consultatif PostgreSQL,
# internal/veridian/leader) : leurs portes de débit vivent en mémoire, deux workers enverraient deux fois plus vite.
# Coupe-circuit : VERIDIAN_LEADER=off, à n'employer qu'avec count = 1.
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
    # Annonce des tags Traefik (règle 23, garde-tags-canari) : empreinte des tags du déploiement suivant (rejeu
    # retry-idempotent@file, délai du healthcheck 2 s). Posée seule d'abord (déploiement destructif), les tags ensuite (en place).
    meta {
      veridian_tags_canari_annonce = "684fd0447f527a90"
    }

    count = 2

    # Placement par étiquette, jamais par machine (cible d'infra du 27/11) : les nœuds `app` (ovh-prod zone ovh,
    # app-01 zone app01), un exemplaire par zone. Les deux portent pg-saas-entree et le réseau tailscale.
    constraint {
      attribute = "${meta.role}"
      operator  = "set_contains"
      value     = "app"
    }
    spread {
      attribute = "${meta.zone}"
    }

# veridian.db_max_connections=300 (base sur pg-saas depuis le 09/11 : a changer des deux cotes ensemble ; 2 x 45 cote appli, VER-270)
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
      # IMAP des boîtes louées (fiche 84) : port DYNAMIQUE, tailnet seul. Aucun port public.
      port "imap" {
        to           = 1993
        host_network = "tailscale"
      }
      # SMTP des boîtes derrière l'entrée TLS publique (Traefik, 443 par nom). Dynamique, tailnet seul.
      port "boxsmtp" {
        to           = 5465
        host_network = "tailscale"
      }
      # Récepteur de courrier des boîtes louées (fiche 84) : port DYNAMIQUE, tailnet seul. Rien de public.
      port "mx" {
        to           = 2525
        host_network = "tailscale"
      }
      # Métriques de l'activité Mail vers le Hub (fiche 98) : écouteur à part, tailnet seul, jamais derrière Traefik (le 8081 est public).
      port "activite" {
        to           = 9464
        host_network = "tailscale"
      }
    }

    # L'IMAP des boîtes louées au catalogue : l'entrée publique le suivra quand le conteneur bouge (fiche 84).
    service {
      name     = "notifuse-imap"
      provider = "nomad"
      port     = "imap"
      # Public par le 443 (oui de Robert, 24/11/2026) : Traefik -> entrée HAProxy de l'infra -> ici. Les étiquettes Traefik vivent sur
      # l'entrée, jamais sur ce groupe à canari (fusible garde-tags-canari). PROXY v2 exigé : la vraie IP du client.
      tags     = ["traefik.enable=false"]
    }

    service {
      name     = "notifuse-boxsmtp"
      provider = "nomad"
      port     = "boxsmtp"
      tags     = ["traefik.enable=false"]
    }

    service {
      name     = "notifuse-mx"
      provider = "nomad"
      port     = "mx"
      tags     = ["traefik.enable=false"]
    }

    # Prometheus de la flotte lit /metrics ici (file de l'activité Mail : retard, en attente, lots par verdict).
    service {
      name     = "notifuse-activite"
      provider = "nomad"
      port     = "activite"
      tags     = ["traefik.enable=false"]
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
      # Pas de check Nomad ici (10/11/2026) : sa connexion TCP fermee sans QUIT journalisait « smtp handler error:
      # connection reset by peer » toutes les 10 s. HAProxy (notifuse-smtp-entree) controle deja chaque instance
      # par banniere 220 puis QUIT 221 et retire celle qui ne repond pas.
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
        ports = ["http", "smtp", "imap", "mx", "boxsmtp"]
        # Plus de volume hôte (27/11) : /opt/veridian-lab/notifuse/data d'ovh-prod mesuré vide (4 Ko) et aucun code ne lit
        # /app/data. L'image tourne en nobody (65534), rootfs en lecture seule, /tmp en tmpfs.
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
# Boîtes louées (fiche 84) : TLS terminé par Traefik (443 par nom), PROXY v2 exigé (la vraie IP du client).
VERIDIAN_IMAP_ENABLED=true
# Un compte supprimé au Hub à partir de cette date n'ouvre plus rien (fiche 91). Reculer la date ferme ceux d'avant (décision de Robert). Absente ou
# illisible : l'application refuse de démarrer.
VERIDIAN_ACCOUNT_GATE_SINCE=2026-11-25T00:00:00Z
VERIDIAN_IMAP_PORT=1993
VERIDIAN_IMAP_BEHIND_TLS_PROXY=true
VERIDIAN_IMAP_PROXY_PROTOCOL=true
VERIDIAN_BOX_HOST=imap.veridian.site
VERIDIAN_IMAP_PUBLIC_PORT=443
VERIDIAN_BOX_SMTP_PORT=5465
VERIDIAN_BOX_SMTP_HOST=smtp.veridian.site
VERIDIAN_BOX_SMTP_PUBLIC_PORT=443
VERIDIAN_BOX_SMTP_PROXY_PROTOCOL=true
# Récepteur : adresses de profil de ces domaines seulement. Entrée publique 25 = job de l'infra, PROXY v2 exigé.
VERIDIAN_MX_PROXY_PROTOCOL=true
# Seuls les HAProxy de l'infra (notifuse-mx-entree) ont le droit d'annoncer une adresse client en PROXY v2 (26/11/2026).
# Vides, n'importe quel pair du tailnet s'inventait une adresse : vu depuis le bastion, en-tête forgé accepté sur les trois.
# Mesuré dans le réseau du conteneur : HAProxy arrive par 172.26.64.1 (passerelle du pont d'ovh-prod, même nœud) ;
# 100.88.202.29 (tailnet d'ovh-prod) pour le cas où Notifuse et son entrée seraient sur deux nœuds.
VERIDIAN_MX_TRUSTED_PROXIES=172.26.64.1/32,100.88.202.29/32
VERIDIAN_IMAP_TRUSTED_PROXIES=172.26.64.1/32,100.88.202.29/32
VERIDIAN_BOX_SMTP_TRUSTED_PROXIES=172.26.64.1/32,100.88.202.29/32
# « domaine=espace » : l'espace propriétaire reçoit les adresses sans profil (contact@, postmaster@...). robertbrunon = Veridian,
# sans redirection ; jamais greenbeamcraft (client, redirige ses réponses chez lui). 26/11/2026, fiche 84.
VERIDIAN_MX_DOMAINS=messagerie-nord-776.fr=robertbrunon
# CSP de la console (VER-265, fiche 96) : report = Content-Security-Policy-Report-Only, enforce = bloquante, off = aucune.
VERIDIAN_CSP_MODE=enforce
# Activité Mail vers le Hub par lots (CONTRAT-HUB §5.25, fiche 98). Liste blanche d'espaces (virgules) : 27/11, l'org Veridian de
# Robert (robertbrunon, tenant 359b76d5, CRM veridian-4) pour VER-267 ; l'org jetable e2esso5yd8jj8ljzwxtr, démontée, en est sortie.
# Rien n'entre en file sans l'abonnement du Hub ; un espace hors liste garde sa file (7 jours, 100 000 au plus), rien ne part.
# Ouvrir à tous = vider VERIDIAN_ACTIVITE_WORKSPACES, sur décision (fiche 98, « Ouvrir à toutes les orgs »).
VERIDIAN_ACTIVITE_ENABLED=true
VERIDIAN_ACTIVITE_WORKSPACES=robertbrunon
VERIDIAN_ACTIVITE_METRICS_PORT=9464
VERIDIAN_MX_HOST=mx.veridian.site
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
# Plafond TOTAL de l appli (systeme + tous les workspaces) : pg-saas sert 5 apps, max_connections=200 depuis le 09/11 (budget : Notifuse 80, Twenty 24-40, Hub 10, Prospection 8, CMS 5, plateforme ~15) ;
# Notifuse en tenait 25 au repos et PostgreSQL refusait des connexions (53300, 09/11). Connexions inactives
# fermees apres 15 s (le planificateur repasse toutes les 20 s : une connexion chaude par workspace n'a pas de sens).
# Deux exemplaires (VER-270) : 45 chacun, 90 au total. Le leader garde une connexion par espace (30 geres le 27/11) plus
# le systeme et le HTTP ; le suiveur ne parcourt aucun espace (son worker ne voit rien) et ne sert que le HTTP.
DB_MAX_CONNECTIONS=45
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
# Seul hôte interne admis pour LIVRER un webhook (fiche 61) : le job tunnel-bridge, abonnement « Tunnel CRM Robert » de robertbrunon.
NOTIFUSE_WEBHOOK_TRUSTED_HOSTS=bridge.staging.veridian.site

# Invitation d'un membre par le Hub (fiches 46 et 86) : secret perdu à la bascule vers Nomad, reposé le 24/11/2026. Variable absente = route 503.
{{ if nomadVarExists "nomad/jobs/notifuse/stack" }}{{ with nomadVar "nomad/jobs/notifuse/stack" }}HUB_INVITATION_SECRET_NOTIFUSE={{ .HUB_INVITATION_SECRET_NOTIFUSE }}{{ end }}{{ end }}
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
