# Cap journalier par SENDER émetteur — warmup IP (cold outbound, 2026-06-16)

Quatrième dimension de plafonnement, **keyée ÉMETTEUR** (≠ les caps V49 keyés
DESTINATAIRE). But : warmup IP/domaine classique — chaque boîte d'envoi monte son
propre volume jour après jour, indépendamment du destinataire. Le PLUS RESTRICTIF
gagne avec les caps destinataire/classe. Spec : ticket
`todo/2026-06-16-cap-par-sender-emetteur.md` (lecture B « 1/jour par provider »).

- **Audit pré-code (clé)** : `message_history` ne stockait AUCUNE colonne sender
  exploitable — le FROM ne vivait que dans le JSON `channel_options.FromName`
  (display name, non-queryable). Migration NÉCESSAIRE (pas de dérivation
  bricolée). **Migration V53** (workspace-only, additive, idempotente, PAS de
  CONCURRENTLY car runner en TX) : colonne `message_history.veridian_sender_email
  VARCHAR(255)` (nullable, stockée lowercase) + index PARTIEL
  `(veridian_sender_email, sent_at) WHERE NOT NULL`. `config.VERSION` 52→53,
  fixture `manager_test`, `migrations-pending.txt` (override safety §12). Idem
  `init.go` pour les nouveaux workspaces.
- **Écriture** : posée à l'envoi (`worker.go:upsertMessageHistory`) depuis
  `entry.Payload.FromAddress` (sender figé à l'enqueue, sender-rotation incluse).
  `Create`/`Upsert` : `NULLIF(lower($26),'')` → vide = NULL (hors index partiel) ;
  Upsert `DO UPDATE COALESCE` préserve.
- **Repo** : `MessageHistoryRepository.CountSentSinceForSender(senderEmail, since)`
  (index-only, COUNT `WHERE veridian_sender_email = lower($1) AND sent_at >= $2`).
  Décorateur quota (passthrough lecture) + mock régénéré (ajout manuel, mockgen
  bloqué par go.sum).
- **Gate worker** : `veridian_per_sender_cap.go` (`veridianPerSenderCapGate` +
  `veridianResolvePerSenderCap`), JUMEAU de `veridianDailyCapGate`. Placé dans
  `processEntry` APRÈS le daily-cap destinataire, AVANT la sending-window. Même
  contrat skip-and-reschedule (`SetNextRetry` sans incrément attempts, re-check
  borné 1h via `veridianRescheduleCapped`). Best-effort : erreur COUNT = pass ;
  `FromAddress` vide = no-op (pas de clé d'attribution).
- **Config** : `EmailProvider.VeridianPerSenderDailyCap int` (JSON blob infra, pas
  de migration pour la config) + `WorkspaceSettings` (allowlist `UpdateWorkspace`)
  + `EmailQueuePayload` (metadata broadcast `veridian_per_sender_daily_cap` via
  `VeridianApplyProviderThrottle` + helper `VeridianPerSenderDailyCapFromMetadata`).
  Cascade `broadcast → infra → workspace`. 0/vide = pas de plafond (opt-in strict).
- **UI** : champ « per-sender daily cap (warmup) » exposé au NIVEAU WORKSPACE
  (carte caps globaux) ET par INFRA (`InfraLimitsCard`) dans
  `veridian_cold_outreach_settings.tsx`. + **preset « Mode warmup »**
  (`VERIDIAN_WARMUP_PRESET` dans `workspace.ts`, `PresetCard` owner-only) :
  bouton qui pré-remplit caps=1/classe + per-recipient=1 + per-sender=20 + rates
  0.5/min + fenêtre lun-ven 9-18 Europe/Paris, sans sauver (relecture + Save). Le
  round-robin s'active tout seul (≥2 senders + contexte cold) — warning si <2.

⚠️ **Diffs INLINE supplémentaires** (cap par sender + preset warmup) :

| Fichier upstream | Diff Veridian |
|---|---|
| `internal/domain/email_provider.go` | +1 champ `EmailProvider.VeridianPerSenderDailyCap int` (omitempty) — JSON blob, pas de migration |
| `internal/domain/email_queue.go` | +1 champ `EmailQueuePayload.VeridianPerSenderDailyCap int` (omitempty) — copié à l'enqueue |
| `internal/domain/workspace.go` | +1 champ `WorkspaceSettings.VeridianPerSenderDailyCap int` (omitempty) |
| `internal/domain/message_history.go` | +1 champ `MessageHistory.VeridianSenderEmail string` (omitempty) + 1 méthode interface `CountSentSinceForSender` |
| `internal/domain/veridian_provider_class.go` | +clé `VeridianPerSenderDailyCapMetadataKey` + helper `VeridianPerSenderDailyCapFromMetadata` + propagation broadcast→payload dans `VeridianApplyProviderThrottle` |
| `internal/repository/message_history_postgre.go` | `Create`/`Upsert` : +colonne `veridian_sender_email` (NULLIF lower vide→NULL ; Upsert COALESCE) + impl `CountSentSinceForSender` |
| `internal/repository/veridian_message_history_decorator.go` | passthrough `CountSentSinceForSender` (lecture, zéro side-effect quota) |
| `internal/service/queue/worker.go` | +gate `veridianPerSenderCapGate` dans `processEntry` (après daily cap, avant sending window) ; `upsertMessageHistory` propage `entry.Payload.FromAddress` → `veridian_sender_email` |
| `internal/service/workspace_service.go` | `UpdateWorkspace` allowlist : +1 ligne `VeridianPerSenderDailyCap` |
| `internal/database/init.go` | +colonne `message_history.veridian_sender_email` + index partiel (nouveaux workspaces) |
| `config/config.go` | `VERSION` 52.0 → 53.0 |
| `internal/migrations/manager_test.go` | fixture sqlmock `db_version` 52 → 53 |

Fichiers veridian dédiés : `internal/service/queue/veridian_per_sender_cap.go`,
`internal/migrations/v53.go` (+ tests colocalisés ;
`console/src/components/settings/veridian_cold_outreach_settings.tsx` étendu :
preset + champ cap sender ; `console/src/services/api/workspace.ts` :
`VERIDIAN_WARMUP_PRESET`). Migration V53 dans `migrations-pending.txt` (override
safety §12, runner en TX). Rampe progressive auto = ticket séparé non livré ici.

