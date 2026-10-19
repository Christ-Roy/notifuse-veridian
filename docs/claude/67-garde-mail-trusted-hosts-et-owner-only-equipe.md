# 67 — Liste blanche IMAP/SMTP (NOTIFUSE_TRUSTED_INTERNAL_HOSTS), owner-only sur l'équipe, vérification admin d'un profil (2026-10-19)

## 1. Pourquoi 100.108.136.89 était refusée et 100.92.215.42 / 100.88.202.29 non

`pkg/netguard` refuse tout le CGNAT 100.64.0.0/10 (plage Tailscale) et les ULA IPv6 (fd7a:...).
Les deux IP qui passaient sont dans `internalRelays` (exception EXACTE ip:port, IMAP 993, fiche 64) ;
le bastion n'y était pas. Aucune liste de refus propre au plan de contrôle : simple absence d'exception.

## 2. `NOTIFUSE_TRUSTED_INTERNAL_HOSTS`

Liste d'IP et/ou CIDR séparés par des virgules, tous ports, vide par défaut (fail-closed). Lue par
`netguard.DefaultAllowlist()`, utilisée UNIQUEMENT par SMTP et IMAP : à l'enregistrement
(`ValidateHost`) ET à chaque connexion (`GuardDialer`/`Control`, anti DNS-rebinding, même liste).
Webhooks, nœud webhook, SparkPost : inchangés (`IsBlockedIP` sans allowlist). Une entrée invalide
fait ignorer TOUTE la liste. Prod : `100.108.136.89` dans `deploy/notifuse.nomad.hcl`.
Code : `pkg/netguard/netguard.go` (fichier Veridian, pas de patch upstream).

## 3. Owner-only : inviteMember et deleteInvitation

Constat : une clé API (rôle `member`, FullPermissions, `Type` parfois vide au chargement) invitait
via `/api/workspaces.inviteMember` et `/api/veridian/workspaces.inviteMember` (HTTP 200).
Correctif : `veridianRequireOwner` (`internal/service/veridian_owner_guard.go`) = rôle `owner` ET type
non api_key, sinon `ErrUnauthorized` (403). Appelé dans `WorkspaceService.InviteMember` et
`DeleteInvitation` (PATCH MINIMAL de `workspace_service.go`, upstream : 2 appels, à rejouer au sync)
et dans `veridian_invite_member_handler.go` (rôle lu sur le `UserWorkspace` retourné par l'auth).
Le chemin Hub -> Notifuse (`attach-member`, provision, HMAC) ne passe pas par là.

### 3bis. La classe : routes de gestion ouvertes à une clé (relevé complet du routeur, 19/10)

Corrigées (owner humain seul, 403 sinon) :
- `workspaces.inviteMember`, `workspaces.deleteInvitation`, `veridian/workspaces.inviteMember` (3)
- `workspaces.revokeAPIKey`, `workspaces.createAgentInstallToken` (avant : owner OU écriture sur la ressource workspace, que toute clé API possède)
- `webhookSubscriptions.{create,update,delete,toggle,regenerateSecret,test}` (6)

TROU PLUS GRAVE trouvé en relevant : `webhookSubscriptions.*`, `segments.*` et `timeline.list` n'avaient AUCUN
contrôle d'appartenance au workspace (le service stocke `authService` sans jamais l'appeler, upstream compris :
`git show origin/main` = 0 appel). Tout JWT valide d'un autre workspace pouvait lire/créer/supprimer des abonnements
webhook et lire/reconstruire des segments d'un tenant tiers. Fermé par UN middleware devant le mux
(`veridian_workspace_authz.go`, table `VeridianWorkspaceAuthzRoutes` = liste qui fait foi) : membre du workspace pour
les lectures et `segments.*`, owner pour la gestion des webhooks. Un test vérifie que chaque route de la table est
montée (un renommage amont rendrait la garde muette).

Déjà owner-only avant : createAPIKey, removeMember, setUserPermissions, workspaces.update/delete,
create/update/deleteIntegration, emailProfiles création. Volontairement laissées à une clé en écriture :
`emailProfiles.setUsage/pause/resume` (écriture workspace), listes, contacts, templates, automations, broadcasts.
Lecture seule : `listAPIKeys`, `members` (e-mails masqués).
`settings.*` : réservé au root. `webhooks.register/status` (webhooks des fournisseurs) : authentifiés, membre.

`workspaces.generateMagicLink` : restreint aux clés gérées par le Hub (`users.veridian_managed`), 403 sinon (fail-closed).
Avant, toute clé member pouvait obtenir le lien de connexion du owner. Conséquence : `notifuse-admin magic-link`, qui
mint une clé éphémère NON gérée, reçoit 403 ; utiliser `sso:issue-magic-link` (HMAC) ou `hub open`.

Conséquence CLI : `notifuse webhooks:create|update|delete|toggle|regenerate|test` avec une clé scopée member rend
maintenant 403 (owner / notifuse-admin seulement). Lectures inchangées.

## 4. Vérification admin d'un profil d'envoi

`POST /api/veridian/admin/email-integration.verify` {workspace_id, integration_id, to} (HMAC Hub +
idempotency, jamais JWT). `EmailService.VeridianAdminVerifyEmailIntegration` fait le vrai test de
transport vers `to` puis pose `VeridianTransportVerifiedAt` seulement si le transport accepte. Échec :
502 générique, NON vérifié, aucun secret. CLI : `notifuse-admin integrations:verify <ws> --id I --to E`.

## 5. Console : mode « Texte » des modèles (19/10)

`console/src/components/templates/CreateTemplateDrawer.tsx` : 3e mode d'éditeur « Text » (Visual / Code MJML / Text).
Un modèle `plain_text_only` s'ouvre en mode Text (textarea, variables Liquid), un Save envoie `plain_text_only=true`
et `text` à jour (avant : le drawer ne portait ni l'un ni l'autre, donc chaque Save basculait le modèle en HTML).
Un corps vide ou un tag Liquid invalide est refusé côté client. L'arbre visuel envoyé reste un arbre valide (le serveur
l'exige) mais n'est jamais émis. Test : `console/e2e/features/template-text-mode.spec.ts` (3 cas, vu échouer sur
l'ancien drawer). Les e2e du dépôt mockent l'API ; la relecture réelle par API reste à faire après déploiement.
