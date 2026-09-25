# Stop-on-reply — réponse prospect → exit séquence (Lot 3 sprint cold, 2026-06-15)

Réflexe cold #1 de décence/réputation : si un prospect RÉPOND, on **arrête
immédiatement** de le relancer. Second consumer du poller IMAP (Lot 1), à côté
du bounce-loop (Lot 2). Spec : mission Lot 3.

- **Détection de réponse** (`internal/domain/veridian_reply_detection.go`, PURE) :
  - **MATCH FORT (privilégié)** par Message-ID. À l'envoi on pose un header
    RFC822 déterministe `Message-ID: <{message_history.id}@{domaine}>` (cf. diff
    INLINE `smtp_service.go`). La réponse recopie ce Message-ID dans
    `In-Reply-To`/`References` → on en ré-extrait la local-part
    (`VeridianExtractMessageIDLocalParts`) = notre `message_history.id`, et on
    confirme que c'est NOTRE envoi vers CE contact via
    `MessageHistoryRepository.FindContactEmailByMessageID` (projection légère
    `contact_email` par id, **sans secretKey ni déchiffrement**).
  - **FALLBACK FAIBLE** : `From` = contact connu du workspace ET pas un NDR.
  - **NDR exclus** : un rapport de non-remise n'est JAMAIS une réponse — verdict
    délégué à `pkg/veridian_ndr.Parse` (parseur canonique PARTAGÉ avec le Lot 2,
    zéro divergence d'heuristique) + garde-fou `VeridianFromLooksLikeDaemon`
    (bloque MAILER-DAEMON/postmaster AVANT le match fort, robuste même si le
    RawBody est absent — cas où `veridian_ndr` rendrait `IsNDR=false`).
- **Signal durable** : table WORKSPACE `veridian_contact_reply` (migration V51,
  PK `contact_email`, `ON CONFLICT DO NOTHING`). PAS custom_string_5 (occupé par
  la classe), PAS contact_lists.status (sémantique abonnement). Source de vérité
  requêtée par `HasReplied`.
- **Action** (`internal/service/veridian_reply_service.go`) : pose le signal +
  timeline `email.replied` + **exit ACTIF** des automations actives du contact
  (`Status=Exited`, `ExitReason='replied'`, `IncrementAutomationStat("exited")`,
  timeline `automation.end`). Best-effort, **idempotent** (fast-path `HasReplied`
  + `ON CONFLICT` + exit borné aux automations `active` → re-dispatch IMAP = 1
  seul effet).
- 🔌 **CONTRAT exit-on-replied pour le Lot 9** : `VeridianReplyService` implémente
  `service.ColdReplyChecker` (`HasReplied(ctx, workspaceID, email) (bool, error)`,
  défini par le Lot 9 dans `veridian_cold_exit.go`). Branché dans `app.go` via
  `automationExecutor.SetColdReplyChecker(a.veridianReplyService)`. Le gate Lot 9
  (PASSIF, à chaque tick) ET l'exit actif (PUSH, à la détection) sont
  complémentaires : le gate rattrape ce que l'exit actif raterait (best-effort).
- **Consumer** : `VeridianReplyConsumer` (`Name()="stop-on-reply"`) implémente
  `domain.VeridianIMAPConsumer`, enregistré dans `app.go` via `RegisterConsumer`.

⚠️ **Diffs INLINE supplémentaires** (stop-on-reply) :

| Fichier upstream | Diff Veridian |
|---|---|
| `internal/service/smtp_service.go` | +pose `Message-ID` RFC822 déterministe `<message_id@from-domain>` via `msg.SetMessageIDWithValue(...)` (au lieu de l'aléatoire go-mail), pour rendre les réponses matchables. Vide => fallback go-mail (non-régression). Construit par `veridian_send_message_id.go`. |
| `internal/domain/message_history.go` | +1 méthode interface `MessageHistoryRepository.FindContactEmailByMessageID` (projection `contact_email` par id, match fort stop-on-reply). |
| `internal/repository/message_history_postgre.go` | +impl `FindContactEmailByMessageID` (SELECT contact_email WHERE id, found bool). |
| `internal/repository/veridian_message_history_decorator.go` | +passthrough `FindContactEmailByMessageID` (lecture, aucun side-effect quota). |
| `internal/database/init.go` | +`CREATE TABLE veridian_contact_reply` pour les workspaces créés après V51. |
| `internal/app/app.go` | +repo `veridianContactReplyRepo` + service `veridianReplyService` + `SetColdReplyChecker` sur l'executor + `RegisterConsumer(veridianReplyConsumer)`. |
| `config/config.go` | `VERSION` 50.0 → 51.0. |

Fichiers veridian dédiés : `internal/domain/veridian_reply_detection.go`,
`internal/domain/veridian_contact_reply.go`,
`internal/repository/veridian_contact_reply_postgres.go`,
`internal/service/veridian_reply_service.go`,
`internal/service/veridian_reply_consumer.go`,
`internal/service/veridian_send_message_id.go`,
`internal/migrations/v51.go` (+ tests colocalisés + mock
`mock_veridian_contact_reply_repository.go`).

