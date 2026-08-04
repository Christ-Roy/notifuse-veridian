# Gmail API OAuth transport

## But

Permettre aux intégrations SMTP OAuth2 Google existantes d'envoyer avec le scope minimal `gmail.send`. Google refuse ce scope sur SMTP XOAUTH2 et exige l'API Gmail pour un usage limité à l'envoi.

## Implémentation

1. Ajouter un transport Veridian dédié qui reçoit le MIME déjà composé par Notifuse et appelle `users.messages.send`.
2. Router uniquement `auth_type=oauth2` et `oauth2_provider=google` vers ce transport. Tous les autres SMTP restent inchangés.
3. Rafraîchir une fois le jeton après un 401, puis échouer proprement sans exposer de secret.
4. Couvrir la sélection, l'encodage base64url, le succès, les erreurs Google et le retry 401.

## Validation

- `go test ./internal/service -run 'TestVeridianGmailAPI|TestSMTPService'`
- `go test ./internal/service/...`
- E2E staging avec un compte Google de test et une boîte sink contrôlée avant promotion prod.
