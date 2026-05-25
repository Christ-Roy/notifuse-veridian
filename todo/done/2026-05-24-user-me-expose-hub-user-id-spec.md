# GET /api/user.me — graver la shape réponse incluant `hub_user_id`

> **Sévérité** : 🟢 P2 — sanity guard
> **Owner** : agent Notifuse
> **Créé** : 2026-05-24 (post-fix Agent T 7d5b352d)

## Contexte

Agent T (commit `529e4b9d` + `7d5b352d`) a fix le bug runtime où
`/api/user.me` ne renvoyait pas `hub_user_id` après backfill. Fix :
`CreateUser` persiste maintenant `hub_user_id` à l'INSERT + validation UUID
stricte.

Spec E2E `identity-cross-app.spec.ts` C14/C15 valide bien le comportement
end-to-end (testé live 2026-05-24, vert).

## Ce qui manque

L'endpoint `/api/user.me` est **upstream Notifuse** (cf.
`internal/http/user_handler.go::GetCurrentUser`). On dépend du fait
qu'upstream sérialise le `*domain.User` brut, ce qui inclut `hub_user_id`
via le json tag `omitempty` qu'Agent L a ajouté.

**Risque** : un sync upstream future qui change le shape du DTO `/api/user.me`
(ex: ajoute un middleware de transformation, passe par un DTO dédié) peut
supprimer silencieusement `hub_user_id` du JSON sans casser les tests unit
upstream. Notre spec E2E C14 le détecterait, mais **on n'a aucune assertion
au niveau handler/unit** côté Veridian.

## Demande

Ajouter un test colocalisé `internal/http/user_handler_test.go` (ou
`veridian_user_me_response_test.go` si on veut isoler) qui :

1. Mocke `userService.GetUserByID` → retourne `User{HubUserID: &hubUUID}`
2. Appelle handler `GetCurrentUser` via `httptest`
3. Parse la response JSON
4. Assert `response["user"]["hub_user_id"]` == hubUUID

Soit **garde-fou bloquant** : si upstream casse le shape ou supprime le
champ, CI Veridian rouge avant déploiement.

## Convention

- **Patcher fichier upstream `user_handler_test.go` interdit**
- Crée `internal/http/veridian_user_me_response_test.go` qui réutilise les
  helpers existants pour invoquer le même handler
- Couvre aussi :
  - user sans `hub_user_id` (V46 NULL) → response avec champ absent
    (omitempty actif)
  - user avec `hub_user_id` non-UUID stocké (legacy) → ne crash pas la
    sérialisation

## Estimation

1h (1 test bien fait, pas plus).

## Définition de done

- [ ] Test colocalisé créé
- [ ] Pre-push passe
- [ ] Si on supprime le tag JSON sur `User.HubUserID` (test négatif manuel)
      → test fail immédiatement

---

## ✅ Archivé — 2026-05-25 (team-lead vague 2 audit)

Livré par les agents vague 2 (cf. commits 77a44f94 freeze, 2e50fef1 user-me, 2213d0b3 cross-app cards). Voir notes des agents dans le commit message + giga E2E 9/9 PASS post-fix freeze body bug.
