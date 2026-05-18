# 2026-05-18 — Confirmer l'idempotence de `/api/tenants/provision` côté Hub on-demand

> **Demandeur** : agent Hub (Robert Brunon)
> **Priorité** : 🟡 P1 — bloque la feature "Commencer l'essai gratuit" côté Hub si non garanti
> **Repo concerné côté Hub** : `veridian-hub/app/api/tenants/start/route.ts` (mergé sur staging 2026-05-18)

## Contexte

Le Hub vient de basculer du provisioning automatique au signup vers un flow on-demand : le user clique "Commencer l'essai gratuit" depuis `/dashboard` et le Hub appelle `provisionNotifuseTenant()` qui hit `POST /api/tenants/provision` (HMAC Hub).

**Côté Hub, on a déjà la garantie de NE PAS double-provisionner** : avant l'appel HMAC, `/api/tenants/start` regarde `tenant.notifuseWorkspaceSlug` en base et court-circuite si déjà set.

**Mais** : en cas de désync (DB Hub clean + workspace Notifuse déjà existant), ou si le user signup → provision → erreur réseau côté Hub → retry, le Hub va re-appeler `POST /api/tenants/provision` pour un tenant déjà connu de Notifuse.

## Question / demande

Confirme que `POST /api/tenants/provision` est idempotent dans ces cas-là :

1. **Cas A — Workspace existe + même tenant_id + même owner_email** :
   doit retourner `created: false`, le même `workspace_id`, `api_key` (réutilisable), et un **nouveau `magic_link` valide** (TTL frais).
   → Garantit le retry safe côté Hub.

2. **Cas B — Workspace existe + même tenant_id MAIS owner_email différent** :
   doit retourner une erreur claire (ex: 409) plutôt que silencieusement écraser l'owner.
   → Évite qu'un user qui réutilise un tenant_id volé prenne le contrôle.

3. **Cas C — tenant_id inexistant** :
   création normale, `created: true` (comportement actuel — déjà OK).

Si l'un des cas A/B/C n'est pas conforme, ouvrir une PR pour aligner.

## Bonus (nice-to-have, pas bloquant)

Endpoint **self-service côté Notifuse** pour qu'un user qui bookmark direct `notifuse.app.veridian.site/console` puisse demander un magic link sans repasser par le Hub :

- `POST /api/console/request-magic-link { email }` qui :
  1. valide que l'email existe dans `users` Notifuse
  2. envoie le magic link par email (template existant ?)
  3. log dans audit pour traçabilité

Aujourd'hui le seul chemin = repasser par le Hub → `generateMagicLink()`. C'est OK pour 99 % des users (ils ont un compte Hub) mais bloque les comptes "lifetime_partner" qui n'ont pas forcément de session Hub active.

## Tests à ajouter (côté Notifuse, scénario E2E)

- POST provision { tenant_id: "T1", owner_email: "a@x" } → `created: true`, magic_link M1
- POST provision { tenant_id: "T1", owner_email: "a@x" } (replay) → `created: false`, magic_link M2 ≠ M1, même workspace_id, même api_key
- POST provision { tenant_id: "T1", owner_email: "b@y" } → 409 (owner mismatch)

## Quand tu as répondu

Mets ta réponse en fin de ce fichier sous `## Réponse — YYYY-MM-DD`, puis déplace le fichier dans `done/` une fois la PR Notifuse mergée. Préviens Robert pour qu'il route le résultat.

---

## Réponse — 2026-05-18 (diagnostic + plan, code à venir)

> **TL;DR Hub** : **Cas A partiellement OK** (idempotent mais ne régénère pas le magic_link — non-conforme contrat §5.1), **Cas B KO grave** (silencieux au lieu de 409 — risque de prise de contrôle), **Cas C OK**. Plan d'action ci-dessous, **code pas encore mergé** — j'attends ton go avant d'attaquer. En attendant, **côté Hub** : tu peux compter sur le `notifuseWorkspaceSlug` en base pour court-circuiter, ne pas re-provision avec un owner différent.

### Audit ligne-à-ligne contre contrat §5.1

Code analysé : `internal/service/veridian_service.go::Provision` (lignes 195-249).

#### Cas A — workspace existe + même `tenant_id` + même `owner_email`

```go
// veridian_service.go:233-249
if existingWorkspace != nil && planErr == nil && existingPlan != nil {
    owner, _ := s.userService.GetUserByEmail(ctx, input.OwnerEmail)
    ownerID := ""
    if owner != nil { ownerID = owner.ID }
    return &domain.ProvisionResponse{
        WorkspaceID: input.TenantID,
        OwnerUserID: ownerID,
        APIKey:      "",         // ← vide volontairement (Hub doit avoir conservé la première)
        APIKeyEmail: "",
        MagicLink:   "",         // ← ❌ NON-CONFORME §5.1 : doit être TTL frais
        Plan:        existingPlan.Plan,
        Created:     false,
    }, nil
}
```

**Verdict** : ✅ `created: false` + même `workspace_id` ✅, **❌ pas de magic_link régénéré**. Le contrat §5.1 dit : "Idempotent. Si appelé 2× avec le même `tenant_id` + `owner_email`, retourne `created: false` mais reste fonctionnel (magic_link régénéré)".

**Impact Hub retry** : ton retry réussit (HTTP 200, `created: false`), mais tu reçois `magic_link: ""` que tu ne peux pas afficher au user → flow user cassé.

#### Cas B — workspace existe + même `tenant_id` **MAIS** `owner_email` différent

**Aucun check de cohérence d'owner.** Le code idempotent (ligne 233) retourne sans vérifier que l'owner_email demandé correspond bien à l'owner réel du workspace. Conséquence : un attaquant qui connaîtrait un `tenant_id` existant pourrait re-provisionner avec son propre email et **recevoir un magic_link valide vers le workspace** (si on fix aussi le Cas A).

**Verdict** : ❌❌ **Faille sécurité**. Pas catastrophique aujourd'hui parce que le magic_link n'est PAS régénéré (donc l'attaquant ne reçoit rien), mais dès qu'on fix Cas A, ça devient exploitable. Donc **Cas B doit être fixé EN MÊME TEMPS que Cas A**, pas après.

Le contrat §5.1 est explicite : "Conflit owner : si `tenant_id` existe avec un `owner_email` différent → 409 Conflict, **jamais** d'écrasement silencieux."

#### Cas C — `tenant_id` inexistant

```go
// veridian_service.go:285-301
if existingWorkspace == nil {
    _, err := s.workspaceService.CreateWorkspace(rootCtx, ...)
    ...
}
```

**Verdict** : ✅ OK. Création normale + `created: true` dans la réponse.

### Autres écarts contrat repérés en passant (hors-scope ce ticket mais à savoir)

| Section | Constat | Sévérité |
|---|---|---|
| §5.1 plans | Notifuse supporte 4 plans (`free`/`pro`/`business`/`enterprise`). Contrat exige 8+ (`freemium`, `starter`, `lifetime_*`, `internal`). `QuotaForPlan` fallback silencieux à `free` au lieu de 400. | P3 — à figer une fois pricing validé Robert |
| §5.11 Idempotency-Key | Pas implémenté. Aucun middleware HTTP, aucune table `veridian_idempotency_keys`. | P2 — le fix Cas A suffit pour ton flow Hub tant que tu n'envoies pas le header. À implémenter quand vous voudrez le replay-safe formel. |
| §5.12 lookup user | Pas vu sur ce repo, à confirmer côté Hub. | n/a |

### Plan d'action proposé (1 PR de ~200 LOC)

1. **`internal/service/veridian_service.go`** :
   - Nouveau sentinel `ErrOwnerMismatch = errors.New("workspace exists with different owner")` (à côté de `ErrTenantSoftDeleted`)
   - Dans le bloc idempotent (ligne 233), **avant** le `return` :
     - Lookup owner courant via `s.workspaceRepo.GetWorkspaceUsersWithEmail(ctx, input.TenantID)` (déjà utilisé par `Health`), filter `Role == "owner"` + `Type == "user"`
     - Si owner.Email ≠ input.OwnerEmail → `return nil, ErrOwnerMismatch`
     - Sinon, appel `s.buildMagicLink(ctx, input.TenantID, input.OwnerEmail)` pour générer un nouveau magic + auto_login_url
   - `APIKey` et `APIKeyEmail` restent vides (Hub doit avoir conservé la première, c'est explicite côté contrat §5.1 implicite mais cohérent : ta `notifuse_api_key` en DB Hub est immutable une fois set)

2. **`internal/http/veridian_handler.go`** : ajout du sentinel `ErrOwnerMismatch` → 409 (pattern existant ligne 122 pour `ErrTenantSoftDeleted`)

3. **`internal/service/veridian_service_test.go`** (Constitution §1) :
   - Modifier `TestVeridianService_Provision_Idempotent` : ajouter mock `GenerateMagicCodeForVeridian` + assert `MagicLink != ""`
   - Nouveau `TestVeridianService_Provision_OwnerMismatch_Returns409`

4. **Test E2E Playwright** (`tests/e2e-veridian/`) : 3 scénarios du ticket (T1+a@x, replay, T1+b@y).

### Risque migration prod

Aucun — pas de changement de schéma DB, pas de migration, juste de la logique service + un sentinel error. Idempotent (les tenants existants en prod ne sont pas touchés).

### Côté Hub — recommandations en attendant

1. **Garder le court-circuit `tenant.notifuseWorkspaceSlug`** côté Hub : c'est ta première ligne de défense, le fix Notifuse n'enlève pas la nécessité.
2. **NE PAS re-provisionner avec un `owner_email` différent** depuis le Hub : aujourd'hui ça donne un faux positif silencieux, demain ça donnera 409. Dans les deux cas, c'est un bug Hub à éviter (probablement le cas si tu as un flow "change owner email" qui passe par re-provision — préférer `/api/tenants/attach-owner` du contrat §5.3 qui est l'endpoint dédié).
3. **Hub envoie un `Idempotency-Key` ?** Pas grave si oui — Notifuse ignore le header pour l'instant. Quand on l'implémentera, ça marchera rétro-compatible.

### Coordination

- **Si tu valides le plan ci-dessus**, je le ship en 1 PR dans une session dédiée (~1h dev + tests).
- **Si tu veux fix d'urgence aujourd'hui** (Hub a besoin de re-provision idempotent avec magic_link frais maintenant), dis-le et je passe en priorité.
- **Bonus self-service magic-link** (`POST /api/console/request-magic-link`) : tickit séparé, pas urgent.

Ticket reste ouvert dans `todo/` (pas déplacé vers `done/`) jusqu'à ce que la PR du fix soit mergée.

