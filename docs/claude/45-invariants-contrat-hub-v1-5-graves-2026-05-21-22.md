# Invariants CONTRAT-HUB v1.5 (gravés 2026-05-21/22)

> **Source de vérité** : `../veridian-hub/docs/CONTRAT-HUB.md` v1.5 +
> `../veridian-hub/docs/CONTRAT-HUB-API-REF.md` v1.1. Lire ces docs
> avant toute modif de `internal/service/veridian_service.go`,
> `internal/http/veridian_handler.go`, `internal/repository/veridian_*`.

### §1.4 — Hub source de vérité + résilience apps

**Anti-pattern interdit** : call Hub synchrone dans un hot path utilisateur
(login, page protégée). Notifuse doit pouvoir continuer à fonctionner si
le Hub est down (mode dégradé "best effort") :
- Plan lookup : colonne locale `tenants.plan` + cache TTL 5 min
- Magic link déjà émis : reste valide même si Hub down (token signé)
- API key tenant : 100 % locale (hash dans `workspaces.api_key_hash`)
- Webhooks app → Hub : best-effort 3 retries backoff puis log + skip

### §1.4bis — Résilience billing niveau 1 (`last_hub_sync_at`)

3 phases mesurées par middleware paywall :
- **Fresh** < 24h : mode normal
- **Stale** 24h-72h : grace period optimiste + log warn rate-limité
- **Dead** > 72h : writes → `503 hub_sync_dead`, reads passent

Constantes côté Notifuse : `HubSyncFreshThreshold` / `HubSyncDeadThreshold`
(migration V39 livre la colonne, middleware `EvaluateHubSyncStatus`).

### §3.7 — Modèle identité user cross-app

Trois identifiants distincts :
- **`hub_app.users.id`** (Hub) — source de vérité unique de l'identité
- **`notifuse.users.id`** (local) — PK interne, distinct par construction
- **`notifuse.users.hub_user_id`** (V46, nullable) — backfillé au 1er contact

**Règles de jointure** :
- Hub appelle Notifuse → body inclut `email` + `hub_user_id`. Notifuse
  résout `hub_user_id` en local, sinon `email`, sinon crée user.
- Notifuse appelle Hub → utilise `email` ou `hub_user_id` selon dispo.
- App appelle app → **INTERDIT** (tout cross-app passe par le Hub).

**Garde-fous** : un même `hub_user_id` ne peut être lié qu'à un seul
user Notifuse (invariant V46). Jamais changer le `hub_user_id` d'un
user existant sans audit + migration explicite.

### §4.4 — Cycle de vie membre (2 manières exclusives)

Un humain peut être lié à un workspace Notifuse de **2 manières** :

1. **Owner** : click "Commencer l'essai" sur SON Hub Dashboard → Hub
   appelle `POST /api/tenants/provision` → Notifuse crée workspace + user
   owner + api_key.
2. **Membre invité** : un autre humain l'invite via `/dashboard/team` →
   Hub envoie magic link → user accepte → Hub appelle
   `POST /api/veridian/workspaces/{id}/attach-member` (ou `/api/tenants/{id}/attach-member`).

Le signup Hub **ne crée AUCUN tenant ni AUCUN membership** côté Notifuse.

### §5.18.2 — DÉPRÉCIÉ : doublon admin invite-member

Le doublon `POST /api/admin/tenants/{id}/invite-member` côté Hub est
déprécié en v1.5. Tout flow invitation passe par P1 §5.22 (endpoint
`/api/invitations/create` côté Hub + `/api/veridian/workspaces/{id}/attach-member`
côté apps). Notifuse n'a jamais consommé l'endpoint déprécié : rien à
nettoyer côté app.

### §5.22.2 — `attach-member` workspace-level

Notifuse expose **deux routes** vers le même handler (mono-workspace :
`tenantId == workspaceId`) :
- `POST /api/tenants/{tenantId}/attach-member` (historique tenant-level lot B 2026-05-21)
- `POST /api/veridian/workspaces/{tenantId}/attach-member` (alias v1.5 conforme §5.22.2)

### §5.22.4 — JAMAIS écraser un rôle existant

Si un user est déjà membre du workspace avec un rôle différent de celui
demandé par le Hub : retourner 200 `already_member=true` avec le **rôle
LOCAL inchangé**, log info `role conflict ignored`. L'admin Notifuse a
le contrôle souverain sur ses rôles internes (§5.18.4 : Hub non-autoritatif).
**Pas de UPDATE remove+re-add** (downgrade silencieux interdit).

### §5.22 — Endpoints membre actifs cross-app

| Niveau | Endpoint Notifuse | Auth | Cas |
|---|---|---|---|
| Workspace | `POST /api/veridian/workspaces/{id}/attach-member` (§5.22.2) | HMAC | Voie normale : invitation user-side |
| Workspace | `POST /api/tenants/{tenantId}/attach-member` (alias) | HMAC | Equivalent mono-workspace |
| Tenant | `POST /api/tenants/{id}/sync-member` (§5.18.3) | HMAC | Voie admin/migration script |
| Webhook | `tenant.member_role_changed` (§5.18.4) | Bearer | App → Hub, audit only |

### §6bis.7 — Logout cross-app local

Modèle "logout local" : chaque app gère son logout indépendamment. Pas
de propagation cross-app obligatoire. Scope cookie en staging :
`.staging.veridian.site` (multi-tenant subdomain).

### §7.1 — Webhooks app → Hub étendus

Nouveaux events disponibles côté Notifuse (à émettre quand applicable) :
- `tenant.member_role_changed` (élévation/abaissement local)
- `tenant.member_added` (post sync-member réussi)
- `tenant.member_removed` (post remove-member réussi)

### §11bis — Permissions cross-app

Matrice des droits par action documentée côté Hub (CONTRAT-HUB-API-REF
section PERMS). Notifuse reçoit le `target_role` du Hub mais peut
l'**élever** localement via UI Team Settings (pattern §5.18.4 informatif).

---

