# Audit — Résidus trial Notifuse après paiement (2026-05-24)

> **Demandeur** : agent Hub (ticket `todo/2026-05-24-audit-trial-residus-notifuse.md`)
> **Référence Hub** : `veridian-hub/docs/AUDIT-TRIAL-RESIDUS-2026-05-24.md`
> **Auteur** : agent Notifuse
> **Statut** : ✅ Audit complet, 2 fixes livrés, 9 tests anti-régression

---

## Contexte

Promesse Robert : « client paie = plus aucune limite, plus aucun bandeau,
plus aucun mail trial ». Le Hub a verrouillé son côté trial le 2026-05-24
(purge `tenant_trials` au webhook Stripe + cron auto-correct). Il fallait
auditer Notifuse pour garantir qu'aucun résidu interne ne subsiste après
réception de `update-plan plan=pro plan_source=stripe`.

## Méthode

Reconnaissance terrain sur 6 axes (cf. ticket Hub) :

| Axe | Question |
|---|---|
| A | `activity_threshold_reached_at` reset/ignoré après passage paid ? |
| B | Existe-t-il un `trial_ends_at` interne Notifuse en plus du Hub ? |
| C | Soft-delete inverse propre quand le tenant repaie ? |
| D | UI bandeau / messages "essai" gated par le plan courant ? |
| E | Crons / emails marketing trial côté Notifuse ? |
| F | Cache local d'un poll `/api/billing-state` Hub ? |

## Résultats axe par axe

### A. `activity_threshold_reached_at` — ✅ OK, pas de fix nécessaire

**Constat** : Le champ V38 est utilisé exclusivement côté backend Go
(`internal/repository/veridian_plan_postgres.go` + emit webhook). Il
n'est **pas exposé** dans une route consommée par le front Notifuse.

Le timestamp peut rester populé après upgrade sans aucune nuisance
visible. Le Hub fait sa réconciliation côté trial state machine
(`processEndingSoon` skip + auto-corrige les rows `trial_active` qui
ont une sub Stripe active).

**Décision** : ne pas le reset à l'`UpdatePlan`. Le reset ajouterait
une UPDATE inutile à chaque mutation Hub et il n'y a aucune
conséquence observable. Conserver le timestamp pour audit historique.

### B. `trial_ends_at` interne Notifuse — ✅ Inexistant

**Constat** : `grep -rn "trial_ends_at|TrialEndsAt"` sur `internal/` et
`console/src/` ne retourne **aucune occurrence**. Le concept de fin de
trial n'existe que côté Hub (`tenant_trials`).

Notifuse ne stocke aucun état trial propre. Toute notion temporelle
trial vient du Hub via `update-plan` (downgrade automatique à
expiration). **Aucun fix nécessaire**.

### C. Soft-delete inverse — ❌ Gap critique trouvé et fixé

**Constat** : 2 middlewares globaux (`veridian_paywall.go` +
`veridian_paywall_softdeleted.go`) utilisent un `PaywallCache` partagé
avec TTL 60s pour éviter un lookup DB par requête.

`service.Restore` clear `deleted_at` en DB, mais aucun call site
n'invalidait le `PaywallCache` après la mutation. Conséquence : le
middleware soft-deleted continuait à servir l'ancien `plan.DeletedAt
!= nil` jusqu'à 60s — donc :
- les **writes restaient bloqués 402 `tenant_soft_deleted`** alors que
  la DB disait "actif"
- les **reads restaient obfusqués** (33% en clair + `•••`) alors que le
  tenant venait d'être restauré

C'est **exactement le scénario décrit par le ticket Hub** : trial
expiré → Hub a soft-deleted → user paie → Hub send Restore → fenêtre
de 60s avant que l'UI ne redevienne normale.

**Idem pour `UpdatePlan`** : un tenant qui paie passait `plan=free →
pro`, mais le cache retournait encore `plan.Plan = "free"` pendant
≤60s → certains middlewares prenaient des décisions sur le mauvais
plan.

**Fix appliqué** : invalidation explicite du cache après succès dans
les handlers concernés, suivant le pattern déjà en place dans
`handleGrantUnlimited`.

| Handler | Fix |
|---|---|
| `handleUpdatePlan` | `h.paywallCache.Invalidate(input.TenantID)` post-success |
| `handleResume` | Idem (débloquer writes immédiatement post-Resume) |
| `handleSuspend` | Idem (bloquer writes immédiatement post-Suspend, sécurité) |
| `handleRestore` | Idem (**critique** — inverse soft-deleted) |
| `handleSoftDelete` | Idem (cohérence cross-route avec write 402) |
| `handleDelete` (legacy) | Idem (la route DELETE délègue à SoftDelete) |

Garde-fous :
- Tous les call sites font `if h.paywallCache != nil` (mode self-hosted
  sans middleware paywall reste passthrough sans panic)
- L'invalidation **ne se fait QUE en cas de succès du service**
  (sentinels `ErrPlanImmune` / `ErrTenantNotSoftDeleted` n'invalident
  rien — la DB n'a pas changé)

### D. UI bandeau "essai" — ✅ Conforme + fix front pour fenêtre cache

**Constat** : Un seul composant UI affiche le mot "trial" :
`console/src/components/settings/veridian_plan_settings.tsx`. La
fonction `planLabel()` retourne `"Free — unlimited access during your
15-day trial"` **uniquement** si `plan === 'free'` ou `undefined`.
Pour `plan === 'pro' | 'business' | 'enterprise'`, le label devient
simplement "Pro" / "Business" / "Enterprise". **Aucun bandeau trial
résiduel**.

**Gap secondaire trouvé** : Le hook `useVeridianPlan` avait
`staleTime: 5 * 60 * 1000` (5 min) **sans `refetchOnWindowFocus`**.
Donc même si le backend invalidait son cache (fix C), le front gardait
"Free — 15-day trial" pendant 5 min si l'utilisateur restait sur
l'onglet (ou jusqu'au prochain refresh manuel).

**Fix appliqué** dans `console/src/hooks/useVeridianPlan.ts` :
- `staleTime` réduit à `30 * 1000` (30s)
- `refetchOnWindowFocus: true` activé

Conséquence : si l'utilisateur revient sur l'onglet après l'upgrade, le
plan se refetch immédiatement. Sinon, écart max 30s entre DB et UI.

### E. Crons / emails marketing trial — ✅ N/A côté Notifuse

**Constat** : Notifuse n'envoie **aucun email marketing trial** à ses
propres users. Les 5 mails trial Free (J0→J15) sont envoyés
**exclusivement par le Hub** côté Veridian (cf. `veridian-hub` trial
state machine v1.4 livrée 2026-05-21).

Notifuse n'envoie que :
- des emails opérationnels (welcome workspace, magic link auto-login)
- les emails que les **clients finaux** envoient via Notifuse à leurs
  propres contacts (hors-scope trial)

**Aucun fix nécessaire**. Aucun cron à modifier.

### F. Cache local `/api/billing-state` — ✅ N/A

**Constat** : `grep -rn "billing-state|billingState"` sur `internal/`
et `console/src/` ne retourne **aucune occurrence**. Notifuse ne poll
**pas** l'endpoint Hub `GET /api/billing-state`. Sa source de vérité
plan/status est :
- la table locale `veridian_plan` (mise à jour via `update-plan`
  Hub → Notifuse)
- le cache local `PaywallCache` (fixé en §C)

**Aucun fix nécessaire**.

---

## Synthèse des fixes livrés

### Code

| Fichier | Changement |
|---|---|
| `internal/http/veridian_handler.go` | +5 invalidations `paywallCache.Invalidate` post-success (UpdatePlan, Suspend, Resume, SoftDelete, Restore, Delete legacy) |
| `internal/http/middleware/veridian_paywall.go` | + méthodes exportées `Has(workspaceID)` et `SeedForTest(workspaceID)` pour les tests anti-régression |
| `console/src/hooks/useVeridianPlan.ts` | `staleTime` 5min → 30s, `refetchOnWindowFocus: true` |

### Tests (`internal/http/veridian_handler_test.go`)

9 tests anti-régression ajoutés :

- `TestVeridianHandleUpdatePlan_InvalidatesCacheOnSuccess`
- `TestVeridianHandleUpdatePlan_DoesNotInvalidateOnError`
- `TestVeridianHandleSuspend_InvalidatesCacheOnSuccess`
- `TestVeridianHandleResume_InvalidatesCacheOnSuccess`
- `TestVeridianHandleDelete_InvalidatesCacheOnSuccess`
- `TestVeridianHandleSoftDelete_InvalidatesCacheOnSuccess`
- `TestVeridianHandleRestore_InvalidatesCacheOnSuccess` (cas critique §C)
- `TestVeridianHandleRestore_DoesNotInvalidateOnError`
- `TestVeridianHandle_CacheInvalidationGracefulWithoutCache` (mode self-hosted sans cache)

Tous verts (`go test ./internal/http/... ./internal/service/...`).
Suite front Vitest sur le hook + composant settings également verte.

---

## Impact sur la promesse Robert

| Avant | Après |
|---|---|
| Tenant restored → 60s d'écart middleware ↔ DB | Invalidation immédiate, cohérence DB ↔ middleware sub-100ms |
| UI : bandeau "Free — 15-day trial" 5 min post-upgrade | Bandeau disparaît max 30s + refetch au focus |
| Risque write bloqué 402 sur tenant Pro frais | Risque éliminé |
| Risque read obfusqué sur tenant restored | Risque éliminé |

La fenêtre de "résidu trial visible après paiement" est ramenée de
**≤5 min** (worst case onglet ouvert) à **≤30s** (avec refetch
instant au refocus). Combiné avec les 2 fixes Hub livrés le même jour,
le contrat « client paie = plus aucun résidu » est tenu.

---

## Hors scope conservé

- Pas touché à `tenant_trials` côté Hub (responsabilité Hub)
- Pas modifié le contrat `update-plan` (toujours v1/v2)
- Pas créé de nouveau endpoint
- `activity_threshold_reached_at` non reset (signal historique inoffensif)
