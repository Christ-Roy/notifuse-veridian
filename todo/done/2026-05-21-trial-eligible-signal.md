# [NOTIFUSE] Signal d'éligibilité trial — compteur lifetime + webhook activity_threshold_reached

> **Type** : Backend feature — émission de signal métier vers Hub
> **Sévérité** : 🟡 P1 — bloque le trial intelligent (cf. ticket Hub
> `2026-05-21-trial-state-machine.md`)
> **Owner** : agent Notifuse
> **Créé** : 2026-05-21
> **Dépendances** :
> - Lot 1-7 V37 pricing déjà livrés (fondation OK)
> - Hub ticket `2026-05-21-trial-state-machine.md` (consommateur du webhook)

---

## Vision business

Robert veut un **trial intelligent** : pas de trial offert au signup
(spam) ni au 1er mail envoyé (curieux qui disparaît). Le trial Pro 15j
ne démarre que **2 jours après l'activation réelle** = quand le user a
prouvé qu'il utilise vraiment Notifuse en envoyant **5 mails**.

Le **Hub orchestre le trial** (state machine 2j → 15j → downgrade)
parce qu'il doit aussi gérer le Stripe customer cross-app. Notifuse
n'a qu'une responsabilité : **détecter le seuil 5 mails et le crier au
Hub via webhook**.

Cf. décision archi 2026-05-21 — section "Stripe → Hub → app" du brief
Robert : Notifuse reste passif sur le pricing, émet des signaux
d'engagement métier (5 mails envoyés), le Hub décide.

---

## Périmètre Notifuse uniquement

Ce ticket **ne fait pas** :
- ❌ Démarrer le trial (= responsabilité Hub)
- ❌ Compter les 2 jours d'attente (= responsabilité Hub)
- ❌ Compter les 15 jours de trial (= responsabilité Hub)
- ❌ Downgrade à expiration (= responsabilité Hub via `update-plan`)

Ce ticket **fait uniquement** :
- ✅ Compteur lifetime `emails_sent_lifetime` qui ne reset jamais
- ✅ Détection du franchissement du seuil 5
- ✅ Émission webhook `tenant.activity_threshold_reached` vers Hub (1 fois)
- ✅ Idempotent : si on relance Notifuse, le webhook n'est pas ré-émis

---

## Livrables

### 1. Migration V38 — colonnes lifetime + threshold tracking

Fichier : `internal/migrations/v38.go` + `v38_test.go`

Colonnes additives sur `veridian_plan` :

```sql
ALTER TABLE veridian_plan
  ADD COLUMN IF NOT EXISTS emails_sent_lifetime BIGINT NOT NULL DEFAULT 0,
  ADD COLUMN IF NOT EXISTS activity_threshold_reached_at TIMESTAMP WITH TIME ZONE;
```

- `emails_sent_lifetime` : compteur cumulatif jamais reset (vs
  `emails_sent_this_month` qui reset par cron).
- `activity_threshold_reached_at` : timestamp du franchissement du seuil.
  NULL tant que le tenant n'a pas envoyé 5 mails. Set une seule fois,
  jamais re-écrit après (idempotence du webhook).

Backfill : `UPDATE veridian_plan SET emails_sent_lifetime =
emails_sent_this_month`. Approximation safe (sous-estime pour les
tenants qui ont reset au moins une fois — pas grave, seuil 5 facile
à atteindre de toute façon).

### 2. Domain — étendre `VeridianPlan` + constante seuil

Fichier : `internal/domain/veridian.go` + `veridian_test.go`

```go
type VeridianPlan struct {
    // ... champs existants ...
    EmailsSentLifetime         int64      `json:"emails_sent_lifetime"`
    ActivityThresholdReachedAt *time.Time `json:"activity_threshold_reached_at,omitempty"`
}

// ActivityThresholdEmails est le nombre de mails envoyés cumulés qui
// déclenche l'émission du webhook tenant.activity_threshold_reached.
// Valeur business : un user qui envoie 5 mails est "activé" = candidat
// trial. Constante exportée pour que les tests Hub puissent assert.
const ActivityThresholdEmails int64 = 5
```

Nouvel event :

```go
const EventTenantActivityThresholdReached VeridianEvent = "tenant.activity_threshold_reached"
```

### 3. Repository — incrémenter lifetime + détection seuil

Fichier : `internal/repository/veridian_plan_postgres.go` + test

**Étendre `IncrementEmailsSent`** pour incrémenter aussi `emails_sent_lifetime`
(atomique, même UPDATE) :

```sql
UPDATE veridian_plan
SET emails_sent_this_month = emails_sent_this_month + $2,
    emails_sent_lifetime = emails_sent_lifetime + $2,
    updated_at = $3
WHERE workspace_id = $1
RETURNING emails_sent_lifetime, activity_threshold_reached_at;
```

Le `RETURNING` permet à l'appelant (decorator message history) de
détecter si on vient de franchir le seuil **sans second roundtrip DB**.

**Nouvelle méthode** `MarkActivityThresholdReached(ctx, workspaceID, at time.Time) error` :

```sql
UPDATE veridian_plan
SET activity_threshold_reached_at = $2, updated_at = $2
WHERE workspace_id = $1 AND activity_threshold_reached_at IS NULL;
```

Idempotent par construction : si le champ est déjà set, le WHERE filtre
et le UPDATE retourne 0 rows affected (no-op silencieux).

**Étendre `Get` et `Upsert`** pour persister les 2 nouveaux champs (cf.
pattern du lot 2 V37).

### 4. Service — détection + émission webhook

Fichier : `internal/service/veridian_service.go` + test

**Modifier** `IncrementEmailsSent` (qui pour l'instant délègue au repo) :

```go
func (s *veridianService) IncrementEmailsSent(ctx context.Context, workspaceID string, delta int64) error {
    lifetimeAfter, alreadyReached, err := s.planRepo.IncrementEmailsSentReturning(ctx, workspaceID, delta)
    if err != nil {
        return err
    }
    // Si on vient de franchir le seuil et qu'on n'a jamais émis l'event
    if !alreadyReached && lifetimeAfter >= domain.ActivityThresholdEmails {
        now := time.Now().UTC()
        if err := s.planRepo.MarkActivityThresholdReached(ctx, workspaceID, now); err != nil {
            // Log warn, mais ne bloque pas l'envoi — best-effort
            s.logger.WithFields(...).Warn(...)
            return nil
        }
        if s.emitter != nil {
            s.emitter.Emit(ctx, domain.EventTenantActivityThresholdReached, workspaceID, map[string]interface{}{
                "emails_sent_lifetime": lifetimeAfter,
                "threshold":            domain.ActivityThresholdEmails,
                "reached_at":           now.Format(time.RFC3339),
            })
        }
    }
    return nil
}
```

**Best-effort sur emit failure** : si Hub down, on a quand même marqué
`activity_threshold_reached_at` côté Notifuse → audit trail. Le Hub
pourra reconcile via le ticket dédié (cf. §6 ci-dessous).

### 5. Décorateur message history — déjà câblé, juste vérifier

Le décorateur `internal/repository/veridian_message_history_decorator.go`
(livré 2026-05-20) appelle déjà `IncrementEmailsSent(ctx, ws, 1)` à
chaque envoi. Après ce ticket, **rien à modifier dans le décorateur** :
l'incrément lifetime + détection seuil + emit se font tous dans le
service `IncrementEmailsSent`.

À vérifier en test E2E : envoyer 5 mails sur un tenant Free → webhook
émis sur le 5ème → 6ème mail = pas de re-émission.

### 6. Endpoint reconcile (optionnel — pour le Hub) — Lot ultérieur

**Pas dans ce ticket.** Si le Hub rate l'event webhook (incident transitoire),
il peut interroger `GET /api/tenants/{id}/limits` qui expose déjà toutes
les dimensions. Ajouter au response existant les 2 nouveaux champs :

```json
{
  "tenant_id": "client42",
  "plan": "free",
  "limits": { ... },
  "activity": {
    "emails_sent_lifetime": 7,
    "activity_threshold_reached_at": "2026-05-22T09:14:23Z"
  },
  "generated_at": "..."
}
```

Permet au Hub de cron reconcile : "ces 20 tenants ont leurs `_lifetime
≥ 5` mais je n'ai jamais reçu de webhook activity_threshold_reached
→ je démarre le trial timer". Robuste en cas d'incident.

### 7. Tests

- Migration V38 : pattern V37 (helper ExpectExec ADD COLUMN + backfill UPDATE)
- Domain : `ActivityThresholdEmails == 5`, struct contains les champs
- Repo : `IncrementEmailsSentReturning` increments both counters + returns
  `(lifetime_after, already_reached)`. `MarkActivityThresholdReached`
  idempotent (2 appels successifs = 1 UPDATE effectif).
- Service : 5ème mail trigger emit. 6ème mail = pas d'emit (déjà
  marqué). Emit failure = best-effort (log + return nil). Cumul lifetime
  > seuil mais déjà marqué = pas d'emit.

---

## Risques identifiés

1. **Backfill imprécis** : `UPDATE emails_sent_lifetime = emails_sent_this_month`
   sous-estime pour les tenants qui ont vu un reset mensuel. Conséquence :
   un tenant qui a déjà envoyé 50 mails ce mois-ci aurait un lifetime à 50
   alors qu'il devrait être à 50+vieux mails. Pas un problème pour le seuil
   5 (trivialement atteint), mais ça fausse les stats lifetime. **Mitigation** :
   si on veut être précis, on peut faire un script one-shot qui scanne
   `message_history` par workspace_id, ou simplement accepter que le
   `_lifetime` démarre à la migration. Décision : on accepte (cas business
   pas critique).

2. **Race condition double increment** : 2 envois simultanés peuvent
   tous les deux voir `_lifetime=4` avant le commit. **Pas un problème** :
   le UPDATE est atomique (`SET _lifetime = _lifetime + 1`), donc 2
   updates concurrents donnent 6 final. Le service détecte le franchissement
   sur le `_lifetime` retourné par RETURNING, donc les 2 goroutines voient
   chacune un seuil franchi. **Le `MarkActivityThresholdReached` est
   idempotent** (WHERE `activity_threshold_reached_at IS NULL` filtre), donc
   un seul UPDATE marque, l'autre est no-op. Le webhook est émis 2 fois →
   le Hub doit le déduper sur `idempotency_key` qui existe déjà dans le
   payload V37.

3. **Webhook Hub down** : si Hub down au moment du 5ème mail, l'event est
   perdu (le `WebhookEmitter` actuel ne queue pas). Le tenant a son champ
   `activity_threshold_reached_at` set en DB → réconciliable via §6.

---

## Plan d'attaque

1. Migration V38 + test
2. Domain + tests (constante, event)
3. Repo + tests (sqlmock SELECT/INSERT/UPDATE étendus)
4. Service + tests (mocking emitter, vérif appel unique)
5. Curl live post-deploy : envoyer 5 mails sur canaryfree via API,
   tail logs prod, vérifier emit
6. Endpoint reconcile (§6) — laisser pour une session ultérieure ou
   inclure si <30min

---

## Status

- [ ] Migration V38 + test
- [ ] Domain `VeridianPlan` étendu + constante `ActivityThresholdEmails`
- [ ] Event `EventTenantActivityThresholdReached`
- [ ] Repo `IncrementEmailsSentReturning` + `MarkActivityThresholdReached`
- [ ] Service détection + emit
- [ ] Tests tous verts
- [ ] Curl live test 5 mails sur canaryfree → webhook reçu côté Hub
- [ ] Endpoint reconcile (lot ultérieur)
