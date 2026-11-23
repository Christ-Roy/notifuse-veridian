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


## Relais hors liste noire à l'ouverture (VER-225, 23/11/2026)

Le relais nord (51.210.7.44) a été listé Spamhaus ZEN du 26/10 au 06/11 (volume) : les envois
partaient et se faisaient refuser. Porte `relay_blocklist` (`queue/veridian_relay_blocklist_gate.go`),
évaluée APRÈS la fenêtre (aucune requête la nuit), pour le commercial seulement (séquences,
campagnes), sur tout profil SMTP :

- vérificateur `internal/veridian/relayguard` (capacité `relayguard`) : IPv4 de l'hôte SMTP du
  profil, requête DIRECTE aux serveurs faisant autorité de zen.spamhaus.org (UDP IPv4), témoins
  127.0.0.2 (doit être listé) et 127.0.0.1 (doit être absent) sur chaque serveur ; 127.255.255.x
  (refus, résolveur public) ou silence = serveur écarté, on passe au suivant ;
- **listée** : l'entrée attend (`relay_blocklisted`), le profil est suspendu (`veridian_paused`,
  reprise humaine `profiles:resume` après radiation) et l'alerte part (log erreur
  `VeridianRelayGuard: relay IP listed on Spamhaus ZEN, sending profile suspended`), une fois ;
- **non aboutie** : rien ne part (`relay_unverified`), nouvel essai dans 5 min ;
- **propre** : verdict gardé 1 h, donc une mesure à chaque ouverture de fenêtre puis une par heure.
- Épreuve réelle : `VERIDIAN_TEST_NETWORK=1 go test -run Live ./internal/veridian/relayguard/`
  (l'adresse d'épreuve 127.0.0.2 doit sortir listée).
