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
Levier simple et propre : purger ces tenants (décision de Robert). Levier de code, non fait : exclure les
tenants `deleted` dans `workspaceRepository.List` (fichier upstream, 7 appelants) ; à décider avec la règle 2.
