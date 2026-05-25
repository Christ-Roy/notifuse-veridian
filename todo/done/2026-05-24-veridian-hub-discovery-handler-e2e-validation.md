# Section A spec `identity-cross-app` — fail attendu si Hub discovery pas wiré côté UI

> **Sévérité** : 🟢 P2 — observabilité
> **Owner** : agent Notifuse
> **Créé** : 2026-05-24

## Contexte

Agent Q a livré `tests/e2e-veridian/specs/identity-cross-app.spec.ts` qui
contient une **Section A — hub-discovery client (Agent J)**.

Côté code, Agent J a livré `GET /api/veridian/hub-discovery/me` (endpoint
Notifuse-side, JWT auth, appelle le Hub en interne). Le client Go fonctionne
en best-effort.

## Question ouverte

Aucun consommateur frontend Notifuse n'appelle aujourd'hui
`/api/veridian/hub-discovery/me`. Le code est en place, l'endpoint marche
(vérifié unit + E2E), mais **personne ne le déclenche au login**.

Le ticket original `2026-05-23-call-hub-discovery-by-email.md` disait :
> "Au login, appeler ce endpoint pour décider du redirect post-login + pré-charger
>  les liens cross-app dans le dashboard Notifuse"

→ Le **call** est livré (côté Hub-driven). Le **wiring UI** (qui consomme la
réponse pour afficher cards "Tu as aussi Prospection") **n'est PAS livré**.

## Demande

Wirer côté `console/src/` :

1. **Au login (post auto-login URL ou magic code)** : appel
   `GET /api/veridian/hub-discovery/me` avec le JWT user
2. **Stocker la réponse** dans le store (Zustand ? React Query cache ?)
3. **Afficher dans le dashboard** : si `exists:true` et `tenants` contient
   d'autres apps que Notifuse, afficher des cards de redirection :
   - "Tu as aussi Prospection → ouvrir Prospection"
   - Lien : `https://app.veridian.site` (Hub)
4. **Fallback gracieux** : si endpoint fail (Hub down, etc.) → ne rien
   afficher, pas d'erreur visible user

## Coordination

- Côté Hub-side (`app.veridian.site`) probablement déjà une UI qui montre les
  apps du user → ticket similaire chez `veridian-hub/todo/` (à dispatcher
  par Robert à l'agent Hub)

## Estimation

~2-3h (composant React `<CrossAppCards>` + appel API + store + tests Vitest).

## Définition de done

- [ ] Composant `console/src/components/veridian_cross_app_cards.tsx` + test
- [ ] Mount dans dashboard principal (post-login)
- [ ] Spec E2E mise à jour : Section A teste que UI affiche bien les cards
      quand `exists:true`
- [ ] Pas de régression sur dashboard standard

## Pourquoi P2 (pas P1)

Pure UX nice-to-have. L'absence ne casse rien. Mais c'est le complément
naturel du flow SSO + OAuth Hub qu'on a livré cette session.

---

## ✅ Archivé — 2026-05-25 (team-lead vague 2 audit)

Livré par les agents vague 2 (cf. commits 77a44f94 freeze, 2e50fef1 user-me, 2213d0b3 cross-app cards). Voir notes des agents dans le commit message + giga E2E 9/9 PASS post-fix freeze body bug.
