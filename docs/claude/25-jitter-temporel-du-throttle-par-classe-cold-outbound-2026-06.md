# Jitter temporel du throttle par classe (cold outbound, 2026-06-15)

Casse le rythme métronomique du throttle minute par classe destinataire (tell de
machine cold : token-bucket burst 1 = espacement strictement régulier que les
filtres/warmup détectent). On disperse le **délai de re-planification** du gate
throttle autour de sa valeur nominale (±jitter_pct, défaut **±30 %**), SANS
toucher le `rate.Limiter` → le débit MOYEN reste piloté par le token-bucket
(c'est son `Allow()` qui autorise l'envoi au tick suivant), seuls les re-checks
sont dispersés. On ne jitte QUE le throttle par classe (l'étage qui gouverne la
cadence cold), PAS le `Wait` émetteur upstream (hors scope).

- **Fichier veridian** : `internal/service/queue/veridian_jitter.go`
  (`veridianApplyJitter(delay, pct, rng)` PUR + `veridianResolveJitterPct` cascade
  + `veridianJitterDelay` appliqué PAR LE GATE). Test colocalisé.
- **Piège pointeur** : `*float64` partout. `nil` = non configuré → **défaut cold
  0.30** (le gate n'est atteint qu'en cold : des rates par classe sont actifs) ;
  `*0` = jitter **explicitement désactivé** (opt-out). À tester (nil vs *0).
- **Cascade** : `broadcast (metadata veridian_jitter_pct)` → `infra
  (EmailProvider, JSON blob, pas de migration)` → `workspace (settings + allowlist
  UpdateWorkspace)`. Clamp `[0, 0.9]`. `math/rand` (pas crypto).
- **Le gate applique LUI-MÊME le jitter** sur son délai avant `SetNextRetry` →
  **diff worker.go = 0** (le worker n'en sait rien).

⚠️ **Diffs INLINE supplémentaires** (jitter) :

| Fichier upstream | Diff Veridian |
|---|---|
| `internal/domain/email_provider.go` | +1 champ `EmailProvider.VeridianJitterPct *float64` (omitempty) — JSON blob, pas de migration |
| `internal/domain/email_queue.go` | +1 champ `EmailQueuePayload.VeridianJitterPct *float64` (omitempty) — copié à l'enqueue |
| `internal/domain/workspace.go` | +1 champ `WorkspaceSettings.VeridianJitterPct *float64` (omitempty) |
| `internal/domain/veridian_provider_class.go` | +clé `VeridianJitterPctMetadataKey` + helper `VeridianJitterPctFromMetadata` (renvoie `(pct, present)` : 0 présent = OFF, absent = défaut) + propagation broadcast→payload dans `VeridianApplyProviderThrottle` |
| `internal/service/queue/veridian_provider_throttle.go` | le gate enveloppe son `delay` nominal dans `veridianJitterDelay(workspace, provider, entry, delay)` AVANT le clamp 1s/5min |
| `internal/service/workspace_service.go` | `UpdateWorkspace` allowlist : +1 ligne `VeridianJitterPct` |

