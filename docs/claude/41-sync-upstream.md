# Sync upstream

```bash
git checkout main && git pull upstream main
git checkout veridian && git merge main
```

Pre-push hook détecte les commits dont l'auteur est `@notifuse.com` et bypasse
le mapping 1-pour-1 sur les fichiers **non-veridian_***. Les fichiers
`veridian_*.go` restent sous discipline stricte même dans un sync upstream.


## Patchs Veridian sur des fichiers upstream (à rejouer après un sync)

Règle 2 de CLAUDE.md : le code Veridian vit dans ses propres fichiers. Exceptions décidées, minimales :
- `internal/repository/workspace_postgres.go` `List` (07/11/2026, Robert via la parente) : exclut les tenants
  `veridian_plan.status = 'deleted'` pour que ni le planificateur, ni la file d'envoi, ni les webhooks, ni le
  poller IMAP ne gardent une connexion PostgreSQL par tenant supprimé. Test : `workspace_core_test.go`
  (`listActiveWorkspacesQuery`). Voir fiche 81.
- `internal/repository/workspace_postgres.go` `DeleteDatabase` (07/11/2026, parente) : `DROP DATABASE … WITH (FORCE)`
  au lieu du DROP simple, que la réouverture de connexions par le planificateur faisait échouer ("being
  accessed") en laissant une base orpheline. Test : `TestWorkspaceRepository_DeleteDatabase_DropsWithForce`.
- `pkg/database/connection_manager.go` `createWorkspacePool` (07/11/2026, parente) : refuse de créer la base d'un
  workspace sans ligne `workspaces` (`ErrWorkspaceRecordMissing`). Avant, une tâche ou un webhook en vol qui demandait
  une connexion après un wipe RECRÉAIT la base avec ses 38 tables (9 orphelines mesurées, alors que les logs
  disaient « database dropped »). Test : `TestConnectionManager_GetWorkspaceConnection_RefusesWorkspaceWithoutRecord`.
