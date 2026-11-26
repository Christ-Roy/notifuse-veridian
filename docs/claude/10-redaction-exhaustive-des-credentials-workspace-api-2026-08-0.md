# Redaction exhaustive des credentials workspace API (2026-08-05)

Toutes les réponses qui embarquent un `Workspace` passent par
`veridianRedactWorkspaceForAPI`. Le helper clone l'objet sans muter l'état runtime
puis retire le secret workspace chiffré, les clés FileManager claires/chiffrées,
les passwords IMAP clairs/chiffrés, les signatures Supabase claires/chiffrées,
les clés LLM/Firecrawl claires/chiffrées et tous les secrets EmailProvider.

- Les seuls états non sensibles ajoutés sont `file_manager.has_secret_key`, les
  `has_signature_key` des deux hooks Supabase et les flags email déjà existants.
- Les mises à jour avec un champ secret vide préservent le ciphertext stocké ;
  les flags de réponse sont nettoyés avant persistance.
- Le FileManager historique parle directement à S3 depuis le navigateur. Il ne
  peut donc plus réutiliser une clé existante après redaction. Aucun workspace
  PROD n'avait de FileManager configuré lors de la vérification live préalable ; la
  remise en service future exige un proxy S3 backend (dette P1), pas le retour du
  secret dans l'API.


## La route publique d'invitation (26/11/2026, chasse sécu lot C)

`POST /api/workspaces.verifyInvitationToken` (sans authentification) rendait le Workspace entier, sans redaction : clés LLM,
Firecrawl, fournisseurs d'envoi, secret du stockage de fichiers, à tout porteur d'un jeton d'invitation. Elle ne rend plus
que `{id, name, settings.logo_url}` (`veridianInvitationWorkspace`). Les deux tests upstream de cette route étaient
neutralisés par `t.Skip` : réactivés (`TestVerifyInvitationTokenRedactsCredentials`, garde du jeu de test).

## ehlo_hostname validé (26/11/2026, chasse sécu lot C)
`ehlo_hostname` d'une intégration SMTP partait tel quel dans `EHLO %s\r\n` : un CRLF injectait des commandes SMTP vers le
serveur du profil. `EmailProvider.Validate` appelle `ValidateVeridianEHLOHostname` (`internal/domain/veridian_ehlo_hostname.go`) :
nom d'hôte ou littéral d'adresse entre crochets, sinon refus à l'écriture.
