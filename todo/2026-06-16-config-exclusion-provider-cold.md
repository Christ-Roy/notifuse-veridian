# Exclusion de providers destinataires en cold (ne PAS envoyer à Microsoft/Outlook)

> **Sévérité** : 🔴 P0
> **Owner** : agent notifuse-veridian
> **Créé** : 2026-06-16
> **Type** : feature BACKEND + UI (capacité inexistante, pas juste un trou d'expo)

## Contexte (demande #1 de Robert)

Robert (audit cold 2026-06-16) : *« il va manquer […] la possibilité de NE PAS
envoyer à des providers qui exigent un warmup avancé comme Outlook/Microsoft »*.

Cas d'usage concret : une IP/un domaine fraîchement monté ne doit PAS taper
Microsoft/Outlook (réputation Microsoft = la plus dure à warmer, SNDS/SmartScreen
impitoyables). On veut pouvoir dire « pour cette campagne / cette infra / ce
workspace : on n'envoie à AUCUNE adresse de classe `microsoft` » — les contacts
microsoft sont SKIPPÉS proprement (pas bounce, pas SMTP ouvert), le reste part
normalement.

## État réel constaté (code à l'appui — la capacité N'EXISTE PAS)

La politique d'envoi cold connaît trois leviers par classe : **rate/min**,
**cap/jour**, **pixel**. AUCUN n'exprime une exclusion :

- `internal/service/queue/veridian_provider_throttle.go:78-82` — une classe
  **sans débit configuré ou avec rate ≤ 0 n'est PAS throttlée du tout** (elle part
  à pleine vitesse) :
  ```go
  ratePerMinute, ok := rates[class]
  if !ok || ratePerMinute <= 0 {
      // Classe sans débit configuré = non throttlée
      return 0, false
  }
  ```
  → Mettre `rate microsoft = 0` dans l'UI signifie aujourd'hui « envoie microsoft
  SANS throttle », exactement l'inverse d'une exclusion. Piège dangereux.
- `internal/service/queue/veridian_daily_cap.go` — le cap/jour à 0 = illimité
  (même sémantique opt-in). Un cap à 0 ne bloque pas non plus.
- `grep -rniE "exclud|skip.?class|block.?class|do.?not.?send"` sur
  `veridian_provider_class.go` / `veridian_provider_throttle.go` /
  `veridian_daily_cap.go` / `veridian_provider_class_mx.go` → seul résultat = le
  helper `VeridianDomainsForClass(... exclude bool)` du daily-cap (filtrage SQL
  des domaines `corporate`), RIEN à voir avec une exclusion de politique d'envoi.

Conclusion : **il n'existe aucun moyen, ni backend ni UI, d'exclure une classe de
provider de l'envoi.** C'est une feature à créer, pas un câblage UI.

## Demande précise

### Backend (gate worker, pattern identique aux gates existants)

1. Nouveau champ de config **cascade** (broadcast → infra → workspace), liste de
   classes exclues, distinct des rates/caps (0 ≠ exclu) :
   - `internal/domain/email_queue.go` :
     `EmailQueuePayload.VeridianExcludedProviderClasses []string` (omitempty).
   - `internal/domain/email_provider.go` :
     `EmailProvider.VeridianExcludedProviderClasses []string` (omitempty, JSON
     blob, **pas de migration** — comme les autres champs R2).
   - `internal/domain/workspace.go` :
     `WorkspaceSettings.VeridianExcludedProviderClasses []string` (omitempty).
   - `internal/domain/veridian_provider_class.go` ou un fichier dédié
     `veridian_excluded_classes.go` : helper de résolution cascade
     `VeridianResolveExcludedClasses(broadcast, provider, workspace) map[string]bool`
     + extracteur metadata `veridian_excluded_provider_classes`.
2. **Nouveau gate worker** `veridianExcludedClassGate` dans
   `internal/service/queue/` (fichier dédié `veridian_excluded_class_gate.go`),
   câblé dans `worker.go:processEntry`. Position : **AVANT le throttle minute**
   (inutile de réserver un token pour une classe qu'on va skipper). Comportement :
   - classe du destinataire (réutiliser `w.veridianClassifyRecipient(entry)`, MX
     comme les autres gates) ∈ liste exclue → **échec PERMANENT par envoi**, PAS
     un reschedule en boucle. Réutiliser le chemin du pré-filtre Lot 7
     (`internal/service/queue/veridian_prefilter.go`) :
     `handleError(ClassifiedError{Type:recipient, Retryable:false})` → trace
     `message_history` avec `FailedAt` (raison « excluded_provider_class:microsoft »)
     → `Delete` de l'entrée queue. AUCUN SMTP ouvert, circuit breaker NON déclenché.
   - liste vide / nil = no-op strict (non-régression, opt-in).
   - propager la liste broadcast→payload comme `VeridianApplyProviderThrottle`
     le fait déjà pour les autres champs (`veridian_provider_class.go`).
3. `internal/service/workspace_service.go` `UpdateWorkspace` allowlist : **+1
   ligne** propageant `VeridianExcludedProviderClasses` (sinon l'UI sauve sans
   persister — bug vécu pixel 2026-06-11, cf. CLAUDE.md). `EmailProvider` étant
   un JSON blob, AUCUNE allowlist à étendre côté infra (comme R2).
4. Tests colocalisés (gate : classe exclue skippée en échec permanent, classe non
   exclue passe, liste vide = no-op, cascade broadcast>infra>workspace).

### UI (carte dans Settings → Cold outreach + par infra)

5. `console/src/components/settings/veridian_cold_outreach_settings.tsx` :
   - dans le bloc workspace (formulaire éditable) : un `Select mode="multiple"`
     « Classes de providers à NE PAS contacter » listant les 11 classes
     (`VERIDIAN_PROVIDER_CLASSES` + `classLabel`), persiste
     `settings.veridian_excluded_provider_classes`. Warning visible quand
     `microsoft` est exclu : « X contacts Microsoft seront ignorés » (réutiliser le
     breakdown R1 `contactCount`).
   - dans `InfraLimitsCard` (par infra) : même Select, persiste
     `EmailProvider.veridian_excluded_provider_classes` (renvoyer le provider
     COMPLET comme les autres champs de cette carte).
6. `console/src/services/api/workspace.ts` : `+veridian_excluded_provider_classes?:
   VeridianProviderClass[]` sur `WorkspaceSettings` ET sur `EmailProvider`.

## Impact

- **Débloque le warm-up Microsoft/Outlook** : on monte une infra en excluant
  microsoft pendant les premières semaines, puis on l'ouvre quand l'IP est mature.
  Sans ça, la seule alternative bricolée serait un rate microsoft à 0.0001 (= 1
  mail tous les ~7 jours) — fragile, contre-intuitif, et le code dit explicitement
  que rate ≤ 0 = PAS de throttle, donc même ça ne marche pas.
- Réputation : un envoi cold non maîtrisé vers Microsoft grille l'IP en heures.
- Demande explicite #1 de Robert sur cet axe → priorité P0.

## Fichiers exacts à toucher (récap)

| Fichier | Action |
|---|---|
| `internal/domain/email_queue.go` | +champ `VeridianExcludedProviderClasses []string` |
| `internal/domain/email_provider.go` | +champ idem (JSON blob, pas de migration) |
| `internal/domain/workspace.go` | +champ idem `WorkspaceSettings` |
| `internal/domain/veridian_excluded_classes.go` (NOUVEAU) | helper cascade + extracteur metadata + tests |
| `internal/domain/veridian_provider_class.go` | propagation broadcast→payload dans `VeridianApplyProviderThrottle` |
| `internal/service/queue/veridian_excluded_class_gate.go` (NOUVEAU) | gate worker + test |
| `internal/service/queue/worker.go` | +câblage gate (avant throttle minute) |
| `internal/service/workspace_service.go` | +1 ligne allowlist `UpdateWorkspace` |
| `console/src/components/settings/veridian_cold_outreach_settings.tsx` | Select exclusion workspace + par infra |
| `console/src/services/api/workspace.ts` | +type sur `WorkspaceSettings` + `EmailProvider` |

Tier de promo : 🔴 HAUT (nouveau gate worker, surface envoi) → push sans
`[risk:low]`, reco + e2e staging avant prod.
