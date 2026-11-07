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
- Prod et staging : PostgreSQL 300, appli 250, `DB_MAX_CONNECTIONS_PER_DB=3`. Changer `max_connections`
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
