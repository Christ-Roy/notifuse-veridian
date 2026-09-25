# Events comportementaux cold↔web — `email.opened/clicked/replied` → Hub (2026-06-17)

Câble l'émission des events comportementaux que le réconciliateur de scoring prospect
du Hub (livré prod, `ingestProspectEvent`) ATTENDAIT sans jamais les recevoir → backend
Hub orphelin (0 row `prospect_events`/`prospect_scores`). La donnée open/click/reply
existait en interne Notifuse mais n'était jamais `.Emit()`. Spec : ticket
`todo/2026-06-17-emettre-events-comportementaux-email-opened-clicked-replied-hub.md`.

- **RÉUTILISE le `VeridianWebhookEmitter` existant** (voie legacy HMAC consommée par le
  Hub `app/api/webhooks/notifuse/route.ts:dispatchLegacyEvent`) — PAS de nouveau canal.
  Best-effort de bout en bout (Emit part en goroutine, retry 3x) → ne bloque JAMAIS le
  pixel d'ouverture / la redirection de clic / la détection de réponse.
- **3 events** (constantes `internal/domain/veridian.go`) : `EventEmailOpened` /
  `EventEmailClicked` / `EventEmailReplied`. `tenant_id` (param Emit) = workspaceID
  Notifuse = `notifuseWorkspaceSlug` côté Hub ; `event_id` (UUID auto par Emit) =
  idempotency_key applicative (replay ne ré-incrémente pas le score).
- **Payload `data`** (lu par le Hub, CONTRAT-HUB §7.5.1/§7.5.2) : `contact_email`
  (✅ CLÉ DE JOINTURE V1 — résolu via `messageRepo.FindContactEmailByMessageID`,
  normalisé), `message_id`, `occurred_at` (RFC3339 UTC), + `match_type` (reply).
  `vid` = étage 2 (ticket vid séparé, BLOQUÉ Hub) → slot prêt, non posé.
- **Points d'émission** : open/click depuis `EmailService.OpenEmail`/`VisitLink` (la
  donnée y est, l'anti-bot du handler `email_handler.go` gate déjà : on n'émet que sur
  `shouldRecord==true`, pas de pollution bot) ; reply depuis
  `VeridianReplyService.ProcessInboundMessage` (après `MarkReplied`, fast-path idempotent).
- **DI optionnelle nil-safe** (`SetVeridianWebhookEmitter`, câblée dans `app.go` après
  création de l'emitter) : emitter noop/absent = aucune émission, comportement upstream
  strictement inchangé (Notifuse self-hosted / Hub non configuré).
- **Fichier veridian** : `internal/service/veridian_behavioral_emit.go`
  (`veridianEmitBehavioral` : lookup contact_email + Emit, best-effort) + tests
  colocalisés. Reply : helper `veridianEmitReplied` dans `veridian_reply_service.go`.

⚠️ **Diffs INLINE supplémentaires** (events comportementaux) :

| Fichier upstream | Diff Veridian |
|---|---|
| `internal/domain/veridian.go` | +3 constantes `VeridianEvent` : `EventEmailOpened`/`EventEmailClicked`/`EventEmailReplied` |
| `internal/service/email_service.go` | +champ `veridianWebhookEmitter` (DI optionnelle) ; `OpenEmail`/`VisitLink` appellent `veridianEmitBehavioral` après `SetOpened`/`SetClicked` réussis |
| `internal/app/app.go` | +`a.emailService.SetVeridianWebhookEmitter(...)` + `a.veridianReplyService.SetVeridianWebhookEmitter(...)` après création de l'emitter |

Fichiers veridian dédiés : `internal/service/veridian_behavioral_emit.go`
(+ test) ; helper reply dans `internal/service/veridian_reply_service.go`
(+ test). Aucune migration (la donnée existe déjà : message_history + signal replied).

