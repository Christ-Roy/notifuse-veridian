# Orphelins système secondaires au wipe / GC (hygiène, non bloquant)

> **Sévérité** : 🟢 P2 (hygiène base système, pas de régénération ni d'impact prod direct)
> **Owner** : agent notifuse
> **Créé** : 2026-06-23
> **Trouvé par** : HUNT axe 7 (archéologie) en complétant la propagation du fix `tasks`

## Contexte

En complétant la propagation du fix `tasks` (commit ae64f878), j'ai ajouté au wipe
(`VeridianDeleteWorkspaceSystemRecord`) le nettoyage des 3 tables système Veridian
keyées `workspace_id` qui survivaient au DROP de la base workspace :
`veridian_api_key_grace`, `veridian_frozen_members`, `veridian_imap_uid_seen`
(+ `veridian_plan` déjà couvert par `planRepo.HardDelete`). Ces 3 étaient un trou
d'hygiène pur (pas de régénération comme `tasks`). **Corrigé.**

Pendant l'audit, 2 sources d'orphelins système SECONDAIRES sont apparues. Elles ne
sont **pas** des régressions urgentes (aucune ne régénère de base ni ne casse la prod),
mais elles méritent une décision propre — d'où ce ticket plutôt qu'un fix silencieux.

## 1. User owner du tenant orphelin dans `users` (+ `user_sessions`)

Au wipe, `user_workspaces` est nettoyé (membership coupé) mais la ROW `users` du
tenant-owner `veridian_managed` n'est PAS supprimée → elle reste orpheline dans la
table système `users` (et ses `user_sessions` éventuelles).

**Pourquoi pas un fix direct** : le modèle d'identité Notifuse (CONTRAT-HUB §3.7) traite
les users comme potentiellement CROSS-workspace. Supprimer inconditionnellement le user
au wipe casserait un humain qui possède un autre workspace. Il faut une condition sûre :
« supprimer le user SEULEMENT s'il est `veridian_managed` ET n'a plus aucune ligne
`user_workspaces` après le wipe ». À trancher + tester (multi-workspace, sessions).

**Impact réel** : rows mortes dans `users`/`user_sessions`. Pas d'impact auth (un user
sans `user_workspaces` ne peut accéder à rien). Volume : 1 row/wipe.

## 2. GC orphan-DB ne purge pas les rows système annexes

`VeridianGCOrphanWorkspaceDBs` (staging-only) DROP les bases physiques orphelines mais ne
nettoie PAS les rows système annexes (`veridian_plan`, `veridian_api_key_grace`,
`veridian_frozen_members`, `veridian_imap_uid_seen`) du workspace_id correspondant.

**Pourquoi pas un fix direct** : le GC dérive le `wsID` du NOM de base via une conversion
`-`→`_` **non bijective** (`VeridianWorkspaceIDFromDBName`) → on ne peut pas reconstruire
de façon fiable le workspace_id exact pour cibler les DELETE annexes. Ajouter un nettoyage
annexe au GC nécessiterait soit un mapping fiable nom-de-base→workspace_id, soit
d'accepter le risque de matcher la mauvaise row (inacceptable).

**Impact réel** : un orphan-DB GC'd n'a déjà PLUS de record `workspaces` (par définition du
GC), donc ses rows annexes ne sont jamais lues (paywall/poller ciblent par PK/List). Rows
mortes pures, staging only. Faible.

## Reco

- #1 : implémenter la suppression conditionnelle du user owner `veridian_managed`
  orphelin au wipe (condition stricte multi-workspace + test). Effort ~moyen.
- #2 : laisser tel quel OU ajouter un balayage périodique des rows système annexes
  sans record `workspaces` correspondant (anti-join), indépendant du nom de base.
  Effort faible si fait en anti-join SQL, pas via le GC de bases.

Aucun des deux n'est bloquant pour la prod.
