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

Routes sœurs vues et NON corrigées (volontairement) : `workspaces.revokeAPIKey` et
`workspaces.createAgentInstallToken` (owner OU « admin » = écriture sur la ressource workspace, or une
clé API a FullPermissions : une clé peut donc révoquer des clés / en frapper une nouvelle) ;
`workspaces.listAPIKeys` et `workspaces.members` (lecture, e-mails masqués pour les clés).
Déjà owner-only : createAPIKey, removeMember, setUserPermissions, update/delete workspace,
create/update/deleteIntegration.

## 4. Vérification admin d'un profil d'envoi

`POST /api/veridian/admin/email-integration.verify` {workspace_id, integration_id, to} (HMAC Hub +
idempotency, jamais JWT). `EmailService.VeridianAdminVerifyEmailIntegration` fait le vrai test de
transport vers `to` puis pose `VeridianTransportVerifiedAt` seulement si le transport accepte. Échec :
502 générique, NON vérifié, aucun secret. CLI : `notifuse-admin integrations:verify <ws> --id I --to E`.
