# 2026-05-19 — Endpoints rotate-api-key + transfer-owner (v1.2)

> **Spec** : `../CONTRAT-HUB.md` §5.15 (rotate api_key) + §5.16 (transfer ownership)
> **Sévérité** : 🟢 P2 (Hub a des fallbacks documentés en §5.15.3 et §5.16.3)
> **Effort** : M (2-8h)

## Contexte

Deux endpoints v1.2 manquants :

### §5.15 — `POST /api/tenants/{id}/rotate-api-key`

Génère une nouvelle api_key tenant, garde l'ancienne valide 5 min (grace period), puis revoque.

Body : `{reason: string}`
Réponse : `{tenant_id, new_api_key, new_api_key_email, old_api_key_revokes_at}`

Aujourd'hui Hub fallback : re-provisionne le tenant pour regénérer une api_key → marche mais 5x plus de side-effects (nouveau user technique, transfer ownership, etc).

### §5.16 — `POST /api/tenants/{id}/transfer-owner`

Transfère l'ownership à un nouvel email (l'ancien owner devient admin).

Body : `{new_owner_email: string, reason: string}`
Réponse : `{tenant_id, old_owner, new_owner, transferred_at}`

Aujourd'hui Hub fallback : appelle `attach-owner` (qui transfert et garde l'ancien comme member) → marche mais l'ancien reste membre du workspace au lieu d'être admin.

## Travail

1. **`service.RotateAPIKey(ctx, tenantID, reason)`** :
   - Crée une nouvelle api_key via `CreateAPIKey` (déjà existant).
   - Marque l'ancienne `revoke_at = NOW() + 5min` (nouvelle colonne `users.revoke_at` ou via `veridian_api_key_grace` table).
   - Emit `tenant.api_key_rotated` (ajouter au ticket webhooks).
   - Cron qui revoke effectivement après 5 min.

2. **`service.TransferOwner(ctx, tenantID, newOwnerEmail, reason)`** :
   - Similaire à `AttachOwner` mais l'ancien owner devient `admin` (pas `member`).
   - Emit `tenant.owner_changed` (déjà émis pour AttachOwner avec transfer:true).

3. **Handlers** + tests colocalisés.

## Note

`AttachOwner` existant fait déjà 80% du job de `transfer-owner`. La différence : aujourd'hui l'ancien owner reste `member`, contrat veut `admin`. Refacto ou nouveau endpoint dédié ? **Reco : nouveau endpoint dédié** pour clarté, qui appelle `AttachOwner` + update role ancien owner en post.
