# CLI utilisateur à parité avec l'admin pour les profils d'envoi (fiche 79, 04/11/2026)

## La décision

Robert, 04/11/2026 : « Tout ce qu'on peut faire sur un workspace, l'utilisateur de ce workspace devrait pouvoir
faire pareil avec son API et son CLI. » Déclencheur : `notifuse integrations:cold <ws> --id <profil>
--reply-forward-to ...` (fiche 78) était refusée au CLI utilisateur (« refusée par `notifuse` : admin, HMAC ou
cross-workspace »), alors qu'elle ne concerne qu'UN workspace.

## Qui peut quoi (règle la plus restrictive qui satisfait le besoin)

| Action | Propriétaire | Clé API / membre avec `workspace:write` | Clé en lecture seule | Clé d'un autre workspace |
|---|---|---|---|---|
| Créer / modifier un profil d'envoi **SMTP** (dont Gmail) et une boîte **IMAP** (`createIntegration`, `updateIntegration`, `emailProfiles.create`) | oui | **oui** (nouveau) | 403 | 403 |
| Vérifier le transport d'un profil (`emailProfiles.verify`, 5 par minute et par workspace) | oui | **oui** (nouveau) | 403 | 403 |
| Types `supabase`, `llm`, `firecrawl`, fournisseurs HTTP (SES, SparkPost, Mailgun...) | oui | non (403) | 403 | 403 |
| Supprimer une intégration | oui | non (403) | 403 | 403 |
| Pause, reprise, usage d'un profil (`emailProfiles.setUsage|pause|resume`) | oui | oui (déjà) | 403 | 403 |

Code : `internal/service/veridian_integration_authz.go` (garde), appelée par `workspace_service.go`
(`CreateIntegration`, `UpdateIntegration`, patch minimal, `Frozen-Override` tracé : deux lignes de condition
remplacées), `veridian_email_profile_create_service.go` et `veridian_email_profile_verify_service.go`.

Trois garde-fous qui ne sont pas dans le CLI mais dans le serveur :

1. **Isolation** : `AuthenticateUserForWorkspace` refuse tout workspace dont l'appelant n'est pas membre. Un test
   négatif par route ouverte (service) et la mesure staging (clé de A sur B).
2. **Lecture seule** : le droit est `workspace:write` (`UserWorkspace.HasPermission`). Une clé mintée
   `keys:mint --role readonly` ne l'a pas.
3. **Un identifiant stocké ne suit jamais une nouvelle destination** : pour un non-propriétaire, changer l'hôte,
   le port ou l'identifiant d'un SMTP ou d'un IMAP sans ressaisir le mot de passe est refusé (400). Sans cette
   garde, `updateIntegration` conserve le mot de passe chiffré déjà stocké (`veridianPreserveEmailProviderSecrets`)
   et l'enverrait à l'hôte choisi par l'appelant. Le propriétaire n'est pas concerné (il connaît le mot de passe).
   Les secrets ne sont jamais renvoyés : la réponse de `workspaces.get` est caviardée (fiche 10), les réponses
   de création, de modification et de vérification ne portent que des identifiants.

Voie la plus restrictive retenue là où c'était ambigu, à rouvrir par décision de Robert :
suppression d'un profil (irréversible, reste propriétaire) ; `settings:set|update` (`workspaces.update` remplace
tout le workspace) ; équipe, clés, jetons d'agent, webhooks sortants (fiche 67) ; types LLM, Firecrawl, Supabase ;
fournisseurs non SMTP ; `llm:chat` et `provider-webhooks:*` (appellent un tiers payant avec les identifiants du
workspace) ; export des secrets (HMAC Hub seul, fiche 76) ; plan, facturation, quotas, `tenants:*` (plateforme).

## Constats de l'audit mesuré (staging, tenants jetables, clés réelles)

- **Faille corrigée dans ce lot : `tasks.*` n'avait aucun contrôle d'appartenance.** `TaskService` n'appelle jamais
  `AuthenticateUserForWorkspace`. Mesure : la clé du workspace A faisait `GET /api/tasks.list?workspace_id=B` et
  recevait les tâches de B (HTTP 200). Même trou pour `tasks.get|create|delete|reset|trigger`. Fermé par la table du
  middleware `VeridianWorkspaceAuthzRoutes` (membre du workspace requis). Test : `veridian_workspace_authz_test.go`.
- `profiles:create` était listée au CLI utilisateur mais le serveur exigeait le propriétaire : elle échouait en 403
  avec une clé. Corrigée par ce lot (même garde).
- `webhooks:create|update|delete|toggle|regenerate|test` sont listées au CLI utilisateur mais refusées (403) aux clés
  depuis la fiche 67. Laissées ainsi (voie restrictive) : décision de Robert si on veut les rouvrir.
- Les refus d'une clé étrangère sont des 403 « not authorized for this workspace » (la sonde ne peut pas distinguer
  « workspace inexistant » de « pas membre »). Sur `workspaces.get`, le refus est un 500 hérité de l'amont.
- Plusieurs routes valident le corps AVANT d'autoriser (400 identique pour A sur A et A sur B) : ces 400 ne prouvent
  rien sur l'isolation. Pour les routes ouvertes par ce lot, la preuve est faite avec des corps valides (section suivante).

## Obtenir sa clé et lancer la commande soi-même

1. Le propriétaire (console : *Settings > API & agents*, ou l'admin : `notifuse-admin keys:mint <ws> --name ma-cle`)
   crée une clé API du workspace. Elle est affichée **une seule fois**. Clé en lecture seule :
   `notifuse-admin keys:mint <ws> --role readonly`.
2. Côté utilisateur :
   ```bash
   curl -fsSL https://notifuse.app.veridian.site/agent/notifuse | tar xz   # CLI distribuée (python3 seul)
   export NOTIFUSE_API_KEY='<la clé>' NOTIFUSE_WORKSPACE='<son workspace>'
   notifuse integrations:list
   notifuse integrations:cold --id <profil> --reply-forward-to client@client.fr
   notifuse integrations:verify --id <profil> --to moi@client.fr
   notifuse profiles:overview
   ```
   Le CLI refuse côté client tout `--workspace` différent de `NOTIFUSE_WORKSPACE`, n'emploie jamais le secret Hub et
   n'a pas de repli super-clé (`NOTIFUSE_OPERATOR=1` seulement, poste opérateur).
3. `notifuse-admin` reste capable de tout (jeton propriétaire dérivé, HMAC).

## Tests

- Go : `internal/service/veridian_integration_authz_test.go` (droits, lecture seule, clé étrangère, types
  réservés, non-re-liaison des secrets, parcours service Create/Update/Delete),
  `veridian_email_profile_verify_service_test.go` (isolation, lecture seule, plafond 5/min),
  `internal/http/veridian_email_profile_verify_handler_test.go` (403/401/400/429/502, pas de fuite d'erreur SMTP),
  `veridian_workspace_authz_test.go` (tâches).
- Mutations volontaires, chacune attrapée par un test rouge : retrait du contrôle `workspace:write` ; garde de
  re-liaison neutralisée ; vérification du transport AVANT l'authentification ; `tasks.list` retirée de la table.
- Python : `~/.claude/skills/notifuse-cli/tests/test_user_cli_parity.py` (11 cas : clé du workspace en Bearer, route
  JWT de vérification, HMAC conservé en admin, `status` sans plan, refus côté client des commandes réservées, partition
  admin/user). `bin/coverage_test.sh` : les marqueurs admin sont des noms exacts (préfixe seulement s'ils finissent par
  `/` ou `.`) et `ADMIN_MODE_ONLY_ROUTES` déclare les deux routes HMAC qu'une commande utilisateur n'emploie qu'en admin.
- CLI embarquée resynchronisée (`scripts/veridian/sync-agent-cli.sh`), MANIFEST recalculé, `cli-coverage-check.sh` vert.

## Tableau d'audit : toutes les commandes du CLI (généré depuis le CLI, avant / après)

Décisions : **a** utilisable avec la clé du workspace (déjà ouverte, ou serveur déjà ouvert et simplement non listée) ·
**b** refusée seulement à cause du jeton propriétaire ou HMAC alors que l'action concerne un seul workspace : ouverte par
ce lot · **c** réservée à l'admin plateforme · **d** reste propriétaire par choix restrictif.

| Commande | Route(s) API | Auth | CLI user avant | CLI user apres | Decision | Justification |
|---|---|---|---|---|---|---|
| `admin:attach-owner` | `/api/veridian/admin/attach-owner` | HMAC Hub | non | non | c reste admin | plateforme : plan, quotas, tenants, super-cles (HMAC Hub) |
| `admin:cache-invalidate` | `/api/veridian/admin/cache/invalidate` | HMAC Hub | non | non | c reste admin | plateforme : plan, quotas, tenants, super-cles (HMAC Hub) |
| `admin:cold-simulate` | `/api/veridian/admin/cold-simulate` | HMAC Hub | non | non | c reste admin | plateforme : plan, quotas, tenants, super-cles (HMAC Hub) |
| `admin:gc-orphans` | `/api/veridian/admin/gc-orphan-workspace-dbs` | HMAC Hub | non | non | c reste admin | plateforme : plan, quotas, tenants, super-cles (HMAC Hub) |
| `admin:grant-unlimited` | `/api/veridian/admin/grant-unlimited` | HMAC Hub | non | non | c reste admin | plateforme : plan, quotas, tenants, super-cles (HMAC Hub) |
| `admin:pricing-cache` | `/api/veridian/admin/pricing-cache` | HMAC Hub | non | non | c reste admin | plateforme : plan, quotas, tenants, super-cles (HMAC Hub) |
| `admin:stats` | `/api/veridian/admin/test-tenants-stats` | HMAC Hub | non | non | c reste admin | plateforme : plan, quotas, tenants, super-cles (HMAC Hub) |
| `admin:tenants` | `/api/veridian/admin/tenants` | HMAC Hub | non | non | c reste admin | plateforme : plan, quotas, tenants, super-cles (HMAC Hub) |
| `agent:exchange-token` | `/api/agent.exchangeToken` | JWT (membre/cle) | non | non | c reste admin | echange un jeton d'installation, sans cle |
| `agent:install-token` | `/api/workspaces.createAgentInstallToken` | JWT (membre/cle) | non | non | d reste proprietaire | cree une cle d'agent : proprietaire seulement (fiche 67) |
| `analytics:query` | `/api/analytics.query` | JWT (membre/cle) | oui | oui | a deja ouverte | route JWT du workspace, droit par ressource, isolation par membre |
| `analytics:schemas` | `/api/analytics.schemas` | JWT (membre/cle) | oui | oui | a deja ouverte | route JWT du workspace, droit par ressource, isolation par membre |
| `api` | (aucune route directe) | derive | non | non | c reste admin | porte de sortie generique (auth forcee) : l'autorisation reste celle du serveur |
| `automations:activate` | `/api/automations.activate` | JWT (membre/cle) | oui | oui | a deja ouverte | route JWT du workspace, droit par ressource, isolation par membre |
| `automations:create` | `/api/automations.create` | JWT (membre/cle) | oui | oui | a deja ouverte | route JWT du workspace, droit par ressource, isolation par membre |
| `automations:delete` | `/api/automations.delete` | JWT (membre/cle) | oui | oui | a deja ouverte | route JWT du workspace, droit par ressource, isolation par membre |
| `automations:enroll` | `/api/automations.enroll` | JWT (membre/cle) | oui | oui | a deja ouverte | route JWT du workspace, droit par ressource, isolation par membre |
| `automations:enrollments` | `/api/automations.enrollments`, `/api/automations.enrollments` | JWT (membre/cle) | oui | oui | a deja ouverte | route JWT du workspace, droit par ressource, isolation par membre |
| `automations:exit-contact` | `/api/automations.exitContact` | JWT (membre/cle) | oui | oui | a deja ouverte | route JWT du workspace, droit par ressource, isolation par membre |
| `automations:get` | `/api/automations.get` | JWT (membre/cle) | oui | oui | a deja ouverte | route JWT du workspace, droit par ressource, isolation par membre |
| `automations:list` | `/api/automations.list` | JWT (membre/cle) | oui | oui | a deja ouverte | route JWT du workspace, droit par ressource, isolation par membre |
| `automations:nodeExecutions` | `/api/automations.nodeExecutions` | JWT (membre/cle) | oui | oui | a deja ouverte | route JWT du workspace, droit par ressource, isolation par membre |
| `automations:pause` | `/api/automations.pause` | JWT (membre/cle) | oui | oui | a deja ouverte | route JWT du workspace, droit par ressource, isolation par membre |
| `automations:reset-contact` | `/api/automations.resetContact` | JWT (membre/cle) | oui | oui | a deja ouverte | route JWT du workspace, droit par ressource, isolation par membre |
| `automations:update` | `/api/automations.update` | JWT (membre/cle) | oui | oui | a deja ouverte | route JWT du workspace, droit par ressource, isolation par membre |
| `blog:categories-create` | `/api/blogCategories.create` | JWT (membre/cle) | non | oui | a listee (serveur deja ouvert) | route JWT du workspace, droit par ressource (service), isolation par membre |
| `blog:categories-delete` | `/api/blogCategories.delete` | JWT (membre/cle) | non | oui | a listee (serveur deja ouvert) | route JWT du workspace, droit par ressource (service), isolation par membre |
| `blog:categories-get` | `/api/blogCategories.get` | JWT (membre/cle) | non | oui | a listee (serveur deja ouvert) | route JWT du workspace, droit par ressource (service), isolation par membre |
| `blog:categories-list` | `/api/blogCategories.list` | JWT (membre/cle) | non | oui | a listee (serveur deja ouvert) | route JWT du workspace, droit par ressource (service), isolation par membre |
| `blog:categories-update` | `/api/blogCategories.update` | JWT (membre/cle) | non | oui | a listee (serveur deja ouvert) | route JWT du workspace, droit par ressource (service), isolation par membre |
| `blog:posts-create` | `/api/blogPosts.create` | JWT (membre/cle) | non | oui | a listee (serveur deja ouvert) | route JWT du workspace, droit par ressource (service), isolation par membre |
| `blog:posts-delete` | `/api/blogPosts.delete` | JWT (membre/cle) | non | oui | a listee (serveur deja ouvert) | route JWT du workspace, droit par ressource (service), isolation par membre |
| `blog:posts-get` | `/api/blogPosts.get` | JWT (membre/cle) | non | oui | a listee (serveur deja ouvert) | route JWT du workspace, droit par ressource (service), isolation par membre |
| `blog:posts-list` | `/api/blogPosts.list` | JWT (membre/cle) | non | oui | a listee (serveur deja ouvert) | route JWT du workspace, droit par ressource (service), isolation par membre |
| `blog:posts-publish` | `/api/blogPosts.publish` | JWT (membre/cle) | non | oui | a listee (serveur deja ouvert) | route JWT du workspace, droit par ressource (service), isolation par membre |
| `blog:posts-unpublish` | `/api/blogPosts.unpublish` | JWT (membre/cle) | non | oui | a listee (serveur deja ouvert) | route JWT du workspace, droit par ressource (service), isolation par membre |
| `blog:posts-update` | `/api/blogPosts.update` | JWT (membre/cle) | non | oui | a listee (serveur deja ouvert) | route JWT du workspace, droit par ressource (service), isolation par membre |
| `blog:themes-create` | `/api/blogThemes.create` | JWT (membre/cle) | non | oui | a listee (serveur deja ouvert) | route JWT du workspace, droit par ressource (service), isolation par membre |
| `blog:themes-get` | `/api/blogThemes.get` | JWT (membre/cle) | non | oui | a listee (serveur deja ouvert) | route JWT du workspace, droit par ressource (service), isolation par membre |
| `blog:themes-get-published` | `/api/blogThemes.getPublished` | JWT (membre/cle) | non | oui | a listee (serveur deja ouvert) | route JWT du workspace, droit par ressource (service), isolation par membre |
| `blog:themes-list` | `/api/blogThemes.list` | JWT (membre/cle) | non | oui | a listee (serveur deja ouvert) | route JWT du workspace, droit par ressource (service), isolation par membre |
| `blog:themes-publish` | `/api/blogThemes.publish` | JWT (membre/cle) | non | oui | a listee (serveur deja ouvert) | route JWT du workspace, droit par ressource (service), isolation par membre |
| `blog:themes-update` | `/api/blogThemes.update` | JWT (membre/cle) | non | oui | a listee (serveur deja ouvert) | route JWT du workspace, droit par ressource (service), isolation par membre |
| `breakdown` | `/api/veridian/contacts.providerBreakdown` | JWT (membre/cle) | non | oui | a listee (serveur deja ouvert) | lecture, 403 sur un autre workspace (mesure staging) |
| `broadcasts:cancel` | `/api/broadcasts.cancel` | JWT (membre/cle) | oui | oui | a deja ouverte | route JWT du workspace, droit par ressource, isolation par membre |
| `broadcasts:create` | `/api/broadcasts.create` | JWT (membre/cle) | oui | oui | a deja ouverte | route JWT du workspace, droit par ressource, isolation par membre |
| `broadcasts:delete` | `/api/broadcasts.delete` | JWT (membre/cle) | oui | oui | a deja ouverte | route JWT du workspace, droit par ressource, isolation par membre |
| `broadcasts:get` | `/api/broadcasts.get` | JWT (membre/cle) | oui | oui | a deja ouverte | route JWT du workspace, droit par ressource, isolation par membre |
| `broadcasts:getTestResults` | `/api/broadcasts.getTestResults` | JWT (membre/cle) | oui | oui | a deja ouverte | route JWT du workspace, droit par ressource, isolation par membre |
| `broadcasts:list` | `/api/broadcasts.list` | JWT (membre/cle) | oui | oui | a deja ouverte | route JWT du workspace, droit par ressource, isolation par membre |
| `broadcasts:pause` | `/api/broadcasts.pause` | JWT (membre/cle) | oui | oui | a deja ouverte | route JWT du workspace, droit par ressource, isolation par membre |
| `broadcasts:refreshGlobalFeed` | `/api/broadcasts.refreshGlobalFeed` | JWT (membre/cle) | oui | oui | a deja ouverte | route JWT du workspace, droit par ressource, isolation par membre |
| `broadcasts:resume` | `/api/broadcasts.resume` | JWT (membre/cle) | oui | oui | a deja ouverte | route JWT du workspace, droit par ressource, isolation par membre |
| `broadcasts:schedule` | `/api/broadcasts.schedule` | JWT (membre/cle) | oui | oui | a deja ouverte | route JWT du workspace, droit par ressource, isolation par membre |
| `broadcasts:selectWinner` | `/api/broadcasts.selectWinner` | JWT (membre/cle) | oui | oui | a deja ouverte | route JWT du workspace, droit par ressource, isolation par membre |
| `broadcasts:send-test` | `/api/broadcasts.sendToIndividual` | JWT (membre/cle) | oui | oui | a deja ouverte | route JWT du workspace, droit par ressource, isolation par membre |
| `broadcasts:testRecipientFeed` | `/api/broadcasts.testRecipientFeed` | JWT (membre/cle) | oui | oui | a deja ouverte | route JWT du workspace, droit par ressource, isolation par membre |
| `broadcasts:update` | `/api/broadcasts.update` | JWT (membre/cle) | oui | oui | a deja ouverte | route JWT du workspace, droit par ressource, isolation par membre |
| `campaign:audit` | (aucune route directe) | derive | non | oui | a listee (serveur deja ouvert) | lectures JWT seulement |
| `config` | (aucune route directe) | derive | oui | oui | a deja ouverte | route JWT du workspace, droit par ressource, isolation par membre |
| `contactLists:getByIDs` | `/api/contactLists.getByIDs` | JWT (membre/cle) | non | oui | a listee (serveur deja ouvert) | route JWT du workspace, droit par ressource (service), isolation par membre |
| `contactLists:getContactsByList` | `/api/contactLists.getContactsByList` | JWT (membre/cle) | non | oui | a listee (serveur deja ouvert) | route JWT du workspace, droit par ressource (service), isolation par membre |
| `contactLists:getListsByContact` | `/api/contactLists.getListsByContact` | JWT (membre/cle) | non | oui | a listee (serveur deja ouvert) | route JWT du workspace, droit par ressource (service), isolation par membre |
| `contactLists:removeContact` | `/api/contactLists.removeContact` | JWT (membre/cle) | non | oui | a listee (serveur deja ouvert) | route JWT du workspace, droit par ressource (service), isolation par membre |
| `contactLists:updateStatus` | `/api/contactLists.updateStatus` | JWT (membre/cle) | oui | oui | a deja ouverte | route JWT du workspace, droit par ressource, isolation par membre |
| `contacts:count` | `/api/contacts.count` | JWT (membre/cle) | oui | oui | a deja ouverte | route JWT du workspace, droit par ressource, isolation par membre |
| `contacts:delete` | `/api/contacts.delete` | JWT (membre/cle) | oui | oui | a deja ouverte | route JWT du workspace, droit par ressource, isolation par membre |
| `contacts:get` | `/api/contacts.getByEmail` | JWT (membre/cle) | oui | oui | a deja ouverte | route JWT du workspace, droit par ressource, isolation par membre |
| `contacts:get-by-external-id` | `/api/contacts.getByExternalID` | JWT (membre/cle) | oui | oui | a deja ouverte | route JWT du workspace, droit par ressource, isolation par membre |
| `contacts:import` | `/api/contacts.import` | JWT (membre/cle) | oui | oui | a deja ouverte | route JWT du workspace, droit par ressource, isolation par membre |
| `contacts:list` | `/api/contacts.list` | JWT (membre/cle) | oui | oui | a deja ouverte | route JWT du workspace, droit par ressource, isolation par membre |
| `contacts:upsert` | `/api/contacts.upsert` | JWT (membre/cle) | oui | oui | a deja ouverte | route JWT du workspace, droit par ressource, isolation par membre |
| `cron:run` | `/api/cron` | sans jeton | non | non | c reste admin | declenche le cron global (sans jeton) |
| `cron:status` | `/api/cron.status` | sans jeton | non | non | c reste admin | etat du cron global |
| `demo:reset` | `/api/demo.reset` | JWT (membre/cle) | non | non | c reste admin | environnement de demonstration |
| `doctor` | (aucune route directe) | HMAC ou IaC (pas de route unique) | non | non | c reste admin | autotest plateforme (HMAC, sante) |
| `dry-run` | (aucune route directe) | HMAC ou IaC (pas de route unique) | non | non | c reste admin | alias de verify |
| `env` | (aucune route directe) | derive | oui | oui | a deja ouverte | route JWT du workspace, droit par ressource, isolation par membre |
| `events:get` | `/api/customEvents.get` | JWT (membre/cle) | non | oui | a listee (serveur deja ouvert) | route JWT du workspace, droit par ressource (service), isolation par membre |
| `events:import` | `/api/customEvents.import` | JWT (membre/cle) | non | oui | a listee (serveur deja ouvert) | route JWT du workspace, droit par ressource (service), isolation par membre |
| `events:list` | `/api/customEvents.list` | JWT (membre/cle) | non | oui | a listee (serveur deja ouvert) | route JWT du workspace, droit par ressource (service), isolation par membre |
| `events:upsert` | `/api/customEvents.upsert` | JWT (membre/cle) | non | oui | a listee (serveur deja ouvert) | route JWT du workspace, droit par ressource (service), isolation par membre |
| `folders:create` | `/api/veridian/folders.create` | JWT (membre/cle) | oui | oui | a deja ouverte | route JWT du workspace, droit par ressource, isolation par membre |
| `folders:delete` | `/api/veridian/folders.delete` | JWT (membre/cle) | oui | oui | a deja ouverte | route JWT du workspace, droit par ressource, isolation par membre |
| `folders:list` | `/api/veridian/folders.list` | JWT (membre/cle) | oui | oui | a deja ouverte | route JWT du workspace, droit par ressource, isolation par membre |
| `folders:move` | `/api/veridian/folders.move` | JWT (membre/cle) | oui | oui | a deja ouverte | route JWT du workspace, droit par ressource, isolation par membre |
| `folders:rename` | `/api/veridian/folders.rename` | JWT (membre/cle) | oui | oui | a deja ouverte | route JWT du workspace, droit par ressource, isolation par membre |
| `folders:reorder` | `/api/veridian/folders.reorder` | JWT (membre/cle) | oui | oui | a deja ouverte | route JWT du workspace, droit par ressource, isolation par membre |
| `inbound-events:list` | `/api/inboundWebhookEvents.list` | JWT (membre/cle) | non | oui | a listee (serveur deja ouvert) | lecture, 403 sur un autre workspace (mesure staging) |
| `integrations:apply` | (aucune route directe) | derive | non | non | c reste admin | IaC multi-workspace (script du bastion) |
| `integrations:cold` | (aucune route directe) | derive | non | oui | b ouverte (serveur modifie) | modifie un profil SMTP du workspace (fiche 78 : redirection des reponses) |
| `integrations:create-firecrawl` | (aucune route directe) | derive | non | non | d reste proprietaire | type firecrawl : cle API tierce, proprietaire seulement |
| `integrations:create-imap` | (aucune route directe) | derive | non | oui | b ouverte (serveur modifie) | cree une boite IMAP du workspace |
| `integrations:create-llm` | (aucune route directe) | derive | non | non | d reste proprietaire | type llm : cle API tierce (cout), proprietaire seulement |
| `integrations:create-smtp` | `/api/workspaces.createIntegration` | JWT (membre/cle) | non | oui | b ouverte (serveur modifie) | cree un profil SMTP du workspace |
| `integrations:create-supabase` | (aucune route directe) | derive | non | non | d reste proprietaire | type supabase : cles de hook, proprietaire seulement |
| `integrations:delete` | `/api/workspaces.deleteIntegration` | JWT (membre/cle) | non | non | d reste proprietaire | suppression d'un profil : irreversible, reste au proprietaire (ouvrir moins) |
| `integrations:export` | `/api/veridian/admin/email-integration.export` | HMAC Hub | non | non | c reste admin | export des mots de passe SMTP/IMAP en clair : HMAC Hub seul (fiche 76), jamais une cle de workspace |
| `integrations:get` | (aucune route directe) | derive | non | oui | a listee (serveur deja ouvert) | lecture de workspaces.get (secrets caviardes) |
| `integrations:list` | `/api/workspaces.get` | JWT (membre/cle) | non | oui | a listee (serveur deja ouvert) | lecture de workspaces.get (secrets caviardes, fiche 10) |
| `integrations:plan` | (aucune route directe) | derive | non | non | c reste admin | IaC multi-workspace (script du bastion) |
| `integrations:test-provider` | `/api/email.testProvider` | JWT (membre/cle) | non | non | c reste admin | envoie un vrai mail avec des identifiants fournis dans le corps : laisse a l'admin (verify couvre le besoin) |
| `integrations:test-smtp` | `/api/settings.testSmtp` | root (jeton systeme) | non | non | c reste admin | route racine /api/settings.testSmtp (reglages systeme) |
| `integrations:update` | `/api/workspaces.updateIntegration` | JWT (membre/cle) | non | oui | b ouverte (serveur modifie) | SMTP et imap seulement ; secret jamais re-lie a une nouvelle destination sans etre ressaisi |
| `integrations:verify` | `/api/veridian/admin/email-integration.verify`, `/api/veridian/emailProfiles.verify` | HMAC Hub + JWT (membre/cle) | non | oui | b ouverte (serveur modifie) | route JWT dediee /api/veridian/emailProfiles.verify, 5 par minute et par workspace |
| `keys:list` | `/api/workspaces.listAPIKeys` | JWT (membre/cle) | non | oui | a listee (serveur deja ouvert) | lecture, e-mails techniques des cles, jamais un jeton |
| `keys:mint` | (aucune route directe) | derive | non | non | d reste proprietaire | idem, et surcouche admin (--role readonly via setUserPermissions) |
| `keys:provision` | `/api/workspaces.createAPIKey` | JWT (membre/cle) | non | non | d reste proprietaire | creer une cle = creer un acces : proprietaire (la cle ne se promeut pas elle-meme) |
| `keys:revoke` | `/api/workspaces.revokeAPIKey` | JWT (membre/cle) | non | non | d reste proprietaire | revoquer un acces : proprietaire (fiche 67) |
| `lists:create` | `/api/lists.create` | JWT (membre/cle) | oui | oui | a deja ouverte | route JWT du workspace, droit par ressource, isolation par membre |
| `lists:delete` | `/api/lists.delete` | JWT (membre/cle) | oui | oui | a deja ouverte | route JWT du workspace, droit par ressource, isolation par membre |
| `lists:get` | `/api/lists.get` | JWT (membre/cle) | oui | oui | a deja ouverte | route JWT du workspace, droit par ressource, isolation par membre |
| `lists:list` | `/api/lists.list` | JWT (membre/cle) | oui | oui | a deja ouverte | route JWT du workspace, droit par ressource, isolation par membre |
| `lists:move` | `/api/veridian/lists.move` | JWT (membre/cle) | oui | oui | a deja ouverte | route JWT du workspace, droit par ressource, isolation par membre |
| `lists:stats` | `/api/lists.stats` | JWT (membre/cle) | oui | oui | a deja ouverte | route JWT du workspace, droit par ressource, isolation par membre |
| `lists:subscribe` | `/api/lists.subscribe` | JWT (membre/cle) | oui | oui | a deja ouverte | route JWT du workspace, droit par ressource, isolation par membre |
| `lists:update` | `/api/lists.update` | JWT (membre/cle) | oui | oui | a deja ouverte | route JWT du workspace, droit par ressource, isolation par membre |
| `llm:chat` | `/api/llm.chat` | JWT (membre/cle) | non | non | c reste admin | flux SSE, appelle un fournisseur payant avec la cle du workspace : non ouvert dans ce lot |
| `logs:decisions` | `/api/veridian/decisions.list` | JWT (membre/cle) | oui | oui | a deja ouverte | route JWT du workspace, droit par ressource, isolation par membre |
| `magic-link` | `/api/sso/issue-magic-link` | HMAC Hub | non | non | c reste admin | HMAC SSO : connexion d'un autre utilisateur |
| `mail:attachment` | `/api/veridian/mail.messages.attachment` | JWT (membre/cle) | oui | oui | a deja ouverte | route JWT du workspace, droit par ressource, isolation par membre |
| `mail:boxes` | `/api/veridian/mail.mailboxes.list` | JWT (membre/cle) | oui | oui | a deja ouverte | route JWT du workspace, droit par ressource, isolation par membre |
| `mail:events` | `/api/veridian/mail.events` | JWT (membre/cle) | oui | oui | a deja ouverte | route JWT du workspace, droit par ressource, isolation par membre |
| `mail:get` | `/api/veridian/mail.messages.get` | JWT (membre/cle) | oui | oui | a deja ouverte | route JWT du workspace, droit par ressource, isolation par membre |
| `mail:list` | `/api/veridian/mail.messages.list` | JWT (membre/cle) | oui | oui | a deja ouverte | route JWT du workspace, droit par ressource, isolation par membre |
| `mail:raw` | `/api/veridian/mail.messages.raw` | JWT (membre/cle) | oui | oui | a deja ouverte | route JWT du workspace, droit par ressource, isolation par membre |
| `mail:reply` | `/api/veridian/mail.messages.reply` | JWT (membre/cle) | oui | oui | a deja ouverte | route JWT du workspace, droit par ressource, isolation par membre |
| `mail:send` | `/api/veridian/mail.messages.send` | JWT (membre/cle) | oui | oui | a deja ouverte | route JWT du workspace, droit par ressource, isolation par membre |
| `mail:thread` | `/api/veridian/mail.threads.get` | JWT (membre/cle) | oui | oui | a deja ouverte | route JWT du workspace, droit par ressource, isolation par membre |
| `mail:threads` | `/api/veridian/mail.threads.list` | JWT (membre/cle) | oui | oui | a deja ouverte | route JWT du workspace, droit par ressource, isolation par membre |
| `mail:update` | `/api/veridian/mail.messages.update` | JWT (membre/cle) | oui | oui | a deja ouverte | route JWT du workspace, droit par ressource, isolation par membre |
| `members:get-permissions` | (aucune route directe) | derive | non | oui | a listee (serveur deja ouvert) | lecture de workspaces.members |
| `members:invite` | `/api/workspaces.inviteMember` | JWT (membre/cle) | non | non | d reste proprietaire | equipe : proprietaire seulement (fiche 67) |
| `members:list` | `/api/workspaces.members` | JWT (membre/cle) | non | oui | a listee (serveur deja ouvert) | lecture, e-mails masques cote serveur |
| `members:permissions` | `/api/workspaces.setUserPermissions` | JWT (membre/cle) | non | non | d reste proprietaire | droits des membres : proprietaire seulement |
| `members:remove` | `/api/workspaces.removeMember` | JWT (membre/cle) | non | non | d reste proprietaire | equipe : proprietaire seulement |
| `messages:broadcastStats` | `/api/messages.broadcastStats` | JWT (membre/cle) | oui | oui | a deja ouverte | route JWT du workspace, droit par ressource, isolation par membre |
| `messages:list` | `/api/messages.list` | JWT (membre/cle) | oui | oui | a deja ouverte | route JWT du workspace, droit par ressource, isolation par membre |
| `policy:priority` | `/api/veridian/policy.priority.list`, `/api/veridian/policy.priority.set`, `/api/veridian/policy.priority.clear` | JWT (membre/cle) | oui | oui | a deja ouverte | route JWT du workspace, droit par ressource, isolation par membre |
| `policy:reset` | `/api/veridian/policy.reset` | JWT (membre/cle) | oui | oui | a deja ouverte | route JWT du workspace, droit par ressource, isolation par membre |
| `policy:set` | `/api/veridian/policy.set` | JWT (membre/cle) | oui | oui | a deja ouverte | route JWT du workspace, droit par ressource, isolation par membre |
| `policy:show` | `/api/veridian/policy.get` | JWT (membre/cle) | oui | oui | a deja ouverte | route JWT du workspace, droit par ressource, isolation par membre |
| `policy:suggest-enrollment` | `/api/veridian/policy.suggest-enrollment` | JWT (membre/cle) | oui | oui | a deja ouverte | route JWT du workspace, droit par ressource, isolation par membre |
| `profiles:create` | `/api/veridian/emailProfiles.create` | JWT (membre/cle) | oui | oui | b ouverte (serveur modifie) | profil SMTP + IMAP lies en une ecriture (etait listee mais 403 avec une cle : proprietaire exige) |
| `profiles:gaps` | `/api/veridian/emailProfiles.overview` | JWT (membre/cle) | oui | oui | a deja ouverte | route JWT du workspace, droit par ressource, isolation par membre |
| `profiles:link-imap` | `/api/workspaces.updateIntegration` | JWT (membre/cle) | non | oui | b ouverte (serveur modifie) | relie un profil a une boite IMAP du workspace (updateIntegration) |
| `profiles:overview` | `/api/veridian/emailProfiles.overview` | JWT (membre/cle) | oui | oui | a deja ouverte | route JWT du workspace, droit par ressource, isolation par membre |
| `profiles:pause` | `/api/veridian/emailProfiles.pause` | JWT (membre/cle) | oui | oui | a deja ouverte | route JWT du workspace, droit par ressource, isolation par membre |
| `profiles:resume` | `/api/veridian/emailProfiles.resume` | JWT (membre/cle) | oui | oui | a deja ouverte | route JWT du workspace, droit par ressource, isolation par membre |
| `profiles:set-usage` | `/api/veridian/emailProfiles.setUsage` | JWT (membre/cle) | oui | oui | a deja ouverte | route JWT du workspace, droit par ressource, isolation par membre |
| `prospection:stats` | `/api/veridian/prospection.stats` | JWT (membre/cle) | oui | oui | a deja ouverte | route JWT du workspace, droit par ressource, isolation par membre |
| `provider-webhooks:register` | `/api/webhooks.register` | JWT (membre/cle) | non | non | c reste admin | appelle l'API d'un fournisseur tiers avec les identifiants du profil : non ouvert dans ce lot |
| `provider-webhooks:status` | `/api/webhooks.status` | JWT (membre/cle) | non | non | c reste admin | idem provider-webhooks:register |
| `provision` | `/api/tenants/provision` | HMAC Hub | non | non | c reste admin | creation d'un tenant : plateforme |
| `queue:cancel` | `/api/veridian/queue.cancel`, `/api/veridian/queue.cancel` | JWT (membre/cle) | oui | oui | a deja ouverte | route JWT du workspace, droit par ressource, isolation par membre |
| `queue:entries` | `/api/veridian/queue.entries`, `/api/veridian/queue.entries` | JWT (membre/cle) | oui | oui | a deja ouverte | route JWT du workspace, droit par ressource, isolation par membre |
| `queue:explain` | `/api/veridian/queue.explain`, `/api/veridian/decisions.list` | JWT (membre/cle) | oui | oui | a deja ouverte | route JWT du workspace, droit par ressource, isolation par membre |
| `queue:recompute` | `/api/veridian/queue.recompute` | JWT (membre/cle) | oui | oui | a deja ouverte | route JWT du workspace, droit par ressource, isolation par membre |
| `reconcile:report` | `/api/veridian/reconcile.report` | JWT (membre/cle) | oui | oui | a deja ouverte | route JWT du workspace, droit par ressource, isolation par membre |
| `reconcile:resolve` | `/api/veridian/reconcile.resolve` | JWT (membre/cle) | oui | oui | a deja ouverte | route JWT du workspace, droit par ressource, isolation par membre |
| `reconcile:run` | `/api/veridian/reconcile.run` | JWT (membre/cle) | oui | oui | a deja ouverte | route JWT du workspace, droit par ressource, isolation par membre |
| `relay:info` | `/api/workspaces.listAPIKeys` | JWT (membre/cle) | non | oui | a listee (serveur deja ouvert) | lecture de listAPIKeys (hote, port, utilisateur ; jamais la cle) |
| `segments:contacts` | `/api/segments.contacts` | JWT (membre/cle) | oui | oui | a deja ouverte | route JWT du workspace, droit par ressource, isolation par membre |
| `segments:create` | `/api/segments.create` | JWT (membre/cle) | oui | oui | a deja ouverte | route JWT du workspace, droit par ressource, isolation par membre |
| `segments:delete` | `/api/segments.delete` | JWT (membre/cle) | oui | oui | a deja ouverte | route JWT du workspace, droit par ressource, isolation par membre |
| `segments:get` | `/api/segments.get` | JWT (membre/cle) | oui | oui | a deja ouverte | route JWT du workspace, droit par ressource, isolation par membre |
| `segments:list` | `/api/segments.list` | JWT (membre/cle) | oui | oui | a deja ouverte | route JWT du workspace, droit par ressource, isolation par membre |
| `segments:move` | `/api/veridian/segments.move` | JWT (membre/cle) | oui | oui | a deja ouverte | route JWT du workspace, droit par ressource, isolation par membre |
| `segments:preview` | `/api/segments.preview` | JWT (membre/cle) | oui | oui | a deja ouverte | route JWT du workspace, droit par ressource, isolation par membre |
| `segments:rebuild` | `/api/segments.rebuild` | JWT (membre/cle) | oui | oui | a deja ouverte | route JWT du workspace, droit par ressource, isolation par membre |
| `segments:update` | `/api/segments.update` | JWT (membre/cle) | oui | oui | a deja ouverte | route JWT du workspace, droit par ressource, isolation par membre |
| `session:logout` | `/api/user.logout` | JWT (membre/cle) | non | non | c reste admin | session humaine |
| `session:me` | `/api/user.me` | JWT (membre/cle) | non | non | c reste admin | session humaine |
| `session:signin` | `/api/user.signin` | JWT (membre/cle) | non | non | c reste admin | session humaine |
| `session:update-language` | `/api/user.updateLanguage` | JWT (membre/cle) | non | non | c reste admin | session humaine |
| `session:verify` | `/api/user.verify` | JWT (membre/cle) | non | non | c reste admin | session humaine |
| `settings:get` | (aucune route directe) | derive | non | oui | a listee (serveur deja ouvert) | lecture de workspaces.get (reglages) |
| `settings:set` | `/api/workspaces.update` | JWT (membre/cle) | non | non | d reste proprietaire | workspaces.update remplace tout le workspace (nom, domaine, fournisseurs par defaut) : proprietaire |
| `settings:update` | (aucune route directe) | derive | non | non | d reste proprietaire | idem settings:set |
| `sso:issue-magic-link` | `/api/sso/issue-magic-link` | HMAC Hub | non | non | c reste admin | HMAC SSO |
| `status` | `/api/tenants/{id}/status` | HMAC Hub | non | oui | a listee (serveur deja ouvert) | plan et facturation exclus du mode utilisateur (HMAC conserve en admin/operateur) |
| `stock:config` | `/api/veridian/stock.config`, `/api/veridian/stock.config.set` | JWT (membre/cle) | oui | oui | a deja ouverte | route JWT du workspace, droit par ressource, isolation par membre |
| `stock:overview` | `/api/veridian/stock.overview` | JWT (membre/cle) | oui | oui | a deja ouverte | route JWT du workspace, droit par ressource, isolation par membre |
| `system:settings-get` | `/api/settings.get` | root (jeton systeme) | non | non | c reste admin | reglages systeme (root) |
| `system:settings-update` | `/api/settings.update` | root (jeton systeme) | non | non | c reste admin | reglages systeme (root) |
| `system:setup-initialize` | `/api/setup.initialize` | root (jeton systeme) | non | non | c reste admin | installation systeme |
| `system:setup-status` | `/api/setup.status` | root (jeton systeme) | non | non | c reste admin | installation systeme |
| `system:setup-test-smtp` | `/api/setup.testSmtp` | root (jeton systeme) | non | non | c reste admin | installation systeme |
| `tasks:create` | `/api/tasks.create` | JWT (membre/cle) | non | non | c reste admin | plomberie systeme : seules tasks:list et tasks:get sont ouvertes |
| `tasks:delete` | `/api/tasks.delete` | JWT (membre/cle) | non | non | c reste admin | idem tasks:create |
| `tasks:execute` | `/api/tasks.execute` | sans jeton | non | non | c reste admin | execution de taches (jeton de tache), systeme |
| `tasks:get` | `/api/tasks.get` | JWT (membre/cle) | non | oui | a listee (serveur deja ouvert) | ouverte APRES fermeture de la faille tasks.* (voir constats) |
| `tasks:list` | `/api/tasks.list` | JWT (membre/cle) | non | oui | a listee (serveur deja ouvert) | ouverte APRES fermeture de la faille tasks.* (voir constats) |
| `tasks:reset` | `/api/tasks.reset` | JWT (membre/cle) | non | non | c reste admin | idem tasks:create |
| `tasks:trigger` | `/api/tasks.trigger` | JWT (membre/cle) | non | non | c reste admin | idem tasks:create |
| `template-blocks:create` | `/api/templateBlocks.create` | JWT (membre/cle) | non | oui | a listee (serveur deja ouvert) | route JWT du workspace, droit par ressource (service), isolation par membre |
| `template-blocks:delete` | `/api/templateBlocks.delete` | JWT (membre/cle) | non | oui | a listee (serveur deja ouvert) | route JWT du workspace, droit par ressource (service), isolation par membre |
| `template-blocks:get` | `/api/templateBlocks.get` | JWT (membre/cle) | non | oui | a listee (serveur deja ouvert) | route JWT du workspace, droit par ressource (service), isolation par membre |
| `template-blocks:list` | `/api/templateBlocks.list` | JWT (membre/cle) | non | oui | a listee (serveur deja ouvert) | route JWT du workspace, droit par ressource (service), isolation par membre |
| `template-blocks:update` | `/api/templateBlocks.update` | JWT (membre/cle) | non | oui | a listee (serveur deja ouvert) | route JWT du workspace, droit par ressource (service), isolation par membre |
| `templates:compile` | `/api/templates.compile` | JWT (membre/cle) | oui | oui | a deja ouverte | route JWT du workspace, droit par ressource, isolation par membre |
| `templates:create` | `/api/templates.create` | JWT (membre/cle) | oui | oui | a deja ouverte | route JWT du workspace, droit par ressource, isolation par membre |
| `templates:delete` | `/api/templates.delete` | JWT (membre/cle) | oui | oui | a deja ouverte | route JWT du workspace, droit par ressource, isolation par membre |
| `templates:get` | `/api/templates.get` | JWT (membre/cle) | oui | oui | a deja ouverte | route JWT du workspace, droit par ressource, isolation par membre |
| `templates:list` | `/api/templates.list` | JWT (membre/cle) | oui | oui | a deja ouverte | route JWT du workspace, droit par ressource, isolation par membre |
| `templates:move` | `/api/veridian/templates.move` | JWT (membre/cle) | oui | oui | a deja ouverte | route JWT du workspace, droit par ressource, isolation par membre |
| `templates:push` | (aucune route directe) | derive | oui | oui | a deja ouverte | route JWT du workspace, droit par ressource, isolation par membre |
| `templates:update` | `/api/templates.update` | JWT (membre/cle) | oui | oui | a deja ouverte | route JWT du workspace, droit par ressource, isolation par membre |
| `tenants:attach-member` | `/api/tenants/{tenantId}/attach-member` | HMAC Hub | non | non | c reste admin | plateforme : plan, quotas, tenants, super-cles (HMAC Hub) |
| `tenants:delete-one` | `/api/tenants/{id}` | HMAC Hub | non | non | c reste admin | plateforme : plan, quotas, tenants, super-cles (HMAC Hub) |
| `tenants:freeze-member` | `/api/tenants/{tenantId}/freeze-member` | HMAC Hub | non | non | c reste admin | plateforme : plan, quotas, tenants, super-cles (HMAC Hub) |
| `tenants:health` | `/api/tenants/{id}/health` | HMAC Hub | non | non | c reste admin | plateforme : plan, quotas, tenants, super-cles (HMAC Hub) |
| `tenants:limits` | `/api/tenants/{id}/limits` | HMAC Hub | non | non | c reste admin | plateforme : plan, quotas, tenants, super-cles (HMAC Hub) |
| `tenants:purge` | `/api/tenants/{id}/purge` | HMAC Hub | non | non | c reste admin | plateforme : plan, quotas, tenants, super-cles (HMAC Hub) |
| `tenants:remove-member` | `/api/tenants/{id}/remove-member` | HMAC Hub | non | non | c reste admin | plateforme : plan, quotas, tenants, super-cles (HMAC Hub) |
| `tenants:restore` | `/api/tenants/{id}/restore` | HMAC Hub | non | non | c reste admin | plateforme : plan, quotas, tenants, super-cles (HMAC Hub) |
| `tenants:restore-member` | `/api/tenants/{id}/restore-member` | HMAC Hub | non | non | c reste admin | plateforme : plan, quotas, tenants, super-cles (HMAC Hub) |
| `tenants:resume` | `/api/tenants/resume` | HMAC Hub | non | non | c reste admin | plateforme : plan, quotas, tenants, super-cles (HMAC Hub) |
| `tenants:rotate-api-key` | `/api/tenants/{id}/rotate-api-key` | HMAC Hub | non | non | c reste admin | plateforme : plan, quotas, tenants, super-cles (HMAC Hub) |
| `tenants:soft-delete` | `/api/tenants/{id}/soft-delete` | HMAC Hub | non | non | c reste admin | plateforme : plan, quotas, tenants, super-cles (HMAC Hub) |
| `tenants:suspend` | `/api/tenants/suspend` | HMAC Hub | non | non | c reste admin | plateforme : plan, quotas, tenants, super-cles (HMAC Hub) |
| `tenants:sync-member` | `/api/tenants/{id}/sync-member` | HMAC Hub | non | non | c reste admin | plateforme : plan, quotas, tenants, super-cles (HMAC Hub) |
| `tenants:touch` | `/api/tenants/{id}/touch` | HMAC Hub | non | non | c reste admin | plateforme : plan, quotas, tenants, super-cles (HMAC Hub) |
| `tenants:transfer-owner` | `/api/tenants/{id}/transfer-owner` | HMAC Hub | non | non | c reste admin | plateforme : plan, quotas, tenants, super-cles (HMAC Hub) |
| `tenants:unfreeze-member` | `/api/tenants/{tenantId}/unfreeze-member` | HMAC Hub | non | non | c reste admin | plateforme : plan, quotas, tenants, super-cles (HMAC Hub) |
| `tenants:update-plan` | `/api/tenants/update-plan` | HMAC Hub | non | non | c reste admin | plateforme : plan, quotas, tenants, super-cles (HMAC Hub) |
| `tenants:usage-summary` | `/api/tenants/{id}/usage-summary` | HMAC Hub | non | non | c reste admin | plateforme : plan, quotas, tenants, super-cles (HMAC Hub) |
| `timeline:list` | `/api/timeline.list` | JWT (membre/cle) | non | oui | a listee (serveur deja ouvert) | lecture, garde du middleware d'autorisation (fiche 67) |
| `transactional:create` | `/api/transactional.create` | JWT (membre/cle) | oui | oui | a deja ouverte | route JWT du workspace, droit par ressource, isolation par membre |
| `transactional:delete` | `/api/transactional.delete` | JWT (membre/cle) | oui | oui | a deja ouverte | route JWT du workspace, droit par ressource, isolation par membre |
| `transactional:get` | `/api/transactional.get` | JWT (membre/cle) | oui | oui | a deja ouverte | route JWT du workspace, droit par ressource, isolation par membre |
| `transactional:list` | `/api/transactional.list` | JWT (membre/cle) | oui | oui | a deja ouverte | route JWT du workspace, droit par ressource, isolation par membre |
| `transactional:send` | `/api/transactional.send` | JWT (membre/cle) | oui | oui | a deja ouverte | route JWT du workspace, droit par ressource, isolation par membre |
| `transactional:testTemplate` | `/api/transactional.testTemplate` | JWT (membre/cle) | oui | oui | a deja ouverte | route JWT du workspace, droit par ressource, isolation par membre |
| `transactional:update` | `/api/transactional.update` | JWT (membre/cle) | oui | oui | a deja ouverte | route JWT du workspace, droit par ressource, isolation par membre |
| `users:by-email` | `/api/users/by-email` | HMAC Hub | non | non | c reste admin | annuaire cross-tenant (HMAC) |
| `utils:detect-favicon` | `/api/detect-favicon` | sans jeton | non | non | c reste admin | utilitaire sans workspace |
| `veridian:deliverability-score` | `/api/veridian/templates.deliverabilityScore` | JWT (membre/cle) | non | oui | a listee (serveur deja ouvert) | lecture, 403 sur un autre workspace (mesure staging) |
| `veridian:email-profile-usage` | `/api/veridian/emailProfiles.usage` | JWT (membre/cle) | non | oui | a listee (serveur deja ouvert) | lecture, 403 sur un autre workspace (mesure staging) |
| `veridian:engagement-by-class` | `/api/veridian/messages.engagementByClass` | JWT (membre/cle) | non | oui | a listee (serveur deja ouvert) | lecture, 403 sur un autre workspace (mesure staging) |
| `veridian:hub-discovery-me` | `/api/veridian/hub-discovery/me` | JWT (membre/cle) | non | non | c reste admin | identite Hub de l'appelant, pas une action de workspace |
| `veridian:invite-member` | `/api/veridian/workspaces.inviteMember` | JWT (membre/cle) | non | non | d reste proprietaire | equipe : proprietaire seulement (fiche 67) |
| `veridian:mode` | `/api/veridian/mode` | sans jeton | non | non | c reste admin | reglage de mode global |
| `veridian:reply-stats` | `/api/veridian/messages.replyStats` | JWT (membre/cle) | oui | oui | a deja ouverte | route JWT du workspace, droit par ressource, isolation par membre |
| `veridian:reputation-status` | `/api/veridian/messages.reputationStatus` | JWT (membre/cle) | oui | oui | a deja ouverte | route JWT du workspace, droit par ressource, isolation par membre |
| `veridian:workspaces-attach-member` | `/api/veridian/workspaces/{tenantId}/attach-member` | JWT (membre/cle) | non | non | c reste admin | HMAC Hub |
| `verify` | (aucune route directe) | HMAC ou IaC (pas de route unique) | non | non | c reste admin | cold-simulate : staging seulement, HMAC admin |
| `version` | `/api/version` | sans jeton | non | non | c reste admin | sans workspace, sans jeton |
| `webhooks:create` | `/api/webhookSubscriptions.create` | JWT (membre/cle) | oui | oui | d reste proprietaire | webhook sortant (secret, exfiltration d'evenements) : proprietaire seulement depuis la fiche 67 ; commande listee mais 403 avec une cle |
| `webhooks:delete` | `/api/webhookSubscriptions.delete` | JWT (membre/cle) | oui | oui | d reste proprietaire | idem webhooks:create |
| `webhooks:deliveries` | `/api/webhookSubscriptions.deliveries` | JWT (membre/cle) | oui | oui | a deja ouverte | route JWT du workspace, droit par ressource, isolation par membre |
| `webhooks:eventTypes` | `/api/webhookSubscriptions.eventTypes` | JWT (membre/cle) | oui | oui | a deja ouverte | route JWT du workspace, droit par ressource, isolation par membre |
| `webhooks:get` | `/api/webhookSubscriptions.get` | JWT (membre/cle) | oui | oui | a deja ouverte | route JWT du workspace, droit par ressource, isolation par membre |
| `webhooks:list` | `/api/webhookSubscriptions.list` | JWT (membre/cle) | oui | oui | a deja ouverte | route JWT du workspace, droit par ressource, isolation par membre |
| `webhooks:regenerateSecret` | `/api/webhookSubscriptions.regenerateSecret` | JWT (membre/cle) | oui | oui | d reste proprietaire | idem webhooks:create |
| `webhooks:test` | `/api/webhookSubscriptions.test` | JWT (membre/cle) | oui | oui | d reste proprietaire | idem webhooks:create |
| `webhooks:toggle` | `/api/webhookSubscriptions.toggle` | JWT (membre/cle) | oui | oui | d reste proprietaire | idem webhooks:create |
| `webhooks:update` | `/api/webhookSubscriptions.update` | JWT (membre/cle) | oui | oui | d reste proprietaire | idem webhooks:create |
| `wipe` | `/api/veridian/admin/wipe-test-tenants` | HMAC Hub | non | non | c reste admin | suppression de tenants de test : plateforme |
| `workspaces:accept-invitation` | `/api/workspaces.acceptInvitation` | JWT (membre/cle) | non | non | c reste admin | flux d'invitation, utilisateur humain (pas une cle) |
| `workspaces:create` | `/api/workspaces.create` | JWT (membre/cle) | non | non | c reste admin | creer un workspace : plan et quotas (facturation) |
| `workspaces:delete` | `/api/workspaces.delete` | JWT (membre/cle) | non | non | d reste proprietaire | suppression du workspace : proprietaire, jamais une cle |
| `workspaces:delete-invitation` | `/api/workspaces.deleteInvitation` | JWT (membre/cle) | non | non | d reste proprietaire | equipe : proprietaire seulement |
| `workspaces:list` | `/api/workspaces.list` | JWT (membre/cle) | non | non | c reste admin | liste de TOUS les workspaces d'un utilisateur : cross-workspace |
| `workspaces:verify-invitation` | `/api/workspaces.verifyInvitationToken` | JWT (membre/cle) | non | non | c reste admin | flux d'invitation (jeton) |
