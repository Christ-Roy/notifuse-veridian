# E2E headful sature staging pendant le run (pic de provisioning) → fails transitoires

> **Sévérité** : 🟡 P1 (rend la CI E2E bloquante FLAKY → bloque toute promo, ex. fix UI 2026-06-20)
> **Owner** : agent notifuse-veridian
> **Créé** : 2026-06-20

## Symptôme

Run E2E `27882415183` (fix UI trivial) a échoué **3 fois de suite** sur des
erreurs TRANSITOIRES d'environnement, PAS sur le code testé :
- run 1 : `connection limit reached: 348/350 connections, cannot create pool`
  (3 tests failed : chaos-provisioning, chaos-magic-link, anti-regression).
- run 2 (après assainissement pool) : 1 test failed, `500` sur provision
  (provision-idempotence / dashboard-smoke).

Pendant un run, le pool DB et le compte de bases `notifuse_ws_*` EXPLOSENT :
mesuré **761 bases** en plein run (vs ~94 records légitimes). 298 tests headful,
beaucoup provisionnent un workspace jetable → pic de bases vivantes + connexions.

## Causes (deux, distinctes)

1. **Stock historique d'orphelines** créées avant le fix wipe-recrée (`514e6c12`)
   → traité par le **cron de cleanup** posé le 2026-06-20
   (`/home/ubuntu/notifuse-staging-gc.sh`, `*/30 * * * *` sur dev-pub).
2. **Pic transitoire pendant le run** : 27/28 specs wipent déjà leur workspace en
   `afterAll`, MAIS (a) pas de **globalTeardown** filet de sécurité, (b) les tests
   tournent en parallèle (workers Playwright) → le pic simultané de bases/connexions
   sature le pool (max_connections=400) et/ou le disque (87-88%) → provision `500`.

## Fix proposé (voie propre)

- **globalTeardown Playwright** : un sweep final qui appelle l'endpoint
  `gc-orphan-workspace-dbs` (ou wipe par prefix de run) pour garantir 0 résidu
  même si un afterAll a sauté (test crashé). Filet de sécurité.
- **Réduire le parallélisme** des specs qui provisionnent lourd (`workers` limité
  ou `fullyParallel: false` sur le projet staging) pour lisser le pic.
- **Cause profonde worker** : le worker garde une connexion idle par base élue
  (round-robin) → 1 base = 1 connexion qui ne se libère pas. Réduire
  l'idle-timeout du pool worker (ou fermer la connexion après inactivité) pour
  que N bases n'ouvrent pas N connexions permanentes. C'est le vrai levier
  réputationnel du pool — à chiffrer (touche `internal/repository` pool config).
- Bump `max_connections` staging (400→600) = pansement, pas un fix. À éviter seul.

## Lien

- Le bug wipe-recrée (`514e6c12`, fix record-first) est sur staging et CONFIRMÉ
  (provision→wipe→base reste à 0 après 90s, preuve on-premise 2026-06-20). Il
  empêche la régénération mais ne réduit PAS le pic transitoire d'un run massif.
- Cron cleanup : ticket `2026-06-17-dev-pub-disk-pressure-bloque-ci-build.md`.

## ✅ CAUSE RACINE TROUVÉE + FIX 2026-06-23 (commit ae64f878)

La régénération de bases venait de **`notifuse_system.tasks` non purgée au wipe**.
Le scheduler global poll `tasks` indépendamment de `workspaces` → re-dispatche les
tasks orphelines → `tasks.execute` → `init.go` RECRÉE la base du workspace mort →
boucle. Constaté : **3124 tasks orphelines → ~991 bases régénérées**, dev-pub 99%.

**Fix** : `VeridianDeleteWorkspaceSystemRecord` supprime maintenant aussi
`DELETE FROM tasks WHERE workspace_id = $1` (en dernier, après workspaces). Purge
immédiate des 3124 tasks orphelines = 0 régénération (prouvé : worker ne poll plus
QUE canary/réels, plus aucun tst*).

→ Une fois ce fix EN PROD, la dette de bases ne s'accumule plus → les 2 tests E2E
skippés (todo/2026-06-22) peuvent être réactivés.
