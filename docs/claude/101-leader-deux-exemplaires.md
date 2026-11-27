# 101 · Deux exemplaires en prod, un seul leader (VER-270, 27/11/2026)

## Pourquoi un leader
La cible d'infra veut chaque job de prod en deux exemplaires (perte d'un nœud sans coupure). Mesuré dans le code le 27/11 :
les files (emails, tâches, automatisations, broadcasts) se prennent en `FOR UPDATE SKIP LOCKED`, donc jamais deux envois du
même mail. Mais quatre travaux de fond ne supportent pas d'être faits deux fois :
- **l'envoi** : les portes de débit vivent en mémoire du processus (throttle par classe et par minute
  `veridian_provider_rate_limiter.go`, limiteur par intégration, disjoncteurs, facteurs de réputation, étalement
  `veridian_pacing.go`). Deux workers enverraient deux fois plus vite par classe, sur les domaines des clients (règle 4) ;
- **la relève IMAP** (`veridian_imap_poller.go`) : deux connexions par boîte client, rebonds et stop-on-reply joués deux fois ;
- **les webhooks** : l'amont les prend sans verrou (`GetPendingForWorkspace`), deux exemplaires les livreraient deux fois ;
- **le certificat du relais** (`relaycert`) : deux émissions ACME, et un suiveur ne verrait jamais le certificat neuf.

## Comment
`internal/veridian/leader` : verrou consultatif de session `pg_try_advisory_lock(270270)` sur une connexion dédiée de la
base système, vérifié toutes les 5 s dans `pg_locks`. Perdu avec la session (instance arrêtée ou tuée), rendu à l'arrêt
propre. Au plus un leader ; pendant une bascule, aucun pendant quelques secondes : l'envoi attend, rien ne part en double.
Branchement sans toucher l'amont : les workers parcourent `workspaceRepo.List` à chaque tour, ils reçoivent un dépôt
enveloppé (`leader.Follow`) dont la liste est vide sur un suiveur. Câblage : `internal/app/veridian_leader.go`, appelé au
début de `WireVeridian`, avant le démarrage des workers. Le relais : `Manager.FollowLeader`, le suiveur relit le stockage.
Le HTTP, le relais SMTP de soumission, l'IMAP, le SMTP et le MX des boîtes sont servis par les deux exemplaires.

Coupe-circuit : `VERIDIAN_LEADER=off` (chaque exemplaire mène), à n'employer qu'avec `count = 1`.

## Job
`count = 2`, contrainte `${meta.role}` contient `app` (ovh-prod zone ovh, app-01 zone app01), `spread` sur `${meta.zone}`.
`DB_MAX_CONNECTIONS=45` par exemplaire (90 au total pour 200 sur pg-saas). Plus de volume hôte (mesuré vide).
Les entrées publiques (MX, IMAP, SMTP) tournent sur ovh-prod : l'exemplaire d'app-01 les reçoit du tailnet depuis
100.88.202.29, déjà dans `VERIDIAN_*_TRUSTED_PROXIES`. Une entrée déplacée ailleurs doit y ajouter son adresse.

## Lire qui mène
Journal de chaque allocation : « leader : cet exemplaire mène… » à la prise, « …ne mène plus » à la perte.
`obs logs notifuse --since 4h --regex "leader :"` (le jeton Nomad par défaut n'a pas le droit de lire les journaux).

## Mesures (27/11)
Bascule au déploiement : verrou lâché et repris dans la même seconde, aucun double leader pendant le canari.
SIGKILL du leader (`docker kill`) : repris en 2,8 s ; reprise bornée par le tick de 5 s. Un rechargement de
pg-saas-entree (HAProxy, `shutdown-sessions`) coupe la connexion du verrou : « verrou perdu », repris en 5 s.

## Routes Traefik à deux exemplaires
On ne modifie jamais une définition Traefik existante (garde-tags-canari, nomad-veridian) : on ajoute sous un nom
neuf, puis on retire l'ancien. Piège payé le 27/11 à 21:29 : les routes neuves en `priority=1000` ont gagné dès que le
seul canari les portait, avant que Traefik le voie sain : 30 s de 503 sur notifuse.app. Une route neuve ne doit pas
prendre le trafic avant que toutes les instances la portent (priorité inférieure ou égale à l'ancienne).
État : bloc `notifuse-web` (notifuseweb*, rejeu retry-idempotent@file, santé 2 s) en prod ; RELÈVE : retirer le bloc
`notifuse` (ancien) au second temps, la garde le laisse passer depuis 8fcb03f1.

## Relève 35.0 (clics par lien)
En prod (47856acd, 0aa20c70), preuve d'un clic NON ABOUTIE : l'espace `veridian` n'a pas de fournisseur marketing.
Recette : un espace interne avec envoi marketing, campagne vers une boîte de test à nous, GET
`/visit?mid=<message>&wid=<espace>&url=<lien>&ts=<envoi>` plus de 7 s après, navigateur réel, puis
`notifuse liens:rapport --broadcast <id>`.
