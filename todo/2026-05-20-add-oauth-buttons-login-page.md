# [NOTIFUSE] Ajouter boutons "Continuer avec Google + Microsoft" sur page login fallback

> **Type** : UX login fallback Notifuse
> **Sévérité** : 🟡 P2
> **Owner** : agent Notifuse
> **Spec parent** : `veridian-hub/todo/2026-05-20-fallback-login-apps-redirect-hub.md`
> **Créé** : 2026-05-20

## ⏸️ STATUT 2026-05-23 — BLOQUÉ par Hub

**Bloqué par** : le Hub n'a pas livré le support `?next=<url>` sur
`app.veridian.site/login`. Vérifié 2026-05-23 par agent Notifuse :
aucune trace dans `veridian-hub/app/login/` ni
`veridian-hub/app/api/auth/[...nextauth]/`.

L'OAuth direct Hub fonctionne (Google + Microsoft livrés 2026-05-20),
mais sans `?next=` le bouton côté Notifuse enverrait l'utilisateur sur
le Hub et **il y resterait** au lieu de revenir sur Notifuse → flow
inutilisable.

**Action attendue** : l'agent Hub livre le `?next=` (cf. ticket parent
mis à jour). Estimation Hub ~30-45 min. Une fois livré, ce ticket
Notifuse devient trivial (0.5j max).

**À reprendre dès que** : `veridian-hub/todo/2026-05-20-fallback-login-apps-redirect-hub.md`
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
