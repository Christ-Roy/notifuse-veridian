# 2026-05-23 — Freeze/Unfreeze members per-user (CONTRAT-HUB §5.21)

> **Sévérité** : 🟢 P2 — bloque pas la prod, attend que Hub branche
> `tenant.member_frozen` cross-app
> **Owner** : agent notifuse-veridian
> **Créé** : 2026-05-23
> **Origine** : livrable 5 differe du ticket parent
> `2026-05-19-v13-multi-membre-cross-app.md`

## Contexte

CONTRAT-HUB §5.21 (Seat overage — soft warning 7j) prevoit qu'a J+7 si
quota seats pas resolu, le Hub envoie webhook
`tenant.member_frozen { user_emails: [...] }` aux apps downstream. Les
apps doivent :

- Mettre chaque user de la liste en mode degrade paywall obfusque §5.9
  (lecture floute) cote lecture.
- Bloquer toute ecriture pour ces users → 402 `tenant_paywall` (cf §5.10).
- L'owner reste actif normalement.

Cote Notifuse aujourd'hui (2026-05-23) : **paywall middleware
tenant-level uniquement** (`internal/http/middleware/veridian_paywall.go`).
Aucun mecanisme per-user.

## Pourquoi differe

Cf reponse ticket parent (Option B documentee) :

1. **§5.21.4 default conservateur** : Hub bloque les nouvelles invitations
   seat-overage cote lui (402 `seat_quota_exceeded_soft_warning`).
   Protection minimale deja en place sans freeze app-side.
2. **Aucun consommateur business** : le Hub n'emet PAS encore
   `tenant.member_frozen`. Le code freeze cote Notifuse ne serait jamais
   declenche. ROI negatif tant que le Hub n'a pas branche cette emission.
3. **Cout refactor** ~1 jour dev + tests + smoke staging.

## Demandes (a faire quand Hub branche)

### 1. Migration `V48` additive

```sql
ALTER TABLE user_workspaces
  ADD COLUMN frozen_at TIMESTAMP WITH TIME ZONE NULL;
CREATE INDEX IF NOT EXISTS idx_user_workspaces_frozen_at
  ON user_workspaces (workspace_id, frozen_at)
  WHERE frozen_at IS NOT NULL;
```

Bump `config.VERSION` 46→48 (saute 47 si pris par autre agent — verifier
git log au moment de l'implementation, cf memory
[[reference_config_version_bump_required]]).

### 2. Endpoints cote Notifuse

- `POST /api/tenants/{id}/freeze-members` (HMAC + Idempotency)
  Body : `{user_emails: ["bob@x.test", "carol@x.test"], reason?: string}`
  Effet : `UPDATE user_workspaces SET frozen_at = NOW() WHERE workspace_id
  = $id AND user_id IN (lookup by email)`.
  Idempotent : si deja `frozen_at != NULL` → 200, ne touche pas.
  Garde-fou : refus du freeze sur le owner du workspace.
- `POST /api/tenants/{id}/unfreeze-members` (HMAC + Idempotency)
  Body : `{user_emails: [...]}`
  Effet : `UPDATE user_workspaces SET frozen_at = NULL WHERE ...`.
  Idempotent.

### 3. Middleware paywall extension

`internal/http/middleware/veridian_paywall.go` doit gerer un nouveau cas :

- Apres auth (user_id resolu), lookup `user_workspaces.frozen_at` pour le
  workspace courant.
- Si `frozen_at != NULL` :
  - GET endpoints (reads) → 200 mais reponse obfusquee §5.9 (champs
    sensibles masques cote serveur, modale paywall cote client).
  - POST/PUT/DELETE (writes) → 402 `tenant_paywall` (corps standard §5.10).

Cache paywall doit etre decouple sur `(tenant_id, user_id)` au lieu de
`tenant_id` seul.

### 4. Webhook bidirectionnel (optionnel)

Si Hub veut une confirmation : emit
`tenant.member_freeze_applied { user_emails: [...] }` apres freeze cote
Notifuse → Hub peut tracer la propagation. Pas critique en v1.

## Estimation

~1j dev + 4h tests middleware + smoke staging.

## Garde-fou anti-flap

Le freeze doit etre **irreversible cote Notifuse sans ordre explicite Hub**
— pas de cron de cleanup ou de auto-unfreeze cote app. Seul l'endpoint
`unfreeze-members` (HMAC Hub) leve le gel. Sinon risque de race
condition avec le webhook delivery (e.g. unfreeze locale puis re-freeze
Hub → user grille).

## Reference

- CONTRAT-HUB §5.9 paywall obfuscation
- CONTRAT-HUB §5.10 codes erreur paywall
- CONTRAT-HUB §5.21 seat overage soft warning
- Ticket parent : `todo/2026-05-19-v13-multi-membre-cross-app.md`
