# Rates + caps par INFRA d'envoi (R2 cold outreach, 2026-06-14)

Ajoute un niveau **INFRA** au MILIEU des cascades de config cold outbound. L'infra
d'envoi = l'intégration `EmailProvider` (host/port/IP/relai SMTP + senders + son
rate). Une IP en warm-up porte ses propres débits/plafonds, indépendants du
workspace. Spec : ticket `todo/2026-06-14-...roadmap.md` (section R2).

- **Cascade étendue** (du plus spécifique au plus général) : `broadcast (payload)`
  → **`infra (EmailProvider)`** [NOUVEAU] → `workspace settings` → rien = no-op.
  S'applique à la fois aux **rates** (emails/min, `veridian_provider_throttle.go`)
  ET aux **caps journaliers** (classe + destinataire, `veridian_daily_cap.go`).
  Premier niveau non vide gagne (pas de merge par classe entre niveaux). Pour les
  caps, cap-classe et cap-destinataire sont résolus INDÉPENDAMMENT.
- **Pas de migration, pas de bug de propagation** : `EmailProvider` est affecté
  PAR VALEUR dans `CreateIntegration`/`UpdateIntegration` (`updatedIntegration.EmailProvider = req.Provider`)
  et persisté comme **JSON blob** (`integrations` column). Les 3 nouveaux champs
  `omitempty` passent donc automatiquement — AUCUNE allowlist champ-par-champ à
  étendre (contrairement à `UpdateWorkspace` pour les settings). `workspace_service.go`
  NON touché.
- **Plomberie worker** : le worker a déjà `integration := workspace.GetIntegrationByID(...)`
  en main avant les gates (worker.go:261) → il passe `&integration.EmailProvider`
  aux deux gates sans requête supplémentaire. Signatures :
  `veridianProviderClassGate(workspace, provider, entry)` et
  `veridianDailyCapGate(workspace, provider, entry)`. `provider == nil` (legacy) →
  niveau infra sauté = comportement pré-R2 strictement inchangé.
- **Fichiers veridian** : `veridian_provider_infra_cascade_test.go` (tests cascade
  3 niveaux rates + caps). Helpers de résolution : `veridianResolveProviderClassRates`
  (throttle) + `veridianResolveDailyCaps` étendu (daily cap, fichier capdaily).
- **UI à venir** (agent uifix) : config rates/caps par infra dans
  Settings → Integrations (par EmailProvider).

⚠️ **Diffs INLINE supplémentaires** (rates+caps par infra) :

| Fichier upstream | Diff Veridian |
|---|---|
| `internal/domain/email_provider.go` | +3 champs `EmailProvider` : `VeridianProviderClassRates` (map[string]float64), `VeridianProviderClassDailyCap` (map[string]int), `VeridianPerRecipientDailyCap` (int), tous `omitempty` |
| `internal/service/queue/worker.go` | 2 call-sites : `veridianProviderClassGate` + `veridianDailyCapGate` reçoivent `&integration.EmailProvider` (param `provider` ajouté) |

