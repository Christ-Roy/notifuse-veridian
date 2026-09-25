# Plafond JOURNALIER d'envoi (cold outbound, 2026-06-14)

Troisième étage : un **plafond journalier durable**, en amont du throttle minute
(qui est un token-bucket EN MÉMOIRE incapable de garantir un cap/jour : il se
réinitialise au redémarrage worker). Spec : ticket
`todo/2026-06-14-tunnel-vente-ui-controle-et-roadmap.md` section R0.

Deux plafonds, **le plus restrictif gagne** :
- **cap par DESTINATAIRE** (anti-harcèlement) : max N envois/jour vers une même
  adresse.
- **cap par CLASSE** (réputation) : max N envois/jour vers toute une classe
  (`google`/`microsoft`/`yahoo_aol`/`freemail_fr`/`corporate`).

- **Source de vérité = `message_history`** (table v21, peuplée à chaque envoi) :
  PAS de table compteur, PAS de cron de reset. Le "jour" = `sent_at >= minuit UTC`
  calculé à la lecture (`COUNT(*)`). Survit aux redémarrages worker.
- **Fichiers veridian** : `internal/service/queue/veridian_daily_cap.go` (gate
  `veridianDailyCapGate`, méthode du worker → accès `messageHistoryRepo` sans
  nouvelle DI ; même contrat skip-and-reschedule que le throttle minute :
  `SetNextRetry` sans incrément d'attempts, re-check borné à 1h ; best-effort —
  une erreur de COUNT ne bloque jamais l'envoi). Extensions de
  `veridian_provider_class.go` : `VeridianProviderClassDailyCapFromMetadata`,
  `VeridianPerRecipientDailyCapFromMetadata`, `VeridianDomainsForClass` (la
  classe n'est PAS stockée en DB → dérivée à la lecture par liste de domaines ;
  `corporate` = exclusion des domaines connus).
- **Repo** : 2 méthodes ajoutées à `MessageHistoryRepository` :
  `CountSentSinceForContact` (cap destinataire, indexé) +
  `CountSentSinceForDomains(domains, exclude, since)` (cap classe, filtre
  `lower(split_part(contact_email,'@',2))` `= ANY`/`<> ALL`). Décorateur quota +
  mock régénérés.
- **Migration V49** (workspace-only, additive, idempotente, PAS de CONCURRENTLY
  car migrations en TX) : index `message_history(contact_email, sent_at)` +
  `(sent_at)`. Idem dans `init.go` pour les nouveaux workspaces. `config.VERSION`
  bumpé 48.0 → 49.0 + fixture `manager_test`.
- **Config** : `broadcast.metadata["veridian_provider_class_daily_cap"]`
  (map classe→int/jour) + `["veridian_per_recipient_daily_cap"]` (int/jour),
  fallback workspace settings homonymes. Absent/0 = **pas de plafond** (opt-in
  strict, non-régression). Unité **/jour** distincte du **/min** des rates.
- **Perf (décision lead)** : COUNT live, pas d'agrégat. Le cap destinataire est
  index-only. Le cap classe filtre d'abord par `sent_at` (index) ; négligeable
  sur le volume cold quotidien. Si le cap classe devient un point chaud →
  matérialiser la classe sur `message_history` (colonne + index), pas avant.

⚠️ **Diffs INLINE supplémentaires** (plafond journalier) :

| Fichier upstream | Diff Veridian |
|---|---|
| `internal/domain/email_queue.go` | +2 champs `EmailQueuePayload` : `VeridianProviderClassDailyCap` (map[string]int), `VeridianPerRecipientDailyCap` (int) — JSONB, omitempty |
| `internal/domain/workspace.go` | +2 champs `WorkspaceSettings` : `VeridianProviderClassDailyCap`, `VeridianPerRecipientDailyCap` (JSON, omitempty) |
| `internal/domain/message_history.go` | +2 méthodes interface `MessageHistoryRepository` : `CountSentSinceForContact`, `CountSentSinceForDomains` |
| `internal/repository/message_history_postgre.go` | +2 impl COUNT (cap destinataire + cap classe par domaines) |
| `internal/service/queue/worker.go` | +gate `veridianDailyCapGate` dans `processEntry` (après le throttle minute, avant `MarkAsProcessing`) |
| `internal/service/workspace_service.go` | `UpdateWorkspace` allowlist : +2 lignes propageant les caps (sinon UI Settings sauve sans persister, cf. bug pixel 2026-06-11) |
| `internal/database/init.go` | +2 `CREATE INDEX` message_history pour les nouveaux workspaces |
| `config/config.go` | `VERSION` 48.0 → 49.0 |

