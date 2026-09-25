# Anti-hash identique par classe de provider destinataire (cold outbound, 2026-06-15)

Empêche deux mails au **rendu identique** (sujet + corps normalisés = même hash)
de partir vers la **même classe de provider destinataire** dans une fenêtre
glissante (défaut **72h**). Le spintax SEUL ne suffit pas (1 groupe `{A|B}` =
2 variantes → ~250 rendus identiques sur 500 envois gmail). On bloque
l'**identité de hash** (rendu identique modulo whitespace/casse), PAS la
similarité floue (pas de fuzzy hashing maison = usine à gaz refusée). **Jamais de
perte de mail** sur ce motif (≠ pré-filtre Lot 7 permanent).

- **VARIÉTÉ garantie à l'ENQUEUE** (le sender a le template brut) :
  `internal/service/broadcast/veridian_content_dedup.go`
  (`veridianContentDedup.Resolve`) calcule le hash du rendu final, vérifie la
  collision par classe via `ExistsContentHashSince`, et en cas de collision
  **RE-SPIN** avec un seed perturbé (`email:r1`, `:r2`…, déterministe) jusqu'à
  **3 fois**. Template sans variété (re-spin ne change pas le hash) → envoi du
  rendu courant + warning (pas de perte). DI optionnelle via la factory (construit
  avec `messageHistoryRepo`). No-op strict hors contexte cold / anti-hash off /
  dedup nil.
- **FILET best-effort au WORKER** :
  `internal/service/queue/veridian_content_hash_gate.go`
  (`veridianContentHashGate`, **LOG-ONLY**) constate une collision résiduelle (le
  worker ne peut pas re-varier un payload figé) et la TRACE sans bloquer. Placé
  après le pré-filtre, avant `MarkAsProcessing`. Best-effort strict (pas de hash =
  no-op, erreur DB = pass).
- **Hash PUR** : `internal/domain/veridian_content_hash.go`
  (`VeridianContentHash(subject, body)` = `SHA-256(normalize(subj)+0x00+normalize(body))`
  tronqué **128 bits** = 32 hex). `normalize` = lowercase + collapse whitespace +
  trim. + helpers de config cascade (`VeridianAntiHashEnabledFor`,
  `VeridianAntiHashWindow`, extracteurs metadata).
- **Stockage (voie A)** : colonne `message_history.veridian_content_hash CHAR(32)`
  (nullable) + index PARTIEL `(veridian_content_hash, sent_at) WHERE NOT NULL`,
  **migration V52**. Posé à l'enqueue, écrit en `message_history` au succès
  (worker `upsertMessageHistory`, NULLIF vide → NULL hors index). EXISTS
  index-only par classe (filtre domaines comme le daily cap → **même dégradation
  gracieuse MX** assumée : classes MX non enforced via ce chemin, le throttle
  minute protège le hot path).
- **Linter délivrabilité** (`pkg/veridian_deliverability`) : règle informative
  `LOW_SPINTAX_VARIETY` (poids 1.0) déclenchée si l'appelant renseigne
  `VariantCount` (via `pkg/veridian_spintax.CountVariants(templateBrut)`, PUR,
  ajouté) `<` `TargetVolume`. Guide, pas filet. Inactif si l'un des deux = 0
  (non-régression).
- **Config** : `broadcast.metadata["veridian_anti_hash_enabled"/"veridian_anti_hash_window_hours"]`
  → infra (`EmailProvider`, JSON blob) → workspace (settings + allowlist).
  `enabled` = `*bool` (nil=défaut cold ON, *false=OFF) ; `window_hours` int
  (<=0 = défaut 72h). Cascade `broadcast→infra→workspace→défaut`.

⚠️ **Diffs INLINE supplémentaires** (anti-hash) :

| Fichier upstream | Diff Veridian |
|---|---|
| `internal/domain/email_queue.go` | +1 champ `EmailQueuePayload.VeridianContentHash string` (omitempty) — posé à l'enqueue |
| `internal/domain/email_provider.go` | +2 champs `EmailProvider.VeridianAntiHashEnabled *bool` + `VeridianAntiHashWindowHours int` (omitempty) — JSON blob, pas de migration |
| `internal/domain/workspace.go` | +2 champs `WorkspaceSettings.VeridianAntiHashEnabled *bool` + `VeridianAntiHashWindowHours int` (omitempty) |
| `internal/domain/message_history.go` | +1 champ `MessageHistory.VeridianContentHash string` (omitempty) + 1 méthode interface `ExistsContentHashSince(ctx, ws, hash, domains, exclude, since)` |
| `internal/repository/message_history_postgre.go` | `Create`/`Upsert` : +colonne `veridian_content_hash` (NULLIF vide→NULL ; Upsert `DO UPDATE` COALESCE) + impl `ExistsContentHashSince` (EXISTS par hash+fenêtre+classe-par-domaines) |
| `internal/repository/veridian_message_history_decorator.go` | passthrough `ExistsContentHashSince` (lecture, zéro side-effect quota) |
| `internal/service/queue/worker.go` | +gate `veridianContentHashGate` (filet log-only) après pré-filtre ; `upsertMessageHistory` propage `entry.Payload.VeridianContentHash` → `message_history` |
| `internal/service/broadcast/queue_message_sender.go` | `buildQueueEntry` : capture `subjectLiquid` (pré-spintax) + appel `veridianContentDedup.Resolve` (re-spin) + pose `Payload.VeridianContentHash` ; +champ `veridianContentDedup` + `SetVeridianContentDedup` (DI) |
| `internal/service/broadcast/factory.go` | `CreateMessageSender` : `SetVeridianContentDedup(newVeridianContentDedup(f.messageHistoryRepo, f.logger))` sur le queue sender |
| `internal/service/workspace_service.go` | `UpdateWorkspace` allowlist : +2 lignes `VeridianAntiHashEnabled` + `VeridianAntiHashWindowHours` |
| `internal/database/init.go` | +colonne `message_history.veridian_content_hash` + index partiel (nouveaux workspaces) |
| `config/config.go` | `VERSION` 51.0 → 52.0 |
| `internal/migrations/manager_test.go` | fixture sqlmock `db_version` 51 → 52 |
| `pkg/veridian_deliverability/veridian_deliverability.go` | +champs `Input.VariantCount`/`TargetVolume` + règle `LOW_SPINTAX_VARIETY` |

Fichiers veridian dédiés : `internal/domain/veridian_content_hash.go`,
`internal/service/broadcast/veridian_content_dedup.go`,
`internal/service/queue/veridian_content_hash_gate.go`,
`internal/migrations/v52.go`, `pkg/veridian_spintax/veridian_spintax_variants.go`
(+ tests colocalisés + mock `mock_message_history_repository.go` régénéré).
Migration V52 dans `migrations-pending.txt` (override safety §12, runner en TX).

