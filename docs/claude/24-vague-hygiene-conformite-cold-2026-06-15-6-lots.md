# Vague hygiène/conformité cold (2026-06-15) — 6 lots

Salve de 6 lots (team-orchestration) sur le backlog cold + sécu + admin :

- **Masquage secrets en sortie JSON** (🔴 sécu, historique) : les MarshalJSON IMAP
  et SMTP protègent le clair pendant la persistance. Depuis le hardening du
  2026-08-05, la couche HTTP workspace retire aussi tous les ciphertexts et les
  autres familles de credentials avant réponse API ; voir la section dédiée plus
  haut. Fichiers Veridian : `internal/http/veridian_workspace_redaction.go`,
  `internal/domain/veridian_imap_integration.go` et
  `internal/domain/email_provider_smtp.go`.
- **Conformité mail cold** : multipart `text/plain + text/html` (au lieu de HTML-only) +
  retrait du header `X-Message-ID` sur le chemin SMTP. Helper `internal/service/veridian_html_to_text.go`
  (basé `golang.org/x/net/html`, best-effort → fallback HTML-only).
- **Cold no-unsubscribe** : `internal/domain/veridian_unsubscribe.go`
  (`VeridianSuppressUnsubscribe`, délègue à `VeridianIsColdContext`) → en contexte
  tunnel, `oneclick_unsubscribe_url` non propagé (ni header List-Unsubscribe ni footer).
- **Linter délivrabilité** : `pkg/veridian_deliverability` (`Score(input) Result` pur,
  ~30 règles SA-public, modes strict/lenient par classe) + endpoint
  `POST/GET /api/veridian/templates.deliverabilityScore` (JWT console, `templates:read`).
- **Listing admin** : `VeridianPlanRepository.ListAllIDs` → bucket `managed` cohérent
  sans prefix (bug `collectPlanIDs(prefix=="")` → nil).
- **OpenAPI** : `transactional.create/update/delete` documentés (doc-only).

⚠️ **Diffs INLINE supplémentaires** (vague cold 2026-06-15) :

| Fichier upstream | Diff Veridian |
|---|---|
| `internal/service/smtp_service.go` | (1) **supprimé** `SetGenHeader("X-Message-ID", ...)` (chemin SMTP/cold ; SES garde le sien) ; (2) HTML-only → si `veridianHTMLToText(Content) != ""` : `SetBodyString(TypeTextPlain, plain)` + `AddAlternativeString(TypeTextHTML, Content)` (multipart/alternative), sinon fallback HTML-only |
| `internal/service/broadcast/queue_message_sender.go` | `buildQueueEntry` : gate `VeridianSuppressUnsubscribe` avant de propager `oneclick_unsubscribe_url` (workspace mémoïsé via pixelResolver) |
| `internal/service/broadcast/message_sender.go` | `SendToRecipient` : idem gate `VeridianSuppressUnsubscribe` |
| `internal/domain/veridian.go` | interface `VeridianPlanRepository` += `ListAllIDs(ctx, limit)` |
| `internal/app/app.go` | +câblage endpoint `templates.deliverabilityScore` (handler+service veridian) |


## Désinscription en un clic (RFC 8058), réparée le 26/11/2026
Mesuré en prod par le parcours Mail de bout en bout : le POST sur l'URL de l'en-tête `List-Unsubscribe` rendait 400, donc aucune
désinscription en un clic n'aboutissait (exigence Gmail et Yahoo pour les envois en nombre). Deux causes : l'URL ne portait pas
`email_hmac` (que le service exige), et le handler upstream ne lit que du JSON alors qu'un client mail envoie le formulaire
`List-Unsubscribe=One-Click`. Correctif : `email_hmac` posé dans l'URL (`domain/template.go`) et `VeridianOneClickUnsubscribe`
(`internal/http/middleware/`) qui convertit le formulaire en la requête JSON du handler, vérification HMAC inchangée. Les mails
partis avant le 26/11 portent une URL sans HMAC : leur lien un-clic reste sans effet (le lien du pied de mail, lui, marche).
