# [NOTIFUSE] Seed `invitation-prospection` silencieusement KO en runtime — staging confirme

> **Sévérité** : 🟡 P1 — sans le seed, Prospection ne peut pas envoyer
>   ses invitations. Filet manuel disponible (admin copie-colle le lien)
>   mais friction confirmée → priorité haute.
> **Owner** : agent Notifuse
> **Créé** : 2026-05-23
> **Découvert par** : agent QA E2E (spec `seed-templates.spec.ts`)

## Symptôme

Sur staging (`https://notifuse.staging.veridian.site`, image
`v46.0-veridian.9192e25a`, qui inclut le commit seed `6ed4820f`) :

```
POST /api/tenants/provision { tenant_id: "tst...", owner_email, plan: "pro" }
→ 200 { created: true, api_key: "..." }

# Puis avec le api_key du tenant :
GET /api/templates.list?workspace_id=tst...
→ 200 { "templates": null }

GET /api/templates.get?workspace_id=tst...&id=invitation-prospection
→ 404 { "error": "Template not found" }

GET /api/transactional.list?workspace_id=tst...
→ 200 { "notifications": null, "total": 0 }
```

→ **Le template + la notification ne sont jamais créés en runtime**.

Reproduction 100% : test 1/2/3/5 de
`tests/e2e-veridian/specs/seed-templates.spec.ts` fail systématiquement.

## Hypothèse de cause racine

`internal/service/template_service.go::CreateTemplate` ligne 178 :

```go
ctx, _, userWorkspace, err := s.authService.AuthenticateUserForWorkspace(ctx, workspaceID)
```

Le seed appelle `seedInvitationProspectionTemplate` qui passe par
`ctxAsRoot(ctx)` → session pour ROOT_EMAIL. **Mais le root user n'est PAS
membre du workspace fraîchement créé** (seul l'owner du tenant l'est).

Donc `AuthenticateUserForWorkspace` retourne probablement une erreur
"user not in workspace" → le seed log un warning (`CreateTemplate failed`)
et skip silencieusement. Les tests unit ne l'attrapent pas parce qu'ils
mockent `templateService.CreateTemplate` sans simuler le check
authorize-for-workspace.

À confirmer via `docker logs notifuse-staging | grep "veridian seed"`.

## Reco fix

Trois options possibles, par préférence décroissante :

1. **(Préféré)** Set `SystemCallKey` dans le ctx avant l'appel
   CreateTemplate, comme ce qui se fait déjà côté `WorkspaceService` pour
   les opérations Veridian-managed. Vérifier que TemplateService.CreateTemplate
   supporte le bypass (sinon ajouter le pattern symétrique à
   `GetTemplateByID` ligne 222-224).

2. Faire le seed via `ctxAsUser(owner.ID)` au lieu de `ctxAsRoot` —
   l'owner est bien membre du workspace. Mais ça mélange les
   responsabilités (le owner ne devrait pas "voir" qu'on lui crée des
   templates système).

3. Bypass le authorize via un wrapper repo direct
   `s.templateRepo.CreateTemplate` au lieu de passer par le service. Casse
   l'encapsulation, mais évite tout problème d'auth.

## Impact

- Prospection ne peut PAS envoyer ses mails d'invitation tant que ce bug
  tient : `transactional.send` retourne 400 "notification not found".
- Le filet manuel (lien admin copier-coller) reste fonctionnel — feature
  Prospection pas cassée, juste dégradée.
- Tous les tenants provisionnés depuis 2026-05-23 sont touchés (le seed
  n'a jamais fonctionné en runtime, malgré les 12 tests unit qui passent).

## Validation post-fix

Lancer la spec E2E :

```bash
cd tests/e2e-veridian
NOTIFUSE_URL=https://notifuse.staging.veridian.site \
HUB_API_SECRET=$NOTIFUSE_HUB_API_SECRET \
npx playwright test specs/seed-templates.spec.ts --reporter=line
```

Cible : 7/7 passing.

Pour les tenants déjà provisionnés sans seed : si la Hub re-provision
idempotente est OK (cf. `seed-templates.spec.ts` test 5 préserve les
existings), une simple re-call `/api/tenants/provision` les couvre en lazy.
Sinon, prévoir un script de backfill `for tenant in active: seed_now`.

## Résolution — 2026-05-23 (agent Notifuse)

**Option 2 retenue** (préférée à l'option 1 SystemCallKey car
TemplateService.CreateTemplate upstream ne supporte pas le bypass —
patcher template_service.go violerait la règle "jamais patcher upstream").

**Diff** :
- `internal/service/veridian_seed_templates.go` : signature
  `seedInvitationProspectionTemplate(ctx, workspaceID, ownerUserID string)`,
  ctxAsUser(ownerUserID) au lieu de ctxAsRoot, early-return si
  ownerUserID == "".
- `internal/service/veridian_service.go` : étape 11 Provision passe
  `owner.ID` au seed avec commentaire pointant ce ticket.
- `internal/service/veridian_seed_templates_test.go` : helper
  `expectCtxAsRoot` → `expectCtxAsUser` (plus de lookup root par email),
  tests existants migrés vers la nouvelle signature, ajout du test
  `TestSeedInvitationProspection_EmptyOwnerUserID_NoOp` (couvre la garde).
- `internal/service/veridian_service_test.go` : ajout
  `TestVeridianService_Provision_SeedReceivesOwnerID` qui capture le ctx
  passé au mock TemplateService et asserte que `UserIDKey == owner.ID`
  (et NON root.ID). Garde-fou anti-régression vers ctxAsRoot.

**Validation post-fix** :
- `go test ./internal/service/...` : OK (toutes suites)
- E2E staging à re-run après deploy : `tests/e2e-veridian/specs/seed-templates.spec.ts`
  (cible 7/7 passing, attendu vu que la modif corrige la cause racine
  identifiée par le ticket).

**Backfill tenants existants** : non implémenté (option lazy via
re-provision côté Hub privilégiée, le seed est idempotent).
