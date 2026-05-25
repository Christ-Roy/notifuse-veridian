# [NOTIFUSE] Refactor EmailService pour brancher hub_mail_gateway

> **Sévérité** : 🟡 P2 — feature mail-gateway consumer-side livrée vague 6 mais sans refactor des call sites broadcasts
> **Owner** : agent Notifuse
> **Créé** : 2026-05-26 par team-lead vague 6 (reco d'agent-mail-gateway-lib)
> **Dépend de** :
> - Vague 6 livrée : lib `pkg/hub_mail_gateway/client.go` (commit 5c893d85)
> - Vague 6 livrée : migration V48 `workspaces.mail_provider_choice` (commit à venir agent-mail-pref-migration)
> - Vague 6 livrée : UI `/settings/mail-account` (commit 0f28012f)

## Contexte

Vague 6 a livré la **lib** Hub Mail Gateway + la **migration** V48 + l'**UI settings**, mais le ticket parent `2026-05-25-mail-send-as-user-via-hub-gateway.md` §3.3 demandait aussi le **refactor des call sites envois transactionnels Notifuse** pour utiliser cette nouvelle lib.

**Découverte d'agent-mail-gateway-lib** (vague 6) :

> "Le code Notifuse n'a AUCUN envoi transactionnel interne simple à refactorer : magic_links/invitations sont délégués au Hub. `EmailService.SendEmail` est utilisé pour broadcasts/transactionnels USER-FACING (pas system mails). Refactorer EmailService demande d'ajouter `EmailProviderKindHubGateway` au switch `getProviderService` + propagation de UserID/IdempotencyKey jusque-là — touche plein de call sites broadcasts."

Le refactor est plus invasif que prévu. Demande un ticket dédié post-V48.

## Spec

### 1. Étendre EmailService

`internal/service/email_service.go` (upstream Notifuse, **ne pas patcher directement** — créer un wrapper Veridian) :
- Ajouter `EmailProviderKindHubGateway` à l'enum `EmailProviderKind`
- Dans le switch `getProviderService(kind)`, brancher vers un nouveau provider `veridian_hub_gateway_provider.go` qui utilise `pkg/hub_mail_gateway/client.go`

### 2. Provider Hub Gateway

`internal/service/veridian_hub_gateway_provider.go` :
- Implémente `EmailProviderService` interface upstream
- `SendEmail(ctx, params)` :
  - Lit `workspace.MailProviderChoice` via repo
  - Si `smtp_generic` → délègue au provider SMTP upstream
  - Si `hub_gmail` → check `user.HubUserID != nil`, call `pkg/hub_mail_gateway/client.SendMailAsUser(...)` avec userId mappé
  - Si `provider_not_linked` retourné → fallback automatique SMTP générique + log warn
  - Si `needs_reauth` → log error + fallback + signal UI via webhook ou DB flag pour afficher banner "Reconnecte Gmail"

### 3. Propagation IdempotencyKey

Les call sites broadcasts upstream ne passent pas d'idempotency_key. Le Hub Mail Gateway en exige un (UUID v4) pour anti-double-envoi.

Options :
- (A) Générer un UUID v4 dans le provider Hub Gateway à chaque appel (perd l'idempotence si l'EmailService retry)
- (B) Si message_history existe pour ce send, réutiliser son ID comme idempotency_key (préserve l'idempotence cross-retry)
- (C) Demander upstream Notifuse d'ajouter idempotency_key au struct EmailParams (impact large, sync upstream future)

Reco : **(B)** — réutiliser ID message_history pour stabilité idempotence.

### 4. Tests

- Tests colocalisés `veridian_hub_gateway_provider_test.go` :
  - Switch smtp_generic vs hub_gmail selon workspace.MailProviderChoice
  - Fallback SMTP si provider_not_linked
  - Préservation idempotency_key cross-retry
- Spec E2E `tests/e2e-veridian/specs/email-via-hub-gateway.spec.ts` :
  - Provision tenant + user + Hub user simulé
  - Set workspace mail_provider_choice=hub_gmail
  - Trigger broadcast send via API
  - Vérifier que Hub reçoit l'appel (mock ou observation HTTP logs côté hub.staging)
  - Set hub_gmail + user pas connecté → fallback SMTP attendu

### 5. Migration UX user

Quand un user choisit `hub_gmail` côté UI mais que Hub retourne `needs_reauth` ou `provider_not_linked` runtime, afficher un banner d'erreur sur le dashboard la prochaine fois qu'il se logge. (Ticket UI dédié vague 7+.)

## DoD

- [ ] Provider `veridian_hub_gateway_provider.go` créé + tests colocalisés stricts
- [ ] Switch EmailService bascule sur workspace.MailProviderChoice
- [ ] Fallback SMTP générique automatique si provider_not_linked/needs_reauth
- [ ] Idempotency_key dérivé de message_history.ID
- [ ] Spec E2E end-to-end verte sur staging
- [ ] CI verte + auto-promote prod
- [ ] Ticket archivé done/ avec récap

## Pourquoi pas dans la vague 6

- Vague 6 a livré l'infrastructure (lib + migration + UI). Refactor §3.3 = travail Go upstream-adjacent qui demande une session dédiée pour couvrir tous les call sites broadcasts sans régression.
- L'UI vague 6 marche déjà : user pick hub_gmail → DB persistée → lib disponible pour futurs envois system Hub-driven directs sans passer par EmailService (cas magic_link cross-app).
- **Pas de régression vague 6** : si user pick hub_gmail aujourd'hui, l'EmailService continue de router vers SMTP générique (default), donc rien ne casse.

## Référence

- Lib : `pkg/hub_mail_gateway/client.go` (commit 5c893d85)
- Migration V48 : `internal/migrations/v48.go` (commit agent-mail-pref-migration vague 6)
- UI : `console/src/components/settings/veridian_mail_account_settings.tsx` (commit 0f28012f)
- Route Hub : `POST https://app.veridian.site/api/mail/send-as-user` (livré Hub staging vérifié 2026-05-25)
- Contrat HMAC : matrice HMAC v3 dans CLAUDE.md Notifuse
