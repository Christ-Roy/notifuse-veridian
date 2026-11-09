# Capacité base : une connexion par workspace (incident du 07/11/2026)

Chaque workspace a sa base `notifuse_ws_<id>` et un pool dans l'appli : plus il y a de workspaces, plus
il y a de connexions ouvertes, même sans activité. Mesure du 07/11 en prod : 93 workspaces (dont 58 de
tests), PostgreSQL `max_connections=100`, appli `DB_MAX_CONNECTIONS=100`, 105 sessions dont 98 inactives.
Résultat : `pq: sorry, too many clients already` et `connection limit reached 100/100` sur des
workspaces CLIENTS (segments, envois, webhooks), et `POST /api/tenants/provision` en 500.

## Règle
- `DB_MAX_CONNECTIONS` (appli) doit rester sous `max_connections` de PostgreSQL moins 30 de réserve
  (superuser, pgBackRest, migrations). `scripts/ci/check-nomad-contract.sh` le vérifie sur les deux
  jobspecs; `scripts/ci/test-nomad-contract-db.sh` prouve qu'il échoue quand ce n'est pas le cas.
- Staging : PostgreSQL 300, appli 250, `DB_MAX_CONNECTIONS_PER_DB=3`. Prod (09/11, base sur `pg-saas` partagée par 5 apps, `max_connections=100`) : appli `DB_MAX_CONNECTIONS=80` (`max_connections=200` sur pg-saas depuis le 09/11, une connexion par workspace car le worker d envoi les interroge toutes chaque seconde), `DB_MAX_CONNECTIONS_PER_DB=2`, `DB_CONNECTION_MAX_IDLE_TIME=15s` (avant : 25 connexions inactives pour 24 workspaces, 68/100 au total, refus 53300 à 12:08 et 18:05 UTC). Mesure : `select state,count(*) from pg_stat_activity where usename='notifuse' group by 1` doit rendre moins de 30. Changer `max_connections`
  redémarre la base au déploiement (pré-hook de sauvegarde habituel).
- Les workspaces de test des batteries (`e2e*`, `tst*`, préfixes de comptes `brunon5robert+…`) pèsent sur ce
  plafond tant qu'ils ne sont pas purgés : la purge est une décision de Robert, jamais automatique.

## Le pool ne fuit pas (mesure du 07/11)
`pkg/database/connection_manager.go` (upstream) garde 1 connexion inactive « chaude » par workspace
(`SetMaxIdleConns(1)`, `DB_CONNECTION_MAX_IDLE_TIME=5m`), réserve `DB_MAX_CONNECTIONS_PER_DB` (3) par pool
à la création et évince les pools inactifs les moins récents (LRU) quand le plafond approche. Les 98
inactives étaient donc normales : 1 par workspace interrogé. Mais le planificateur (tâches toutes les 20 s,
file d'envoi, webhooks, poller IMAP, automations) parcourt TOUS les workspaces, soft-supprimés compris :
50 des 93 workspaces étaient des tenants de test `deleted` qui gardaient chacun une connexion chaude.
Correctifs (07/11/2026) : (1) les 56 workspaces de test ont été purgés par `wipe-test-tenants` (sauvegarde
logique hors dépôt, `brunon5robert` = workspace personnel de Robert, jamais touché) : 105 -> 52 sessions ;
(2) `workspaceRepository.List` n'énumère plus les tenants `deleted` (patch minimal décidé par Robert, consigné
dans la fiche 41) : plus aucun parcours ne les touche, leur connexion chaude se ferme à 5 min d'inactivité ;
(3) test `workspace_core_test.go` (`listActiveWorkspacesQuery`), vu échouer avant le patch.

## Bases orphelines : la classe supprimée (07/11/2026)
17 bases `notifuse_ws_*` sans ligne `workspaces` (toutes vides: 0 contact, 0 message; 5 sans aucune table)
ont été sauvegardées puis supprimées (53 -> 36 bases pour 36 workspaces). Deux causes, deux correctifs :
- une suppression de workspace (`DeleteDatabase` upstream) faisait un DROP simple qui échouait quand le
  planificateur rouvrait une connexion : désormais `WITH (FORCE)` (patch listé en fiche 41, test vu échouer) ;
- un `provision` qui échouait entre la création de la base et celle de son schéma laissait une base sans
  record : `cleanupFailedProvisionDB` la retire, sauf si elle existait avant la tentative ou si le record
  existe (tests vus échouer par mutation). Aucune tâche planifiée ajoutée.
Contrôle : `select count(*) from pg_database where datname like 'notifuse_ws_%' and substr(datname,13) not in
(select id from workspaces)` doit rendre 0.

## Comptes locaux : le wipe retire aussi le propriétaire de test
`wipe-test-tenants` supprimait le workspace et sa base mais laissait le compte local du propriétaire :
79 comptes humains sans aucun workspace le 07/11/2026 (restes des batteries). `wipeOneTenant` appelle désormais
`pruneOrphanedOwners` : un humain du workspace wipé qui n'a plus AUCUN workspace est supprimé (compte et sessions).
Jamais le root, le co-owner par défaut, un compte machine ni un compte rattaché ailleurs ; une erreur de lecture
ne supprime rien. Tests: `TestPruneOrphanedOwners`, `TestVeridianService_WipeTestTenants_PrunesOrphanedOwner`.

## Cause réelle des orphelines : le gestionnaire de connexions recréait la base
Mesure du 07/11 après le déploiement du FORCE : 9 nouvelles bases complètes (38 tables) sans ligne `workspaces`,
alors que les logs du wipe disaient « database dropped ». `connectionManager.createWorkspacePool` (upstream) appelle
`EnsureWorkspaceDatabaseExists` : toute tâche ou tout webhook en vol qui demande une connexion pour un workspace
supprimé RECRÉE sa base. Correctif (patch listé en fiche 41) : pas de ligne `workspaces`, pas de base.

## Reprovisionnement après wipe (09/11/2026)

Le wipe retirait les propriétaires humains orphelins mais laissait le compte machine `veridian-api-<tenant>@...` : reprovisionner le même slug répondait 500 (« create api key: this user already exists »). `pruneOrphanedOwners` supprime maintenant aussi le compte machine de CE tenant (préfixe exact, aucun autre workspace), jamais celui d un autre tenant.

## Haute disponibilité : transport durci et tenants supprimés (09/11/2026)

Incident : après le redémarrage du primaire `pg-saas` (14:31 UTC), des tâches de l app restaient bloquées 80 s par salves et la boîte de réception chargeait en boucle jusqu au redémarrage de l allocation. `pkg/pgdial` ouvre le pool système et les pools de workspace avec un `TCP_USER_TIMEOUT` de 20 s et un keepalive (15 s, 5 s, 3 sondes) : une connexion morte sans RST est coupée au lieu d attendre ~15 min. À prouver par une bascule volontaire (`ss -o` montre le keepalive). Le scheduler ignore aussi les tâches d un tenant supprimé en douceur (`veridian_plan.deleted_at`), qui bouclaient en 402 et remplissaient le lot de 100 ; elles reprennent à la restauration.

### Bascule du 09/11 (18:05 UTC) et reprise seule

Le pool système gardait des connexions inactives mortes : chaque vérification `/healthz` en consommait une et échouait, quatre échecs faisaient redémarrer la tâche (2 min 30 de coupure). `pingWithRecovery` jette les connexions inactives au premier échec et réessaie sur une connexion neuve (1,5 s puis 3 s, sous les 5 s du check). Le dialer respecte le contexte (`DialContext`), keepalive 10 s / 3 s / 3 sondes et `TCP_USER_TIMEOUT` 10 s. `check_restart` du job passe de 4 à 6 échecs (90 s) : le redémarrage reste le dernier filet. Preuve attendue : une bascule sous sonde, zéro redémarrage et moins de 30 s de coupure.

### Seconde bascule (09/11, 19:01 UTC) : pool coincé

Après 3e2b53a3, la sonde a encore vu `Database ping failed: context deadline exceeded` pendant 2 min alors que le nouveau primaire répondait (tâche redémarrée par Nomad). Le pool était plein de connexions bloquées (sessions mortes côté HAProxy : le TCP reste vivant, ni keepalive ni TCP_USER_TIMEOUT ne les voient) et le ping attendait un emplacement. `/healthz` : (1) ping sur le pool ; (2) sinon ping sur une connexion DÉDIÉE hors pool (`SetHealthDSN`) : échec = base injoignable (503) ; (3) sinon `pgdial.CloseAll()` ferme de force toutes les connexions de l app (les requêtes bloquées échouent, les pools se reconnectent), au plus une fois toutes les 10 s, puis nouveau ping. Preuve attendue : une bascule sous sonde, zéro redémarrage, coupure < 30 s. Piste côté infra : fermer les sessions HAProxy quand le leader change.

### Bases workspace fermées à PUBLIC (09/11/2026)

Sur `pg-saas` (cluster partagé par 5 apps), une base créée par `CREATE DATABASE` naît ouverte à tout rôle. `EnsureWorkspaceDatabaseExists` exécute donc, juste après la création, `REVOKE ALL ON DATABASE … FROM PUBLIC` puis `GRANT CONNECT` au propriétaire (rôle de l appli) et à `pgsaas_mon` s il existe (`restrictWorkspaceDatabase`, tests vus échouer sans le REVOKE). Un échec du REVOKE fait échouer la création. Contrôle : `select has_database_privilege('hub', 'notifuse_ws_<id>', 'CONNECT')` doit rendre false.

### Troisième bascule (09/11, 20:08 UTC)

Le pool ne tombait plus seul mais `/healthz` restait rouge 80 s (Traefik le sonde avec 1 s de délai, Nomad redémarre après 90 s) et le démarrage suivant tombait sur l ancien primaire devenu replica (`cannot execute CREATE TABLE in a read-only transaction`, sortie 1). Maintenant : une sonde de fond (`StartDBHealth`, toutes les 2 s : pool, sonde dédiée, fermeture forcée) ; `/healthz` lit son verdict et reste vert 45 s après le dernier succès (une bascule dure 30 à 45 s), puis rouge, `check_restart` reste le dernier filet. Les changements d état et les fermetures forcées sont journalisés (`db health: …`). `InitializeDatabaseResilient` réessaie 90 s tant que l erreur est transitoire (replica, arrêt, EOF) au lieu de sortir en 1.

Le pool ne garde jamais une connexion sur un replica : `rwConnector` (pgdial) vérifie `SHOW transaction_read_only` à chaque ouverture et réessaie ~2,4 s (6 × 400 ms), le temps que HAProxy écarte l ancien leader (fenêtre de ~2 s au retour de bascule, `fall 2 rise 2`).
