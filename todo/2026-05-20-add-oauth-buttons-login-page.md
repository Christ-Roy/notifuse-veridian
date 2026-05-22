# [NOTIFUSE] Ajouter boutons "Continuer avec Google + Microsoft" sur page login fallback

> **Type** : UX login fallback Notifuse
> **Sévérité** : 🟡 P2
> **Owner** : agent Notifuse
> **Spec parent** : `veridian-hub/todo/2026-05-20-fallback-login-apps-redirect-hub.md`
> **Créé** : 2026-05-20

## ⏸️ STATUT 2026-05-23 — BLOQUÉ par Hub (spec gravée au contrat)

**Spec contractuelle** : `veridian-hub/docs/CONTRAT-HUB.md` §6bis.8
« Couche 4 — Bounce OAuth Hub » (gravée 2026-05-23). Décrit le flow
complet, agnostique aux noms d'apps, extensible à toute future
`*.veridian.site`.

**Bloqué par** : le Hub n'a pas encore livré la couche 4 (param
`?next=` + appel `POST /api/sso/issue-magic-link`). Vérifié 2026-05-23 :
aucune trace dans `veridian-hub/app/login/` ni `app/api/auth/`.

**Ce ticket Notifuse devient** :
1. Implémenter l'endpoint contrat `POST /api/sso/issue-magic-link`
   (cf. CONTRAT-HUB §6bis.8.3) — HMAC §6.1, body `{hub_user_id, email}`,
   réponse `{magic_link_url}`. Réutilise la logique magic_link existante.
2. Ajouter les 2 boutons « Continuer avec Google » + « Continuer avec
   Microsoft » sur `/signin` (cf. CONTRAT-HUB §6bis.8.1) — bouton
   standardisé qui redirige vers
   `app.veridian.site/login?next=<encoded_self_url>`.
3. Tests CI bloquants (cf. §6bis.8.5).

**Action attendue** : agent Hub livre la couche 4 côté Hub. Estimation
~45-60 min Hub.

**Côté Notifuse une fois Hub livré** : ~1-2h (endpoint + boutons + tests).

**À reprendre dès que** :
`veridian-hub/todo/2026-05-20-fallback-login-apps-redirect-hub.md`
passe dans `done/`.

---

## Demande

Sur `notifuse.app.veridian.site/signin` (page login fallback), ajouter
2 boutons "Continuer avec Google" + "Continuer avec Microsoft" en plus du
form magic link existant.

**Pas d'implémentation OAuth côté Notifuse** — les boutons redirigent
simplement vers `app.veridian.site/login?next=<current_url>` et le Hub gère
le flow OAuth puis renvoie un magic link Notifuse via le contrat HMAC.

## Contrat avec le Hub

- Côté Notifuse : aucun endpoint nouveau, juste l'UI à ajouter
- Côté Hub : implémente le param `?next=` + whitelist domaines `*.veridian.site`
  (cf. spec parent)

## Pré-requis

- Hub doit avoir livré le support `?next=` (Phase 2 ticket OAuth)

## Effort estimé

- 0.5j (UI + redirect, pas d'API)

## Référence

- Spec complète : `veridian-hub/todo/2026-05-20-fallback-login-apps-redirect-hub.md`
