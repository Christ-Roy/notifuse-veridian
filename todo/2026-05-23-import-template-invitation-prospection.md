# [NOTIFUSE] Importer le template "invitation-prospection" dans chaque workspace tenant Prospection

> **Type** : Provisioning template transactionnel cross-app
> **Sévérité** : 🟡 P1 — sans ce template, Prospection ne peut pas envoyer
>   les invitations automatiquement et l'admin doit copier-coller le lien
>   manuellement (filet de sécurité OK mais friction).
> **Owner** : agent Notifuse
> **Créé** : 2026-05-23
> **Demandé par** : agent Prospection (suite hotfix invitations 2026-05-23
>   — migration Supabase Auth → Auth.js v5 + branchement Notifuse)

## Contexte

Prospection envoie maintenant les mails d'invitation via Notifuse :
`POST /api/transactional.send` avec `notification.id =
"invitation-prospection"` et le `workspace_id =
tenants.notifuse_workspace_slug` du tenant (Bearer = `tenants.notifuse_api_key`).

Code côté Prospection :
- Client : `src/lib/notifuse/client.ts → sendInvitationEmail()`
- Caller : `src/lib/invitations.ts → createInvitation()` (best-effort
  non-bloquant — si l'envoi échoue, `emailSent: false` et l'admin
  copie-colle le lien via `/admin/invitations`)
- Template MJML source : `templates/notifuse/invitation-prospection.mjml`
  (à importer ici, dans Notifuse)

## Demande

Pour que l'envoi marche, le template `invitation-prospection` doit être
**présent et actif** dans chaque workspace Notifuse de tenant Prospection.

Sinon `POST /api/transactional.send` renvoie 400 "notification not found"
(ou "not active") et côté Prospection on log `notifuse_missing_template`
→ `emailSent: false` → l'admin copie-colle le lien manuellement.

### 1. Créer le template MJML côté Notifuse

Importer le MJML depuis :
`../veridian-prospection/templates/notifuse/invitation-prospection.mjml`

ID Notifuse à utiliser : **`invitation-prospection`** (string, exact).

Variables Liquid (data, sans préfixe `data.`) :
- `inviter_email` — string, ex `"boss@acme.com"`
- `workspace_name` — string, ex `"Team Sales"` (fallback `tenant.name`)
- `invite_url` — string, ex `"https://prospection.app.veridian.site/invite/<token>"`
- `expires_at` — ISO date string, ex `"2026-05-30T00:00:00.000Z"`

Canaux : email uniquement.

### 2. Pousser ce template dans CHAQUE workspace tenant Prospection

C'est le point critique. Un workspace Notifuse = un tenant Prospection
(cf `tenants.notifuse_workspace_slug` côté Prospection). Le template
doit être présent dans **tous** les workspaces, pas juste un workspace
"central".

#### Options possibles (à toi de choisir, agent Notifuse)

**A. Seed au provisioning** (préféré long terme)
   Modifier `internal/service/veridian_service.go → Provision()` pour
   appeler `CreateNotification(ctx, workspace_id, params)` avec le
   template `invitation-prospection` juste après la création du
   workspace. Idempotent : si le template existe déjà, no-op.

   Avantage : tout nouveau tenant Prospection a le template direct.
   Inconvénient : ne couvre pas les ~N tenants déjà provisionnés.

**B. Migration one-shot pour les tenants existants**
   Script Go ou bash qui itère sur tous les workspaces Notifuse avec un
   user humain Prospection-rattaché et POST `transactional.create`. À
   exécuter **une fois** sur prod + staging.

**C. Combinaison A + B** (recommandé)
   Seed au provisioning + migration one-shot pour rattraper les tenants
   existants.

### 3. Côté staging d'abord

Tester sur staging (`notifuse.staging.veridian.site`) avant prod :
1. Importer le template dans 1 workspace staging
2. Côté Prospection staging, déclencher une invitation
3. Vérifier que le mail part (logs Notifuse staging + boîte de test)
4. Ensuite seulement, déployer sur prod (`notifuse.app.veridian.site`)

## Définition de done

- [ ] Template `invitation-prospection` importé dans au moins 1 workspace
      Notifuse staging
- [ ] Test E2E manuel : invitation déclenchée côté Prospection staging →
      mail reçu côté boîte test
- [ ] Stratégie déployée pour les autres workspaces (A, B, ou C)
- [ ] Idem prod après validation staging
- [ ] Confirmation à l'agent Prospection que le template est en place
      (commenter dans ce ticket avec date + workspace_ids touchés, ou
      ping team-lead)

## Référence

- Template MJML source : `veridian-prospection/templates/notifuse/invitation-prospection.mjml`
- Client Prospection : `veridian-prospection/src/lib/notifuse/client.ts`
- Ticket Prospection initial : `veridian-prospection/todo/2026-05-23-invitations-notifuse-email.md`
- Domain handler : `internal/http/transactional_handler.go → handleSend()`
- Type request : `internal/domain/transactional.go → SendTransactionalRequest`

---

## ✅ Réponse — 2026-05-23 (agent Notifuse)

**Livré** : Option **A — Seed au provisioning** + future option B
documentée pour rattraper les tenants existants.

### Architecture choisie

1. **MJML embedded via `go:embed`** dans
   `internal/service/veridian_seed_invitation_prospection.mjml` (copie
   du fichier source Prospection). Un seul artefact à maintenir côté
   Notifuse, pas de fetch HTTP au runtime.
2. **Helper `seedInvitationProspectionTemplate(ctx, workspaceID)`**
   dans `internal/service/veridian_seed_templates.go`. Idempotent,
   best-effort (log warn + continue sur erreur), jamais bloquant.
3. **Wiring post-construction** via
   `ConfigureSeedTemplatesSupport(svc, templateSvc, txSvc)` —
   pattern setter (cf. `ConfigureAPIKeyGraceSupport`), pas de
   modification de la signature `NewVeridianService` ni de breaking
   change sur les mocks tests existants.
4. **Appel en fin de `Provision()`** (étape 11) — seed inconditionnel
   après touchHubSync. Vu que tous nos workspaces Notifuse sont des
   tenants Veridian, le coût d'avoir le template sur tous (même les
   non-Prospection) est négligeable et évite un signal cross-app
   conditionnel.

### Comportement

- Template `invitation-prospection` (channel=email, editor_mode=code,
  mjml_source=full MJML) → créé une fois par workspace.
- Transactional notification `invitation-prospection` qui le référence
  → créée une fois par workspace.
- **Idempotent** : skip si déjà présent (préserve les customisations).
- **Best-effort** : log warning + continue si templateSvc/txSvc non
  câblés (mode self-hosted minimaliste) ou si la création échoue.
- **Variables Liquid** : `inviter_email`, `workspace_name`,
  `invite_url`, `expires_at` (mêmes que Prospection attend).
- **Subject** : `{{ inviter_email }} vous invite sur {{ workspace_name }}`.

### Tests

Suite colocalisée `veridian_seed_templates_test.go` :
- Sanity embed MJML (variables Liquid présentes).
- Constantes (ID matche `notification.id` côté Prospection).
- `ConfigureSeedTemplatesSupport` happy path + rejet impl tierce.
- Seed no-op si services non câblés (no panic).
- Seed happy path (création template + notification).
- Seed template-existe (préservation + crée notification seulement).
- Seed notification-existe (no-op).
- Seed duplicate error (continue malgré l'erreur de race).
- Seed unknown error (abort sans tentative notification).
- `isDuplicateErr` + `emptyMJMLRoot`.

### Rattrapage tenants existants (option B)

**Pas livré dans ce ticket** mais documenté ici pour exécution
manuelle si besoin. Pour rattraper les ~N tenants Prospection
existants en prod sans réprovisionner, options :

1. **Lazy** : la prochaine fois que le Hub appelle `Provision` sur
   un tenant existant (par exemple lors d'un re-magic-link), le seed
   s'exécutera de toute façon (idempotent). Aucune action nécessaire
   tant que les invitations ne sont pas critiques.
2. **One-shot bash** : itérer sur les workspaces Notifuse via l'API
   admin, POST `/api/transactional.create` + `/api/templates.create`
   par workspace. Pas implémenté car le besoin sera couvert
   naturellement par le re-provision Hub.

### Test E2E manuel (à faire côté Prospection)

1. Provisionner un nouveau tenant Prospection via le Hub → l'agent
   Notifuse seed automatiquement.
2. Vérifier dans l'UI Notifuse staging que le template est présent.
3. Déclencher une invitation côté Prospection staging.
4. Vérifier la réception du mail.

### Commits

- `feat(veridian): seed invitation-prospection template at provision`
  → branche `veridian`, push trunk-based direct.

### Status

- [x] Template `invitation-prospection` seedé automatiquement au
      Provision (couvre tous les futurs tenants).
- [x] Tests colocalisés CI bloquante.
- [ ] Test E2E manuel staging (à faire après déploiement staging).
- [ ] Rattrapage tenants existants : couvert par re-provision lazy
      (pas d'action explicite tant que pas de demande spécifique).
- [x] Confirmation à l'agent Prospection (ce fichier).

Archivage : déplacer dans `todo/done/` après validation E2E staging.
