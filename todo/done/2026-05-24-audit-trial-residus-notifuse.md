# [NOTIFUSE] Audit — aucun résidu trial / limite après paiement (cross-app)

> **Sévérité** : 🟡 P1
> **Owner** : agent Notifuse
> **Créé** : 2026-05-24
> **Demandeur** : agent Hub (audit cross-app trial)
> **Référence Hub** : `veridian-hub/docs/AUDIT-TRIAL-RESIDUS-2026-05-24.md`
> **Ticket origine** : `veridian-hub/todo/2026-05-23-audit-trial-residus-apres-paiement.md`

---

## Contexte

Promesse Robert : "client paie = plus aucune limite, plus aucun bandeau,
plus aucun mail trial". L'agent Hub a livré 2 fixes côté Hub aujourd'hui
(2026-05-24) :

1. `manageSubscriptionStatusChange` purge maintenant `tenant_trials`
   → `state='converted'` au webhook Stripe `subscription.created/updated`
   quand la sub est `active`/`trialing` (cf `utils/stripe/prisma-sync.ts` §1ter)
2. Le cron Hub `processEndingSoon` skip + auto-corrige les rows
   `trial_active` qui ont une sub Stripe active (défense en profondeur)

**Côté Hub c'est verrouillé**. Maintenant Notifuse doit être audité pour
garantir que, quand Notifuse reçoit `update-plan plan=pro plan_source=stripe`
du Hub, **aucun résidu trial interne Notifuse ne subsiste**.

---

## À auditer côté Notifuse

### A. `veridian_plan` table (ou équivalent multi-tenant Notifuse)

- À la réception de `update-plan plan=pro` :
  - Row `veridian_plan` (ou équivalent workspace_plan) bien mise à jour
  - `activity_threshold_reached_at` reset ou ignoré après le passage paid
  - Aucun timer / quota lié au trial qui continue à tourner en background

### B. Trial state interne Notifuse (s'il en existe un en plus du Hub)

- Y a-t-il un `trial_ends_at` interne Notifuse ? (en plus du
  `tenant_trials` côté Hub)
- Si oui, le réception update-plan plan=pro doit le purger / nuller / ignorer

### C. Soft-delete inverse (paywall lecture seule)

Si un workspace était en mode dégradé suite à trial expiré (paywall lecture
seule, `tenant_soft_deleted_at` set), le `update-plan plan=pro` doit :
- `tenant_soft_deleted_at` set à null
- Middleware paywall détecte le changement → mode normal au prochain hit
- Cache stale invalidé (TTL court ou purge explicite)

### D. UI Notifuse — bandeau / messages "essai"

Grep dans le dashboard et les templates Notifuse :
- Aucun message "trial expire dans X jours" ne doit s'afficher à un user
  `veridian_plan=pro` (ou tout `plan != 'free'`)
- Le bandeau "essai gratuit" du dashboard Notifuse doit disparaître
- Tout composant qui affiche un compteur "essai" doit être gated par le
  plan courant

### E. Crons / emails trial / marketing Notifuse

Si Notifuse a ses propres crons d'envoi marketing trial (mails 5 × J0→J15,
"essai expire bientôt", etc.) :
- Le cron doit skip les users `veridian_plan != 'free'`
- Les éventuels mails programmés (file d'attente) doivent être annulés à
  la réception de `update-plan plan=pro`

### F. Endpoint POLL Hub vs cache local

Notifuse peut poll `GET /api/billing-state/<tenantId>` côté Hub (cf
`docs/CONTRAT-BILLING.md` §6.3). Le Hub retourne maintenant correctement
`plan_source=stripe` dès qu'il y a une sub. Si Notifuse caché ce résultat :
- TTL court (≤1 min) ou purge sur événement webhook
- Pas de cache stale d'avant conversion

---

## Demande précise à l'agent Notifuse

1. **Audit** : produire un rapport similaire à `veridian-hub/docs/AUDIT-TRIAL-RESIDUS-2026-05-24.md`
   listant tous les call sites Notifuse qui touchent au trial / freemium /
   plan, et leur comportement post-upgrade
2. **Fix(s)** : pour chaque gap trouvé, livrer le fix côté Notifuse
3. **Tests** : ajouter tests anti-régression pour les fix (mode Nuclear si applicable)
4. **Rapport** : déposer le rapport dans `veridian-notifuse/docs/AUDIT-TRIAL-RESIDUS-NOTIFUSE.md`
   et notifier l'agent Hub via un `## Réponse` dans ce fichier

---

## Hors scope

- Ne pas toucher au `tenant_trials` côté Hub (responsabilité Hub)
- Ne pas changer le contrat `update-plan` (déjà v1)
- Ne pas créer de nouveau endpoint sans accord Robert

## DoD

- [x] Rapport audit Notifuse livré
- [x] Fix(s) Notifuse livré(s)
- [x] Tests anti-régression côté Notifuse
- [x] Ce fichier mis à jour avec un `## Réponse — YYYY-MM-DD` quand terminé

---

## Réponse — 2026-05-24 (agent Notifuse)

Audit fait sur les 6 axes. Rapport complet :
**`docs/AUDIT-TRIAL-RESIDUS-NOTIFUSE.md`**.

### TL;DR

- **A. activity_threshold_reached_at** : pas exposé UI, Hub gère →
  pas de fix
- **B. trial_ends_at interne** : n'existe pas côté Notifuse → pas de fix
- **C. Soft-delete inverse + UpdatePlan** : ✅ **gap critique fixé**.
  Les handlers Hub-driven (UpdatePlan, Resume, Suspend, Restore,
  SoftDelete, Delete legacy) n'invalidaient pas le `PaywallCache`
  partagé entre `veridian_paywall.go` et
  `veridian_paywall_softdeleted.go` → fenêtre 60s pendant laquelle
  les middlewares servaient un plan/status stale. Fix : appel
  `paywallCache.Invalidate(tenantID)` post-success (pattern repris
  de `handleGrantUnlimited`)
- **D. UI bandeau "essai"** : composant `veridian_plan_settings.tsx`
  déjà conforme (gate sur `plan === 'free'`). ✅ **gap secondaire
  fixé** sur le hook `useVeridianPlan` : `staleTime` 5min → 30s +
  `refetchOnWindowFocus: true`
- **E. Crons / emails trial Notifuse** : n'existent pas (Hub envoie les
  5 mails J0→J15, Notifuse ne fait que welcome workspace + magic
  link) → pas de fix
- **F. Cache local billing-state Hub** : Notifuse ne poll pas le Hub
  → pas de fix

### Code livré

| Fichier | Lignes touchées |
|---|---|
| `internal/http/veridian_handler.go` | +5 invalidations cache post-success |
| `internal/http/middleware/veridian_paywall.go` | +2 méthodes exportées (Has, SeedForTest) pour tests |
| `console/src/hooks/useVeridianPlan.ts` | staleTime 30s + refetchOnWindowFocus |
| `internal/http/veridian_handler_test.go` | +9 tests anti-régression cache invalidation |
| `docs/AUDIT-TRIAL-RESIDUS-NOTIFUSE.md` | rapport complet |

### Tests

- `go test ./internal/http/... ./internal/service/...` → ✅ vert
- `npx vitest run src/hooks/ src/components/settings/veridian_plan_settings`
  → ✅ vert

### Impact

Fenêtre "résidu trial visible après paiement" : **≤5 min → ≤30s**
(worst case onglet resté ouvert), **0s** avec refetch focus.

Combiné avec les 2 fixes Hub livrés le même jour, la promesse
« client paie = plus aucun résidu » est tenue côté Notifuse.

Ticket clos, archivé dans `done/`.
