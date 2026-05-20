# [NOTIFUSE] Endpoint `GET /api/users/by-email` pour Hub discovery

> **Type** : Endpoint contrat HMAC Hub
> **Sévérité** : 🟡 P2 — utile pour le pattern discovery long terme
> **Owner** : agent Notifuse
> **Spec parent** : `veridian-hub/todo/2026-05-20-hub-discovery-by-email-pattern.md`
> **Créé** : 2026-05-20

## Use case

Permettre au Hub d'afficher la carte Notifuse côté dashboard user **par
discovery** plutôt que via les colonnes dénormalisées de `hub_app.tenants`
(qui restent valides en attendant la migration).

Aujourd'hui pour Notifuse spécifiquement, le user voit déjà sa carte car
`tenants.notifuse_*` est rempli au provisioning. Mais en migrant vers le
pattern discovery, on supprime cette dette.

## Endpoint à livrer

Voir spec parent. Réponse type :

```json
{
  "found": true,
  "user_email": "user@x.com",
  "workspaces": [
    {
      "workspace_id": "workspace-slug",
      "workspace_name": "User Workspace",
      "role": "owner",
      "plan": "freemium",
      "magic_link_capable": true,
      "fallback_url": "https://notifuse.app.veridian.site/signin"
    }
  ]
}
```

Notifuse a déjà :
- `internal/http/veridian_magic_handler.go` (génération magic link)
- `internal/http/veridian_autologin_handler.go` (auto-login)
- Donc `magic_link_capable: true` toujours

Implementer : `internal/http/veridian_discovery_handler.go` avec HMAC verify
(pattern existant) + query SQL `SELECT workspaces JOIN user_workspaces WHERE
user_email = ?`.

## Effort

- 1j (réutilise les patterns HMAC existants)

## Référence

- Spec discovery cross-app : `veridian-hub/todo/2026-05-20-hub-discovery-by-email-pattern.md`
- Pattern magic_link Notifuse : `internal/http/veridian_magic_handler.go`
