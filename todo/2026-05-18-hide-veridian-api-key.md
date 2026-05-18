# Cacher la clé API Veridian-managed des Team Settings (anti-sabotage)

> **Type** : Hardening + UX sécurité
> **Priorité** : 🟡 P1 — risque réel mais faible probabilité (clients ne savent pas que la clé existe)
> **Origine** : Robert Brunon — observation directe en staging 2026-05-18 via Chrome MCP.
> **Bloque** : non. Le ship AttachOwner part en prod sans ce fix.

---

## Le problème

Quand le Hub provisionne un workspace Notifuse via `POST /api/tenants/provision`, on crée :

1. Un user humain (`owner_email`, `type=user`, `role=owner`)
2. Un user technique pour la clé API (`veridian-api-<tenant>@notifuse...`, `type=api_key`, `role=member` + `Full Access`)

La clé API est **stockée côté Hub** dans `hub_app.tenants.notifuse_api_key` et sert à :
- Générer des magic links cross-app (`POST /api/workspaces.generateMagicLink`)
- Émettre des events (potentiel futur)

**Bug observé** : aujourd'hui cette clé technique apparaît dans la page **Console → Settings → Team** côté user :

```
Email                                                          Role     Permissions  Since
chrome88887@chrome.test                                        Owner    Full Access  18/05/2026
veridian-api-chrome88887@notifuse.staging.veridian.site        API Key  Full Access  18/05/2026
```

L'user voit cette clé, peut probablement la supprimer ou en révoquer le token. **Résultat** : le bouton "Open Notifuse" depuis le dashboard Hub renvoie 401 sur ce tenant, sans aucun moyen de comprendre pourquoi côté user, et sans alerte côté Hub.

C'est exactement la même classe de problème que le bug 2026-05-17 (owner orphelin → magic link cassé silencieux), mais déclenchable par l'user lui-même.

## Standard industrie

Variante 2 du pattern "service-managed credentials" — la plus courante chez SaaS B2B :

1. **Flag DB** sur la ressource : ajouter `users.veridian_managed BOOLEAN DEFAULT FALSE`
2. **Filtre serveur** : les queries de listing membres excluent les users avec ce flag
3. **Handler bloquant** : DELETE / révocation refuse avec 403 si target est veridian-managed
4. **Optionnel UI** : badge "Integration credentials" dans une section read-only (transparence > opacité totale)

Variante chez Vercel/Render/Railway. Variante 3 (compte technique séparé via GitHub App style) plus solide mais ~2j de refacto, hors scope.

## Livrables — Phase 1 (1-2h)

### 1. Migration V7 — `internal/migrations/v7.go`

```go
package migrations

import (
    "context"
    "github.com/Notifuse/notifuse/config"
    "github.com/Notifuse/notifuse/internal/domain"
)

type V7Migration struct{}

func (m *V7Migration) GetMajorVersion() float64 { return 7.0 }
func (m *V7Migration) HasSystemUpdate() bool { return true }
func (m *V7Migration) HasWorkspaceUpdate() bool { return false }

func (m *V7Migration) UpdateSystem(ctx context.Context, _ *config.Config, db DBExecutor) error {
    // Ajouter veridian_managed pour distinguer les users system-managed (api_key
    // créées par Hub Veridian) des users normaux. Anti-sabotage : ces users ne
    // doivent ni apparaître dans Team Settings ni être supprimables par l'user.
    _, err := db.ExecContext(ctx, `
        ALTER TABLE users
        ADD COLUMN IF NOT EXISTS veridian_managed BOOLEAN NOT NULL DEFAULT FALSE
    `)
    if err != nil { return err }
    // Backfill : tous les users existants avec email matching le prefix
    // veridian-api-* sont system-managed.
    _, err = db.ExecContext(ctx, `
        UPDATE users
        SET veridian_managed = TRUE
        WHERE email LIKE 'veridian-api-%@notifuse.%'
          AND type = 'api_key'
          AND veridian_managed = FALSE
    `)
    return err
}

func (m *V7Migration) UpdateWorkspace(_ context.Context, _ *config.Config, _ *domain.Workspace, _ DBExecutor) error {
    return nil
}

func init() { Register(&V7Migration{}) }
```

Bumper `config/config.go` à `VERSION = "7.0"`.

### 2. Setter dans le domain `User` — `internal/domain/user.go`

```go
type User struct {
    // ... champs existants
    VeridianManaged bool `json:"veridian_managed,omitempty" db:"veridian_managed"`
}
```

### 3. Filtre côté service Veridian — `internal/service/veridian_service.go` Provision()

À l'étape "5. Creer une API key tenant", après `CreateAPIKey` (qui crée le user en interne), faire un UPDATE additionnel pour set `veridian_managed = true` :

```go
apiKeyToken, apiKeyEmail, err := s.workspaceService.CreateAPIKey(rootCtx, input.TenantID, apiKeyPrefix)
// ...
// === Veridian patch === Marker le user api_key comme veridian-managed
// pour qu'il soit invisible / non-supprimable côté Team Settings.
if err := s.userRepo.MarkVeridianManaged(ctx, apiKeyEmail); err != nil && s.logger != nil {
    s.logger.WithFields(map[string]interface{}{
        "api_email": apiKeyEmail,
        "error":     err.Error(),
    }).Warn("veridian: failed to mark api_key user as veridian_managed (non-fatal)")
}
```

Nouvelle méthode `UserRepository.MarkVeridianManaged(ctx, email)` côté `internal/repository/user_repository.go` :

```sql
UPDATE users SET veridian_managed = TRUE WHERE email = $1
```

### 4. Filtre serveur — `internal/repository/workspace_repository.go`

Modifier `GetWorkspaceUsersWithEmail` pour exclure les users `veridian_managed = TRUE`. **ATTENTION** : c'est utilisé par notre propre `AttachOwner` Step 3 (résoudre owner courant). On veut exclure de l'UI uniquement, pas du contexte interne Hub.

Donc deux options :

- **Option A** (préférée) : créer une nouvelle méthode `GetWorkspaceUsersWithEmailVisible` qui filtre, et changer **seulement** les call sites UI (handler `/api/workspaces.members`).
- Option B : ajouter un paramètre `includeManaged bool` à la méthode existante.

Côté handler `workspace_handler.go` route `/api/workspaces.members` : utiliser la nouvelle méthode.

### 5. Handler DELETE bloquant — `internal/http/workspace_handler.go`

Trouver le handler `RemoveMember` ou équivalent. Avant la suppression :

```go
target, err := h.userRepo.GetUserByID(ctx, req.UserID)
if err != nil { /* 404 */ }
if target.VeridianManaged {
    WriteJSONError(w, "cannot remove a Veridian-managed integration credential", http.StatusForbidden)
    return
}
```

### 6. Tests colocalisés (Constitution CI §1)

- `internal/migrations/v7_test.go` : test migration idempotente + backfill
- `internal/service/veridian_service_test.go` : `TestProvision_MarksAPIKeyVeridianManaged`
- `internal/repository/user_repository_test.go` : `TestMarkVeridianManaged_Idempotent`
- `internal/repository/workspace_repository_test.go` : `TestGetWorkspaceUsersWithEmailVisible_ExcludesManaged`
- `internal/http/workspace_handler_test.go` : `TestRemoveMember_Refuses403OnVeridianManaged`

### 7. UI Console — optionnel mais propre

`console/src/pages/settings/Team.tsx` : ne PAS afficher les users `veridian_managed`. Ou afficher dans une section séparée "Integrations" en read-only avec badge "Required by platform — cannot be removed".

## Livrables — Phase 2 (plus tard, P2)

- Endpoint `GET /api/veridian/admin/list-managed-users` (HMAC Hub) qui retourne tous les `veridian_managed` users avec leurs workspaces. Permet au Hub d'auditer ses propres credentials.
- Endpoint `POST /api/veridian/admin/rotate-api-key` (HMAC Hub) qui régénère un token sans casser le mapping `hub_app.tenants.notifuse_api_key`. Pour rotation 6 mois.

## Risque actuel sans ce fix

| Probabilité | Impact | Détectabilité |
|---|---|---|
| Faible (clients ne savent pas que c'est là) | Bouton "Open Notifuse" cassé sur ce tenant, silencieux | Le cron `/api/tenants/{id}/health` post-fix livré 2026-05-18 détecterait : `api_key_valid: false` → alerte |

Donc on a déjà une **detection layer** post-2026-05-18-prod. Mais la prévention est meilleure que la détection.

## Coordination Hub

Quand ce fix est en prod Notifuse :

1. Le Hub peut continuer comme avant — pas de breaking change côté API.
2. Optionnel : ajouter un check post-provision côté Hub : appel `/api/user.me` (Bearer api_key) → vérifie que le user retourné a `veridian_managed: true`. Garantie contractuelle.

## Branche & PR

Branche dédiée `feat/veridian-managed-users`. PR séparée du AttachOwner ship. Pas de breaking change → pas de v2 API needed.
