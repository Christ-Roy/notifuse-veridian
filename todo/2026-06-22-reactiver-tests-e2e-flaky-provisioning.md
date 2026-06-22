# Réactiver 2 tests E2E skippés (flaky provisioning sous charge)

> **Sévérité** : 🟡 P1 (dette de test — 2 anti-régressions désactivées temporairement)
> **Owner** : agent notifuse-veridian
> **Créé** : 2026-06-22

## Contexte

Pour débloquer la promo prod du lot de 5 fixes (session 2026-06-19/21, bloquée
3 jours par la CI), 2 tests E2E ont été passés en `test.skip` le 2026-06-22 :

1. `tests/e2e-veridian/specs/anti-regression-veridian-mode.spec.ts:116`
   « GET /api/workspaces.list après tentative bloquée = 1 seul workspace »
2. `tests/e2e-veridian/specs/chaos-provisioning.spec.ts:168`
   « 5 provisions concurrentes tenants distincts → majorité 200, pas de crash »

**Les tests sont SAINS** — ils ne testent rien de cassé. Ils flakent (404/500)
parce qu'ils **provisionnent en plein run** et que le pool DB staging est saturé
par la **dette de bases de test** (cf. ci-dessous). Le bump connexions 600/800 a
éliminé les `connection limit` mais ces 2 tests (provisioning concurrent) restent
fragiles tant que staging accumule des centaines de bases.

## Cause racine à corriger AVANT de réactiver

**Bug de régénération de bases workspace** : même worker staging fraîchement
redémarré + bases purgées, le compte remonte (mesuré 8 records → 355 bases en
quelques minutes). Le fix wipe-recrée (`514e6c12`, dans le lot promu) coupe UNE
source mais pas tout — il reste une source de création de bases pour des
workspaces de test dont le record n'existe plus / plus pollés. À investiguer :
- Le worker `EmailQueueWorker` recrée-t-il la base au poll d'un workspace dont le
  record vient d'être supprimé mais qui est encore en cache/liste ?
- Y a-t-il un cache de liste de workspaces non invalidé au restart ?
- Les tenants de test E2E (prefix `tst`/`e2e`/`chaos`...) ont-ils des records qui
  survivent et que le worker re-poll → recrée la base ?

## Garde-fous déjà en place (ne suffisent pas seuls)

- `globalTeardown` Playwright (`tests/e2e-veridian/global-teardown.ts`) : GC en
  fin de run → réduit la dette inter-runs.
- Crons dev-pub : purge idle `*/2min` + GC `*/30min`.
- Bump connexions DB 600 (app) / 800 (postgres).

## Définition of done

- [ ] Bug de régénération de bases identifié + corrigé (la cause, pas le symptôme)
- [ ] Staging stable au repos : bases ≈ records (pas de gonflement spontané)
- [ ] Retirer les 2 `test.skip` → `test` + run E2E complet vert sur staging propre
- [ ] Vérifier que le globalTeardown maintient bases ≈ records après un run complet
