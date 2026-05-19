# 2026-05-19 — Mode dégradé paywall obfusqué (tenants soft-deleted)

> **Spec** : `../CONTRAT-HUB.md` §5.9
> **Sévérité** : 🟡 P1 UX
> **Effort** : L (1-2 jours)

## Contexte

Aujourd'hui un tenant `soft_deleted` est juste tagué `deleted_at != NULL`. La console reste accessible si l'user passe par un vieux magic link, et il voit ses données comme avant — pas top niveau UX (le client doit comprendre qu'il est en sursis).

Contrat §5.9 exige un **mode dégradé** :

- Routes **lecture** : 33% des chars en clair + reste obfusqué (`fooba••••••`)
- Routes **écriture** : refusent avec `402 Payment Required` + `error: tenant_soft_deleted` + lien `restore_url` du Hub
- Champs sensibles (`SENSITIVE_FIELDS` §5.9) : password, api_key, billing info → **toujours obfusqués**

## Travail

1. **Middleware `internal/http/middleware/paywall.go`** :
   - Lookup `veridian_plan.deleted_at` (caché 60s via `PaywallCache`).
   - Si soft-deleted :
     - Si méthode write → 402 + body standardisé.
     - Si méthode read → wrapper response, transformer JSON via `obfuscate(fieldName, value)`.

2. **Constante `SENSITIVE_FIELDS`** : liste partagée (probablement dans `internal/http/middleware/paywall_fields.go`).

3. **Helper `obfuscate(field, value)`** : retourne `value[:n/3] + strings.Repeat("•", n-n/3)` pour string, `null` pour objets sensibles.

4. **Wire** : appliquer le middleware sur les routes métier (`/api/contacts.*`, `/api/templates.*`, `/api/broadcasts.*`, etc.). **Pas** sur les routes Veridian-managed (`/api/tenants/*` HMAC Hub, `/api/veridian/admin/*`).

5. **Tests** : 1 test par catégorie de route + obfuscation contract test.

## Risque

P2 — feature additive, ne change rien pour les tenants `active`. Vérifier la liste exhaustive des routes write/read (Notifuse upstream a ~40-50 endpoints à classer).

## Reco

Avant d'attaquer : **Robert** valide d'abord la liste des `SENSITIVE_FIELDS` (j'en ferai un draft à partir des modèles `contact`, `template`, `broadcast`, `workspace`). Le pattern d'obfuscation 33% est arbitraire — Robert peut préférer "10 chars max + ellipsis" ou autre.
