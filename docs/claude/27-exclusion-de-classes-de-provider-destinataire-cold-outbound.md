# Exclusion de classes de provider destinataire (cold outbound, 2026-06-16)

Levier DÉDIÉ pour EXCLURE une ou plusieurs classes de provider destinataire de
l'envoi cold (cas #1 Robert : « ne PAS envoyer à microsoft/outlook » sur une IP
fraîche, Microsoft = warm-up le plus dur). Les contacts d'une classe exclue sont
SKIPPÉS proprement (échec PERMANENT par envoi, pas de SMTP, pas de bounce, entrée
queue Delete) ; le reste du broadcast part normalement. Spec : ticket
`todo/2026-06-16-config-exclusion-provider-cold.md`.

- **Pourquoi un levier neuf** : les leviers throttle/cap sont OPT-IN « 0 = pleine
  vitesse / illimité ». Mettre `rate microsoft = 0` = « envoie microsoft SANS
  throttle » (`veridian_provider_throttle.go:75`), l'INVERSE d'une exclusion. Donc
  une LISTE de classes exclues, distincte des rates/caps.
- **Cascade** (du plus spécifique au plus général), identique aux rates/caps :
  `broadcast (metadata veridian_excluded_provider_classes)` → `infra
  (EmailProvider, JSON blob, pas de migration)` → `workspace settings`. Premier
  niveau NON VIDE gagne. Liste vide/nil partout = no-op strict (non-régression).
- **Gate worker** : `veridianExcludedClassGate` (fichier dédié
  `internal/service/queue/veridian_excluded_class_gate.go`) câblé dans
  `processEntry` **APRÈS le circuit breaker, AVANT le throttle minute** (inutile de
  réserver un token pour une classe qu'on skippe). Classe résolue via
  `w.veridianClassifyRecipient(entry)` (tag amont prime, sinon MX — MÊME résolution
  que throttle/cap, zéro duplication). Si exclue → chemin du pré-filtre Lot 7 :
  `MarkAsProcessing` puis `handleError(ClassifiedError{Type:recipient,
  Retryable:false})` (raison `excluded_provider_class:<classe>`) → `message_history`
  FailedAt + Delete. Circuit breaker NON déclenché (décision de politique, pas
  erreur provider).
- **Fichiers veridian** : `internal/domain/veridian_excluded_classes.go`
  (`VeridianResolveExcludedClasses` cascade → set O(1) + extracteur metadata
  `VeridianExcludedProviderClassesFromMetadata` + normalisation/dédup/validation) +
  `internal/service/queue/veridian_excluded_class_gate.go` (+ tests colocalisés). UI :
  `console/src/components/settings/veridian_cold_outreach_settings.tsx`
  (`ExcludedClassesCard` workspace avec warning chiffré « X contacts ignorés » via
  breakdown R1 + multi-select par infra dans `InfraLimitsCard`) + types front
  (`WorkspaceSettings` ET `EmailProvider` += `veridian_excluded_provider_classes`).
- **Pas de migration, pas de `config.VERSION` bump** (aucun schéma DB touché ;
  `EmailProvider` JSON blob, settings JSON).

⚠️ **Diffs INLINE supplémentaires** (exclusion de classes) :

| Fichier upstream | Diff Veridian |
|---|---|
| `internal/domain/email_queue.go` | +1 champ `EmailQueuePayload.VeridianExcludedProviderClasses []string` (omitempty) — copié à l'enqueue |
| `internal/domain/email_provider.go` | +1 champ `EmailProvider.VeridianExcludedProviderClasses []string` (omitempty) — JSON blob, pas de migration |
| `internal/domain/workspace.go` | +1 champ `WorkspaceSettings.VeridianExcludedProviderClasses []string` (omitempty) |
| `internal/domain/veridian_provider_class.go` | `VeridianApplyProviderThrottle` propage l'exclusion broadcast → payload |
| `internal/service/queue/worker.go` | +gate `veridianExcludedClassGate` dans `processEntry` (après circuit breaker, AVANT le throttle minute) → route vers `handleError` permanent (skip SMTP, jamais re-tenté) |
| `internal/service/workspace_service.go` | `UpdateWorkspace` allowlist : +1 ligne `VeridianExcludedProviderClasses` (sinon l'UI Settings sauve sans persister) |

