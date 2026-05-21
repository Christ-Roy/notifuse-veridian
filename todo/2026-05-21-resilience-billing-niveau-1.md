# 2026-05-21 — Résilience billing niveau 1 (cache plan + grace Hub-down)

> **Type** : Backend résilience cross-app — Notifuse continue de servir si Hub down
> **Sévérité** : 🟡 P2 (pas bloquant pour 1er client mais à câbler avant 10 clients payants)
> **Owner** : agent Notifuse (Go) — autonome
> **Créé** : 2026-05-21
> **Lien parent** : décision archi Robert 2026-05-21 (réflexion redondance Stripe comme pour auth)
> **Lien CONTRAT-HUB** : §1.4 (gravé 2026-05-21) — "Hub source de vérité + résilience apps"

---

## Problème à résoudre

Aujourd'hui, **toutes** les mutations billing/lifecycle d'un tenant
côté Notifuse passent par le Hub (provision, update-plan, suspend,
resume, soft-delete, restore, touch). Si le Hub plante 24-72h :

- Notifuse n'a aucun moyen de détecter que Hub est silencieux
- Le `veridian_plan` cache local devient stale sans signal
- Aucune dégradation contrôlée vers paywall lecture seule
- Le tenant peut continuer d'envoyer des mails sur un plan obsolète
  (perte revenu si Stripe a downgrade entre-temps), OU au contraire
  être bloqué arbitrairement si le cache local pointe vers un état
  ancien type "suspended"

**Objectif** : Notifuse mesure la fraîcheur du lien Hub→Notifuse via
un timestamp `last_hub_sync_at`, et applique une dégradation graduelle
selon l'âge du dernier sync.

---

## Approche choisie (la plus simple et propre)

**PAS un poll Notifuse→Hub** (pas d'endpoint Hub `/api/tenants/:id/state`
HMAC dispo, et un poll N tenants × 6h = inutile complexité).

**Pattern "push freshness measurement"** : `last_hub_sync_at` est mis
à jour à **chaque mutation Hub→Notifuse**. Si Hub down → les pushs
n'arrivent plus → le timestamp vieillit → on déclenche la dégradation.

C'est le proxy le plus fiable car il mesure exactement ce qu'on veut
mesurer : "depuis combien de temps Hub n'a-t-il pas eu d'effet sur ce
tenant ?".

---

## Spec technique

### 1. Migration V39 — colonne additive

Fichier : `internal/migrations/v39.go` + `v39_test.go`

```sql
ALTER TABLE veridian_plan
  ADD COLUMN IF NOT EXISTS last_hub_sync_at TIMESTAMP WITH TIME ZONE;
```

Backfill : `UPDATE veridian_plan SET last_hub_sync_at = updated_at`
(approximation safe — sous-estime un peu pour les tenants jamais
modifiés post-provision, mais ces tenants ont au moins reçu leur
provision donc on prend leur `updated_at` initial).

**⚠️ Piège connu** : bumper aussi `config/config.go:18` → `"39.0"`,
sinon migration jamais déclenchée au boot (cf. memory
`reference_config_version_bump_required`).

Ne pas oublier `internal/migrations/manager_test.go` ligne ~547
fixture `AddRow("39")`.

### 2. Domain — étendre VeridianPlan + constantes seuils

Fichier : `internal/domain/veridian.go` + `veridian_test.go`

```go
type VeridianPlan struct {
    // ... existant ...
    LastHubSyncAt *time.Time `json:"last_hub_sync_at,omitempty"`
}

// HubSyncFreshThreshold : si last_hub_sync_at < NOW - 24h on est en
// mode normal. Hub considéré "frais".
const HubSyncFreshThreshold = 24 * time.Hour

// HubSyncDeadThreshold : si last_hub_sync_at < NOW - 72h on bascule en
// dégradation paywall lecture seule. Hub considéré "mort".
// Entre 24-72h = grace period optimistic (sert comme avant, log warn).
const HubSyncDeadThreshold = 72 * time.Hour

// HubSyncStatus représente l'état de fraîcheur du lien Hub→Notifuse.
type HubSyncStatus string

const (
    HubSyncFresh HubSyncStatus = "fresh" // < 24h
    HubSyncStale HubSyncStatus = "stale" // 24-72h grace optimistic
    HubSyncDead  HubSyncStatus = "dead"  // > 72h dégrade
)

// EvaluateHubSyncStatus retourne l'état de fraîcheur. NULL = jamais
// synchronisé = traité comme "fresh" (cas legacy pre-V39 backfillé
// par migration).
func (p *VeridianPlan) EvaluateHubSyncStatus(now time.Time) HubSyncStatus {
    if p.LastHubSyncAt == nil {
        return HubSyncFresh // pas de signal = best-effort
    }
    age := now.Sub(*p.LastHubSyncAt)
    switch {
    case age < HubSyncFreshThreshold:
        return HubSyncFresh
    case age < HubSyncDeadThreshold:
        return HubSyncStale
    default:
        return HubSyncDead
    }
}
```

### 3. Repository — TouchHubSync helper

Fichier : `internal/repository/veridian_plan_postgres.go` + test

```go
// TouchHubSync met à jour last_hub_sync_at = NOW pour le workspace.
// Idempotent. No-op silencieux si la row n'existe pas.
// Appelé en queue de chaque mutation Hub→Notifuse pour mesurer la
// fraîcheur du lien.
func (r *veridianPlanRepository) TouchHubSync(ctx context.Context, workspaceID string) error {
    const q = `
        UPDATE veridian_plan
        SET last_hub_sync_at = $2
        WHERE workspace_id = $1
    `
    _, err := r.systemDB.ExecContext(ctx, q, workspaceID, time.Now().UTC())
    return err
}
```

⚠️ **Piège TZ connu** : `updated_at` est `WITHOUT TIME ZONE` (legacy),
`last_hub_sync_at` est `WITH TIME ZONE`. **Ne pas** partager le même
`$N` sur un UPDATE qui touche les 2 colonnes (cf. memory
`feedback_sqlmock_does_not_validate_postgres_types`). Le UPDATE
ci-dessus ne touche que `last_hub_sync_at`, donc safe.

**Étendre `Get` et `Upsert`** pour lire/écrire `last_hub_sync_at`
(pattern lot 2 V37, lot C V38).

### 4. Service — câbler TouchHubSync dans toutes les mutations Hub

Fichier : `internal/service/veridian_service.go` + tests

Mutations à câbler (queue, après l'opération principale, best-effort) :

- `Provision` (création initiale)
- `UpdatePlan`
- `Suspend`
- `Resume`
- `SoftDelete`
- `Restore`
- `Touch` (heartbeat anti-soft-delete)
- `AttachOwner`
- `AttachMember`
- `GrantUnlimited`

Pattern :

```go
func (s *veridianService) Provision(...) (..., error) {
    // ... logique existante ...

    // Marquer le sync Hub réussi (best-effort, ne bloque pas la mutation)
    if err := s.planRepo.TouchHubSync(ctx, input.TenantID); err != nil {
        s.logger.WithFields(...).Warn("touch hub sync failed (non-fatal)")
    }

    return resp, nil
}
```

**Ne PAS** câbler `WipeTestTenants` ni les méthodes admin internes
(grant-unlimited tu vois reste discutable — c'est admin mais ça
remonte aussi de Hub potentiellement). Décision agent : grant-unlimited
**oui** car Robert peut l'appeler depuis Hub future feature, et c'est
un signal valide de "lien Hub OK".

### 5. Middleware paywall — gating 3 phases

Fichier : `internal/http/middleware/veridian_paywall.go` + test

Modifier `VeridianPaywallPathFilter` (ou helper interne `IsBlocked`)
pour évaluer `HubSyncStatus` en plus du status tenant actuel.

```go
// Évalue d'abord le status tenant (suspended/quota/deleted) comme avant.
// Si tenant actif : check hub sync freshness.
status := plan.EvaluateHubSyncStatus(time.Now())
switch status {
case domain.HubSyncFresh:
    // mode normal, rien à faire
case domain.HubSyncStale:
    // mode optimistic grace : log warn 1×/minute par tenant pour ne
    // pas spammer les logs, mais continue de servir.
    if shouldLogStale(tenantID) {
        log.Warn("hub sync stale > 24h, continuing best-effort", ...)
    }
case domain.HubSyncDead:
    // mode dégradé : refuser les writes avec 503 Service Unavailable +
    // header Retry-After. Reads passent encore (best-effort).
    if isWriteRoute(r) {
        WriteJSONErrorCode(w, ErrCodeHubSyncDead,
            "Service degraded — Veridian Hub unreachable since > 72h. Writes paused for safety.",
            http.StatusServiceUnavailable,
            map[string]interface{}{
                "last_hub_sync_at": plan.LastHubSyncAt,
                "retry_after_s":    3600,
            })
        w.Header().Set("Retry-After", "3600")
        return
    }
}
```

**Distinct du paywall mode dégradé soft-deleted** (`§21`) :
- Soft-deleted = décision business Hub (tenant churné/trial expiré)
- HubSyncDead = défaillance technique Hub (incident infra)
- Codes erreur distincts pour debug
- Both peuvent coexister sur le même tenant

**Helper `shouldLogStale(tenantID)`** : dédup logs via map mémoire
`{tenantID → lastLoggedAt}`. Rate-limit 1×/min par tenant.

**Helper `isWriteRoute(r)`** : check méthode HTTP (POST/PUT/PATCH/DELETE)
ET path matchant (skip `/api/veridian/*` admin Hub car eux ne devraient
pas être bloqués — Hub doit pouvoir réveiller le tenant).

### 6. Errors — nouveau code

Fichier : `internal/http/veridian_errors.go` + test

```go
ErrCodeHubSyncDead = "hub_sync_dead"
```

### 7. Wire dans app.go

Pas de cron nécessaire. Pas de nouveau service. Juste les modifications
des méthodes existantes du service. **Zéro change app.go.**

### 8. Tests obligatoires (Constitution §1, mapping 1-pour-1)

- `v39_test.go` : 8 tests pattern V37/V38 (ADD COLUMN + backfill + registry)
- `veridian_test.go` (domain) :
  - `EvaluateHubSyncStatus` : nil → Fresh, < 24h → Fresh, 24-72h → Stale,
    > 72h → Dead, exactement 24h → Stale (boundary), exactement 72h → Dead
  - JSON roundtrip `LastHubSyncAt` (omitempty si nil)
- `veridian_plan_postgres_test.go` :
  - `TouchHubSync` sqlmock UPDATE + idempotent (0 rows)
  - `Get` étendu : nullable LastHubSyncAt scan correct
  - `Upsert` étendu : persiste LastHubSyncAt
- `veridian_service_test.go` (compile-check) : nouveau test exposé
- Nouveau fichier dédié `veridian_hub_sync_test.go` :
  - Provision → TouchHubSync appelé (best-effort, fail = log warn)
  - UpdatePlan → idem
  - Suspend → idem
  - Resume → idem
  - SoftDelete → idem
  - Restore → idem
  - Touch → idem
  - AttachOwner → idem
  - AttachMember → idem
  - GrantUnlimited → idem
  - TouchHubSync fail = service mutation réussit quand même (best-effort)
- `veridian_paywall_test.go` :
  - Tenant fresh + write → passe
  - Tenant stale + write → passe + log warn
  - Tenant dead + write → 503 + Retry-After + ErrCodeHubSyncDead
  - Tenant dead + read → passe (best-effort)
  - Tenant dead + route admin Hub → passe (pas de blocage)
  - Tenant dead + déjà soft-deleted → soft-deleted prime (UX cohérent)
  - shouldLogStale rate-limit 1×/min par tenant

### 9. Documentation

Ajouter une section dans CONTRAT-HUB §1.4bis "Billing résilience"
(côté veridian-hub) — **MAIS** : NE PAS éditer le contrat Hub depuis
ce repo. Créer un ticket cross-app dans `veridian-hub/todo/` pour
demander à l'agent Hub de documenter.

---

## Critères de complétion

- [ ] V39 migration + tests + bump config.VERSION + fixture manager_test
- [ ] Domain : VeridianPlan.LastHubSyncAt + 3 constantes + EvaluateHubSyncStatus + tests
- [ ] Repo : TouchHubSync + Get/Upsert étendus + tests sqlmock
- [ ] Service : TouchHubSync appelé sur 10 mutations Hub + tests
- [ ] Middleware paywall : gating 3 phases + ErrCodeHubSyncDead + tests
- [ ] `go build ./...` clean
- [ ] `go test ./internal/...` tous verts
- [ ] `BASE_REF=origin/veridian scripts/ci/check-test-mapping.sh` OK
- [ ] Curl live staging post-deploy : provision tenant → vérifier
  `last_hub_sync_at` set en DB ; simuler 73h artificiellement via
  `UPDATE veridian_plan SET last_hub_sync_at = NOW() - INTERVAL '73 hours'`
  puis tenter un send mail → vérifier 503 ErrCodeHubSyncDead
- [ ] Ticket cross-app déposé dans `veridian-hub/todo/2026-05-21-document-billing-resilience-niveau1.md`

---

## Risques identifiés

1. **Backfill V39** : `UPDATE last_hub_sync_at = updated_at` sous-estime
   pour tenants jamais modifiés post-provision (resteront en stale si
   provision > 24h avant V39 deploy). Acceptable car ils sont peu
   nombreux et le tenant peut "réveiller" la fraîcheur via toute
   mutation Hub (Touch via heartbeat 1×/h).

2. **Tenants provisionnés peu activement** : un tenant qui ne fait
   rien et n'a pas de heartbeat Hub passera en stale après 24h. Mais
   le Hub envoie déjà des Touch via cron 1×/h donc en pratique tous
   les tenants restent fresh. Si Hub Touch cron down spécifiquement
   → c'est exactement ce qu'on veut détecter.

3. **Coexistence avec mode dégradé soft-deleted** : si tenant
   soft-deleted ET HubSyncDead, prioriser soft-deleted (UX cohérent).
   Tester explicitement.

4. **shouldLogStale rate-limit** : map mémoire = perd state au restart
   container. Pas grave (juste un re-log à reboot).

---

## Effort estimé

- 0.5j : migration V39 + domain + repo + tests
- 0.5j : câblage service mutations + tests
- 0.25j : middleware paywall + tests
- 0.25j : curl live staging + cross-app ticket + push
- **Total : ~1.5j**

---

## Notes pour l'agent autonome

- Mode autonome strict : pas d'ouverture sub-agent, pas de questions
  ouvertes à Robert. Si choix UX/produit ambigu (ex: "73h ou 168h pour
  HubSyncDeadThreshold ?"), trancher avec la valeur conservative et
  documenter le choix dans le commit message.
- Tests colocalisés Constitution §1 OBLIGATOIRES à chaque modif.
- Pre-push hook bloquant — JAMAIS `--no-verify`.
- Mockgen v1.6.0 legacy (`github.com/golang/mock`) — regen mock
  VeridianPlanRepository après ajout TouchHubSync.
- Build clean `go build ./...` puis tests `go test ./internal/...
  -count=1 -timeout=120s` avant chaque commit.
- Curl live staging post-deploy = critère de complétion. Si pas
  possible (staging down), documenter dans commit + ticket.
- Au push final : si pre-push hook fail mapping → fix tests, ne
  jamais bypass.

## Référence patterns réutilisables

- V37 (lots pricing-plans-implementation) : pattern ADD COLUMN +
  Upsert/Get étendus
- V38 (lot C trial-eligible-signal) : pattern UPDATE atomique +
  best-effort emit en queue de mutation
- Lot G (admin tenants listing) : pattern `IncludeOrphans` extension
  cohérente d'API existante
