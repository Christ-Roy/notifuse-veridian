# Throttle par provider destinataire (cold outbound, 2026-06-10)

Second étage de rate-limiting keyé par **classe de provider destinataire**
(`google` / `microsoft` / `yahoo_aol` / `freemail_fr` / `corporate`), en
amont du throttle émetteur upstream qui reste intact. Spec : ticket
`todo/2026-06-02-throttle-par-provider-destinataire.md` (archivé dans
`todo/done/`).

- **Fichiers veridian** : `internal/domain/veridian_provider_class.go`
  (classification V1 par suffixe, parsing config, contrat tag contact
  `custom_string_5`), `internal/service/queue/veridian_provider_rate_limiter.go`
  (clone du pattern `IntegrationRateLimiter`, clé `integrationID|classe`),
  `internal/service/queue/veridian_provider_throttle.go` (gate worker
  skip-and-reschedule, pattern circuit breaker : `SetNextRetry` sans
  incrément d'attempts, pas de head-of-line blocking).
- **Config** : par broadcast via `broadcast.metadata["veridian_provider_class_rates"]`
  (map classe → emails/minute, fractions OK : `0.5` = 1 mail/2 min), fallback
  workspace settings `veridian_provider_class_rates`. Aucune config = no-op
  strict (non-régression upstream).

⚠️ **Diffs INLINE sur fichiers upstream** (à re-vérifier à chaque merge
upstream, le pre-push bypass `@notifuse.com` ne les protège pas) :

| Fichier upstream | Diff Veridian |
|---|---|
| `internal/domain/email_queue.go` | +2 champs `EmailQueuePayload` : `VeridianProviderClass`, `VeridianProviderClassRates` (JSONB, omitempty) |
| `internal/domain/workspace.go` | +1 champ `WorkspaceSettings` : `VeridianProviderClassRates` (JSON, omitempty) |
| `internal/service/queue/worker.go` | +champ `providerClassLimiter` + init constructeur + gate `veridianProviderClassGate` dans `processEntry` (avant `MarkAsProcessing`, après circuit breaker) |
| `internal/service/broadcast/queue_message_sender.go` | +2 appels `domain.VeridianApplyProviderThrottle(entry, broadcast, contact)` (SendBatch + SendToRecipient) |

