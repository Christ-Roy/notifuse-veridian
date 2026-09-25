# Fenêtre d'envoi — horaires ouvrables (sending windows, 2026-06-15)

Gate worker qui ne laisse envoyer que dans une fenêtre configurable (jours +
heures + timezone, ex. lun-ven 9h-18h Europe/Paris). Hors fenêtre →
skip-and-reschedule à la prochaine ouverture (`NextOpening`), MÊME contrat que les
gates throttle/cap : `SetNextRetry` SANS incrément d'attempts, délai borné à 24h.
Aucune fenêtre = envoi 24/7 (non-régression). Spec : ticket Lot WINDOWS.

- **Fichiers veridian** : `internal/domain/veridian_sending_window.go`
  (`VeridianSendingWindow` : `IsValid`/`IsWithinWindow`/`NextOpening` ; plage
  croissante stricte, pas de wrap minuit = pas de fenêtre ; parsing
  `VeridianSendingWindowFromMetadata`) +
  `internal/service/queue/veridian_sending_window_gate.go`
  (`veridianSendingWindowGate` + `veridianResolveSendingWindow` cascade). Timezone
  de la fenêtre, si vide → fallback `WorkspaceSettings.Timezone`.
- **Cascade** identique aux rates/caps : `broadcast (metadata)` → `infra
  (EmailProvider)` → `workspace settings` → rien = pas de fenêtre. Premier niveau
  VALIDE gagne.
- **Câblage worker** : gate dans `processEntry` APRÈS le daily cap, AVANT le
  pré-filtre (pas de classification/COUNT si on est hors fenêtre).

⚠️ **Diffs INLINE supplémentaires** (multi-SMTP round-robin + sending windows) :

| Fichier upstream | Diff Veridian |
|---|---|
| `internal/domain/email_provider.go` | +1 champ `EmailProvider.VeridianSendingWindow` (*VeridianSendingWindow, omitempty) ; +3 méthodes via `veridian_sender_rotation.go` : `VeridianSelectSender`, `VeridianActiveSenderCount`, `VeridianEffectiveRateLimit` (déclarées hors ce fichier, comptées sur `veridian_sender_rotation.go`) |
| `internal/domain/email_queue.go` | +1 champ `EmailQueuePayload.VeridianSendingWindow` (*VeridianSendingWindow, omitempty) — copié à l'enqueue |
| `internal/domain/workspace.go` | +1 champ `WorkspaceSettings.VeridianSendingWindow` (*VeridianSendingWindow, omitempty) ; le `Timezone` workspace sert de fallback à la fenêtre |
| `internal/domain/veridian_provider_class.go` | `VeridianApplyProviderThrottle` propage la sending window broadcast → payload |
| `internal/service/queue/worker.go` | +gate `veridianSendingWindowGate` dans `processEntry` (après daily cap, avant pré-filtre) ; `RateLimitPerMinute` → `VeridianEffectiveRateLimit()` au call-site rate limiter ET dans `getMinEmailRateLimit` (capacité alignée multi-SMTP) |
| `internal/service/broadcast/message_sender.go` | `GetSender` → `veridianResolveSender` dans `SendToRecipient` ; +champ `veridianSenderRotator` + `SetVeridianSenderRotator` |
| `internal/service/broadcast/queue_message_sender.go` | `GetSender` → `veridianResolveSender` dans `buildQueueEntry` ; +champ `veridianSenderRotator` + `SetVeridianSenderRotator` |
| `internal/service/broadcast/factory.go` | +champ `veridianSenderRotator` (créé une fois, partagé) injecté dans les deux senders via `SetVeridianSenderRotator` |
| `internal/service/workspace_service.go` | `UpdateWorkspace` allowlist : +1 ligne propageant `VeridianSendingWindow` (sinon l'UI Settings sauve sans persister) |

