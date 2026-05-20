# [NOTIFUSE] Ajouter boutons "Continuer avec Google + Microsoft" sur page login fallback

> **Type** : UX login fallback Notifuse
> **Sévérité** : 🟡 P2
> **Owner** : agent Notifuse
> **Spec parent** : `veridian-hub/todo/2026-05-20-fallback-login-apps-redirect-hub.md`
> **Créé** : 2026-05-20

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
