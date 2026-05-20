# Implémentation plans Notifuse — Free / Pro / Business / Enterprise

> **Owner** : agent Notifuse
> **Source de vérité** : `../VISION-BUSINESS.md` (racine veridian-platform)
> **Sévérité** : 🔴 P1 — bloque la commercialisation SaaS
> **Effort estimé** : 5-8 jours dev (backend pur + tests)
> **Dépendances** : ticket Hub `2026-XX-XX-trial-state-machine.md` (à créer) pour gestion trial centralisée

> **⚠️ Update 2026-05-20** — Pas de limite **emails/mois** côté Notifuse tant que
> Veridian ne fournit pas son propre provider d'envoi. Le BYO sending fait
> que c'est le provider du client (Gmail/SES/...) qui limite, pas nous.
> Le code a déjà été modifié (PlanQuotas tous à -1, IsBlocked ne check plus
> ce quota). **L'implémentation V37 doit donc PORTER UNIQUEMENT** sur les
> autres dimensions : contacts, seats, oauth, custom domains, sequences,
> A/B, branding, white-label, historique. **Ne PAS recâbler de limite
> emails/mois sauf instruction explicite de Robert (Phase C Resend managé).**

---

## Objectif

Câbler dans le code Notifuse les nouveaux plans tels que décrits dans `VISION-BUSINESS.md` :

| | **Free** | **Pro 29€/mo** | **Business 99€/mo** | **Enterprise** |
|---|---|---|---|---|
| Emails/mois | 300 | 10 000 | 50 000 | Illimité |
| Contacts en base | 500 | 5 000 | 25 000 | Illimité |
| Seats (membres workspace) | 1 | 5 | 25 | Illimité |
| Comptes OAuth (BYO) | 1 | 5 | 25 | Illimité |
| Domaines custom | 0 | 1 | 5 | Illimité |
| Automation sequences actives | 1 | Illimité | Illimité | Illimité |
| A/B testing | ❌ | ✅ | ✅ multi-variant | ✅ |
| Branding "Powered by Veridian" | ✅ obligatoire | ❌ | ❌ + white-label | ❌ |
| Historique data | 30j | 12 mois | Illimité | Illimité |

---

## État actuel du code (reconnaissance terrain à faire)

À vérifier avant de coder, par lecture :
- [ ] `internal/domain/veridian.go` : déjà `PlanQuotas` map avec `free=500, pro=10k, business=50k, enterprise=-1`. **Quota emails actuel = 500 pour Free, à descendre à 300.**
- [ ] `internal/migrations/` : trouver la dernière migration V35-V36, déterminer la prochaine version (V37+ probablement)
- [ ] `internal/http/middleware/veridian_paywall.go` : paywall déjà câblé sur `transactional.send`, `broadcasts.create/schedule/sendToIndividual` (cf. memory `project_email_sending_strategy`)
- [ ] Multi-seat : actuellement **PAS** câblé. Cf. memory `pricing_multi_seat_decision` (Free=1, Pro=5, Business=25 décidé mais non implémenté)
- [ ] Contacts count limit : actuellement aucune limite côté Notifuse (juste DB infinie). À ajouter.
- [ ] A/B testing : check si c'est natif upstream Notifuse, et si oui comment le gater
- [ ] Branding "Powered by Veridian" : check les templates MJML, voir où ajouter le footer obligatoire et comment le retirer pour Pro+
- [ ] Historique : check la table `messages` et autres, voir s'il y a déjà un cron de cleanup

---

## Livrables

### 1. Migration V37 — étendre `veridian_plan` avec les nouvelles dimensions

- [ ] Créer `internal/migrations/v37.go`
- [ ] Ajouter colonnes à `veridian_plan` :
  ```sql
  ALTER TABLE veridian_plan
    ADD COLUMN IF NOT EXISTS max_contacts INTEGER NOT NULL DEFAULT 500,
    ADD COLUMN IF NOT EXISTS max_seats INTEGER NOT NULL DEFAULT 1,
    ADD COLUMN IF NOT EXISTS max_oauth_accounts INTEGER NOT NULL DEFAULT 1,
    ADD COLUMN IF NOT EXISTS max_custom_domains INTEGER NOT NULL DEFAULT 0,
    ADD COLUMN IF NOT EXISTS max_active_sequences INTEGER NOT NULL DEFAULT 1,
    ADD COLUMN IF NOT EXISTS feature_ab_testing BOOLEAN NOT NULL DEFAULT FALSE,
    ADD COLUMN IF NOT EXISTS feature_branding_removed BOOLEAN NOT NULL DEFAULT FALSE,
    ADD COLUMN IF NOT EXISTS feature_white_label BOOLEAN NOT NULL DEFAULT FALSE,
    ADD COLUMN IF NOT EXISTS history_retention_days INTEGER NOT NULL DEFAULT 30;
  ```
  Valeurs par défaut = Free, backfill auto sur les rows existantes.
- [ ] Backfill : `UPDATE veridian_plan SET max_contacts = CASE plan WHEN 'pro' THEN 5000 WHEN 'business' THEN 25000 WHEN 'enterprise' THEN -1 ELSE 500 END` + idem pour les autres colonnes
- [ ] **Migration additive only** (pas de DROP, pas de NOT NULL strict sans default) → safe pour Constitution §12 expand & contract
- [ ] Test mapping : `internal/migrations/v37_test.go`

### 2. Domain : mettre à jour `internal/domain/veridian.go`

- [ ] Étendre la struct `VeridianPlan` avec les nouvelles colonnes
- [ ] Étendre `PlanQuotas` (déjà map int64) en `PlanLimits` struct :
  ```go
  type PlanLimits struct {
      MonthlyEmailQuota   int64
      MaxContacts         int64
      MaxSeats            int
      MaxOAuthAccounts    int
      MaxCustomDomains    int
      MaxActiveSequences  int
      FeatureABTesting    bool
      FeatureBrandingRemoved bool
      FeatureWhiteLabel   bool
      HistoryRetentionDays int
  }

  var DefaultPlanLimits = map[string]PlanLimits{
      "free": {
          MonthlyEmailQuota: 300, MaxContacts: 500, MaxSeats: 1,
          MaxOAuthAccounts: 1, MaxCustomDomains: 0, MaxActiveSequences: 1,
          FeatureABTesting: false, FeatureBrandingRemoved: false,
          FeatureWhiteLabel: false, HistoryRetentionDays: 30,
      },
      "pro": {
          MonthlyEmailQuota: 10000, MaxContacts: 5000, MaxSeats: 5,
          MaxOAuthAccounts: 5, MaxCustomDomains: 1, MaxActiveSequences: -1,
          FeatureABTesting: true, FeatureBrandingRemoved: true,
          FeatureWhiteLabel: false, HistoryRetentionDays: 365,
      },
      "business": {
          MonthlyEmailQuota: 50000, MaxContacts: 25000, MaxSeats: 25,
          MaxOAuthAccounts: 25, MaxCustomDomains: 5, MaxActiveSequences: -1,
          FeatureABTesting: true, FeatureBrandingRemoved: true,
          FeatureWhiteLabel: true, HistoryRetentionDays: -1,
      },
      "enterprise": {
          MonthlyEmailQuota: -1, MaxContacts: -1, MaxSeats: -1,
          MaxOAuthAccounts: -1, MaxCustomDomains: -1, MaxActiveSequences: -1,
          FeatureABTesting: true, FeatureBrandingRemoved: true,
          FeatureWhiteLabel: true, HistoryRetentionDays: -1,
      },
  }
  ```
- [ ] Helper `LimitsForPlan(plan string) PlanLimits` (fallback free)
- [ ] **PRÉSERVER** rétrocompat : garder `PlanQuotas` map ou la déprécier proprement avec build tag
- [ ] Tests `internal/domain/veridian_test.go` : couvrir les nouvelles dimensions + cas plan inconnu

### 3. Repository : étendre `veridian_plan_postgres.go`

- [ ] Étendre `Get()` pour lire les nouvelles colonnes
- [ ] Étendre `Upsert()` et `UpdatePlan()` pour écrire les nouvelles colonnes
- [ ] Quand le Hub appelle `update-plan` avec juste `plan="pro"`, le repo doit auto-appliquer les `DefaultPlanLimits["pro"]` SAUF si des `quotas` custom sont fournis (le Hub peut override pour des deals custom)
- [ ] **Attention** : drift PostgreSQL types (cf. memory `sqlmock_does_not_validate_postgres_types`). Tester avec curl live post-deploy.
- [ ] Tests `internal/repository/veridian_plan_postgres_test.go` : couvrir tous les nouveaux setters

### 4. Service : étendre `veridian_service.go`

- [ ] `Provision()` : applique `DefaultPlanLimits[input.Plan]` à la création
- [ ] `UpdatePlan()` : pareil, en remplaçant les valeurs existantes
- [ ] Nouveaux helpers exposés :
  - `func (s *VeridianService) GetLimits(ctx, tenantID) (PlanLimits, error)` — lit le plan + retourne les limites effectives
  - `func (s *VeridianService) CanAddSeat(ctx, tenantID) (bool, error)` — true si seats_used < max_seats
  - `func (s *VeridianService) CanAddContact(ctx, tenantID, delta int64) (bool, error)` — true si contacts_count + delta <= max_contacts
  - `func (s *VeridianService) CanAddOAuthAccount(ctx, tenantID) (bool, error)`
  - `func (s *VeridianService) CanAddCustomDomain(ctx, tenantID) (bool, error)`
- [ ] Tests : 1 test par helper, plus tests UpdatePlan avec et sans quotas custom

### 5. Middlewares — gating des features

- [ ] Étendre `internal/http/middleware/veridian_paywall.go` :
  - Sur `POST /api/users.invite` ou équivalent : refuser 402 si `seats_used >= max_seats` avec error_code `seat_limit_reached`
  - Sur `POST /api/contacts.create` ou `POST /api/contacts.import` : refuser 402 si dépassement `max_contacts` avec error_code `contact_limit_reached`
  - Sur `POST /api/automation.activate` : refuser 402 si `active_sequences >= max_active_sequences` avec error_code `sequence_limit_reached`
- [ ] Nouveau middleware `veridian_feature_gate.go` :
  - Sur les endpoints A/B testing : refuser 402 si `!feature_ab_testing` avec error_code `feature_not_in_plan`
  - Sur les endpoints custom domain : refuser 402 si `max_custom_domains == 0` ou `domains_count >= max_custom_domains` avec error_code `custom_domain_limit_reached`
- [ ] Tests : 1 par middleware, couvrir le cas limite (== max) et au-delà

### 6. Branding "Powered by Veridian"

- [ ] Identifier le template MJML de base ou le post-processing email qui injecte le footer
- [ ] Si pas existant : créer un middleware sur l'envoi qui appende `<mj-section background-color="#f5f5f5"><mj-column><mj-text font-size="11px" color="#888" align="center">Sent with <a href="https://veridian.site">Veridian</a></mj-text></mj-column></mj-section>` AVANT envoi si `!feature_branding_removed`
- [ ] Pour `feature_white_label` (Business+) : permettre au tenant de configurer son propre footer custom dans Settings
- [ ] Tests : 3 cas (free → footer, pro → no footer, business white-label → footer custom)

### 7. Cron de cleanup historique

- [ ] Nouveau cron `internal/cron/history_retention_cleanup.go` :
  - Tourne 1x/jour
  - Pour chaque tenant, lit `history_retention_days`
  - Supprime de `messages`, `message_history`, etc. les rows > retention
  - -1 = no cleanup (Business+)
- [ ] Test cron : seed data avec timestamps variés, run, vérifier purge
- [ ] Documenter dans `runbooks/` ou CHANGELOG

### 8. Endpoint exposé pour la console UI

- [ ] Nouvel endpoint `GET /api/veridian/limits` (auth JWT user) — renvoie les limites + usage actuel pour le workspace courant :
  ```json
  {
    "plan": "pro",
    "limits": { "max_seats": 5, "max_contacts": 5000, ... },
    "usage": { "seats_used": 2, "contacts_count": 1247, "emails_sent_this_month": 3421, ... },
    "features": { "ab_testing": true, "branding_removed": true, "white_label": false }
  }
  ```
- [ ] Permet à la console UI d'afficher les widgets quota (cf. `review_hot_reload_with_robert.md` §4)
- [ ] Tests handler

### 9. Documentation

- [ ] Mettre à jour `CHANGELOG.md` (version V37) avec breaking changes (aucun normalement, additif)
- [ ] Documenter dans `README.md` ou `docs/plans.md` la table des plans avec leurs limites
- [ ] Référencer `../VISION-BUSINESS.md` comme source de vérité

---

## Hors scope de ce ticket (autres tickets)

- **Trial state machine** (signal d'activité → bandeau → expiration → soft-delete) : ticket Hub dédié `2026-XX-XX-trial-state-machine.md`
- **UI bandeau trial + paywall obfuscation** : tickets UI séparés (cf. `review_hot_reload_with_robert.md` et `paywall-obfuscation-degrade.md`)
- **Stripe products + webhooks** : ticket Hub `2026-XX-XX-stripe-products-creation.md`
- **Multi-seat côté workspace_members management** : ticket Hub `2026-05-19-v13-multi-membre-cross-app.md` déjà existant — coordonner pour ne pas dupliquer la logique seats
- **Implémentation Prospection** : ticket parallèle `veridian-prospection/todo/2026-05-20-pricing-plans-implementation.md`

---

## Risques identifiés

1. **Multi-seat conflit avec v13-multi-membre-cross-app** : le ticket Hub `v13-multi-membre` couvre déjà la propagation des seats Hub → Notifuse. Cette implémentation Notifuse doit juste lire `max_seats` du plan local et ne pas réinventer la propagation. **Coordination obligatoire** avec agent Hub avant de coder le seat enforcement.
2. **Contacts count performance** : compter `SELECT COUNT(*) FROM contacts WHERE workspace_id = ?` à chaque insert est coûteux. **Mitigation** : compteur dénormalisé `veridian_plan.contacts_count` mis à jour via trigger ou increment service, refresh nightly pour audit.
3. **A/B testing gating** : si la feature A/B est partiellement câblée upstream Notifuse, on peut bloquer un endpoint mais laisser fuir via un autre. **Mitigation** : audit complet des endpoints touchés par A/B avant de gater.
4. **Backfill historique > 30j pour les Free actuels** : si on déploie le cron cleanup en l'état, on supprime potentiellement des data de tenants Free existants qui ne s'attendent pas à ça. **Mitigation** : grace period 30 jours après le déploiement avant que le cron commence à supprimer. Ajouter une migration `INSERT INTO veridian_plan_cleanup_grace` qui marque les tenants existants.
5. **Drift Postgres types** (cf. memory `sqlmock_does_not_validate_postgres_types`) : nouvelles colonnes INTEGER vs BIGINT, BOOLEAN vs SMALLINT, etc. → toujours curl live post-deploy sur les endpoints qui touchent les nouvelles colonnes.

---

## Plan d'attaque suggéré

1. **Jour 1** : reconnaissance terrain complète + migration V37 + domain
2. **Jour 2-3** : repository + service + helpers + tests
3. **Jour 4** : middlewares paywall étendus + feature gate
4. **Jour 5** : branding "Powered by Veridian" + endpoint `/api/veridian/limits`
5. **Jour 6** : cron cleanup historique
6. **Jour 7** : doc + CHANGELOG + curl live tests post-deploy staging
7. **Jour 8** : tests E2E + bug fix + promote prod

**Trunk-based discipline** (cf. CLAUDE.md racine §0) : tout push direct sur `veridian`, auto-promote staging → main si CI vert, pas de PR.

---

## Status

### ✅ Lot 1 livré 2026-05-20 (commit `a260adcf`, v37.0-veridian.a260adcf)

- [x] Reconnaissance terrain faite
- [x] Migration V37 shippée — 9 colonnes ADD + backfill par plan, idempotente. Verifie en prod : free=500/1, pro=5000/5, enterprise=-1/-1.
- [x] Domain étendu + tests — `PlanLimits` struct, `DefaultPlanLimits` map, `LimitsForPlan(plan)` helper avec fallback Free safe. 7 tests verts.
- [x] Bump VERSION 35.0 → 37.0 (V36 reservee au ticket aligner-types-timestamp)
- [x] Curl live tests post-deploy staging+prod OK (`/api/version` retourne tag v37, colonnes confirmees en prod via psql sur notifuse_system)

### ✅ Lot 2 livré 2026-05-20 (commit `7e028c1b` → rejoue dans c85ffd28)

- [x] Repo `veridian_plan_postgres.go` étendu Get/Upsert/UpdatePlan
- [x] `Get` SELECT et scan vers les 9 nouveaux champs de VeridianPlan
- [x] `Upsert` INSERT 21 params avec auto-fill via `domain.LimitsForPlan(plan)`
  si dimensions toutes à zéro (cas Provision standard) — sinon respecte
  les overrides custom (deal Enterprise hors-grille). `ON CONFLICT DO
  UPDATE` ne touche PAS les dimensions V37 (un Upsert idempotent ne doit
  pas régresser un Pro vers Free silencieusement).
- [x] `UpdatePlan` applique `LimitsForPlan(plan)` au changement de plan →
  upgrade/downgrade re-applique les limites du nouveau tier. Fallback
  Free safe sur plan inconnu (no privilege escalation).
- [x] Helpers internes `isZeroPricingDimensions` + `applyDefaultLimits`
- [x] Tests : `Get` 3 sous-tests + `Upsert` 4 sous-tests (avec custom
  override) + `UpdatePlan` 6 sous-tests + `PreservesSourceOnEmpty` +
  garde-fou V34 lifecycle scan. Tous verts.

### ✅ Lot 3 livré 2026-05-20 (commit `1309def0` → rejoue dans c85ffd28)

- [x] `domain.LimitsResponse` struct + `VeridianService.GetLimits` interface
- [x] `service.GetLimits` lit `planRepo.Get`, expose limites DB telles
  quelles. Fallback safe `LimitsForPlan(p.Plan)` si toutes dimensions
  à zéro (row antédiluvien jamais re-upsert post-V37). Fallback Free
  strict sur plan inconnu.
- [x] Mock `MockVeridianService.GetLimits` ajouté (mockgen v1.6.0 legacy,
  extension manuelle).
- [x] `Provision` et `UpdatePlan` côté service : NON modifiés (le lot 2
  câble déjà l'auto-application au niveau du repo).
- [x] Tests service (5) + domain `LimitsResponse` JSON schema + interface
  expose `GetLimits`. Tous verts.

### ✅ Lot 7 livré 2026-05-20 (commit `c85ffd28`)

- [x] Handler `GET /api/tenants/{id}/limits` HMAC dans `veridian_handler.go`
- [x] Mappe `sql.ErrNoRows` → 404, autres erreurs → 500
- [x] Pattern strictement copié de `handleHealth` (cohérence contrat Hub)
- [x] Tests handler (5) : OK / MissingID / NotFound / InternalError /
  RegisteredInRoutes (garde-fou anti-suppression)
- [x] **Note routage** : ticket original proposait `GET /api/veridian/limits`
  (sans tenant_id). Choix actuel `GET /api/tenants/{id}/limits` pour
  cohérence avec `/status` `/health` `/usage-summary` + HMAC strict.

### ✅ Lot 4a livré 2026-05-21 (commit `f08410cd`) — Feature gate A/B testing

- [x] Nouveau middleware `NewVeridianFeatureGateMiddlewareWithCache`
  partageant le `PaywallCache` 60s existant.
- [x] Map `featureGatedPaths` :
  - `/api/broadcasts.getTestResults` → ab_testing
  - `/api/broadcasts.selectWinner` → ab_testing
- [x] Helper `checkFeatureAllowed(plan, feature)` avec switch ab_testing/
  branding_removed/white_label. Fail-open sur plan nil ou feature inconnue.
- [x] `VeridianPaywallPathFilterWithCache` route les paths gated vers le
  feature gate (avant le passe-direct).
- [x] Stratégie fail-open cohérente avec paywall existant : tenant
  non-Veridian / erreur DB transitoire → laisse passer.
- [x] Réponse 402 avec `error_code: "feature_not_in_plan"`, `feature`,
  `tenant_plan` dans le body JSON.
- [x] 10 tests verts couvrant chemins nominaux + edge cases + path filter.

**Choix design** : gater les endpoints "A/B only" plutôt que de parser
le body de `broadcasts.create` pour détecter `test_settings`. Plus
simple, plus robuste, business-équivalent.

### ⏳ Lots restants (à arbitrer avec Robert avant de coder)

- [ ] **Lot 4b** — Seat enforcement sur `/api/user.add` ou équivalent.
  **Question design** : où compter les seats ? `workspace_users.WHERE
  workspace_id=X AND type='user'` (excluant les api_keys) — à
  confirmer.
- [ ] **Lot 4c** — Contact count enforcement sur `/api/contacts.upsert`
  et `/api/contacts.import`. **Question design** : `SELECT COUNT(*)` à
  chaque insert = coûteux. Alternative : compteur dénormalisé
  `veridian_plan.contacts_count` mis à jour via trigger ou increment
  service, refresh nightly pour audit.
- [ ] **Lot 4d** — Custom domain enforcement (`feature_white_label` ou
  `MaxCustomDomains > 0`).
- [ ] **Lot 5** — Branding "Powered by Veridian". **Question design** :
  footer MJML ajouté côté serveur dans `EmailService.SendEmailForTemplate`
  juste avant le `providerRequest` (besoin d'ajouter `planRepo` en
  dépendance à `EmailService`) OU modifier les templates Veridian dans
  la console (UX différente, pas de modif code).
- [ ] **Lot 6** — Cron cleanup historique (`history_retention_days`).
  **Question design** : DELETE direct dans `messages` / `message_history`
  par workspace_id ou soft-delete avec audit ? Chaque tenant ayant sa
  propre DB Postgres, le cron doit itérer sur tous les tenants Veridian.
- [ ] **Lot 8** — Documentation CHANGELOG + README. Note : CHANGELOG
  est upstream-aligned, peut-être préférable de documenter dans CLAUDE.md
  ou dans un fichier dédié `VERIDIAN-PRICING-V37.md`.

**Note lot 1** : choix volontairement non-régressif — fondation seule.
**Note lot 2** : auto-fill côté repo = lots 1+2 deviennent transparents
pour le Provision/UpdatePlan côté service, qui profitent gratuitement.
**Note lot 3** : GetLimits lit le repo, fallback safe pour les rows
antédiluviens. Surface API stable consommable par middleware + UI + Hub.
**Note lot 7** : endpoint /limits expose la primitive aux callers.
