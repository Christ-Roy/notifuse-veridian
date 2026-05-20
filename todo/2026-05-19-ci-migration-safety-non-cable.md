# 2026-05-19 — CI : check-migration-safety.sh non câblé dans le workflow

> **Spec** : `../CI-ARCHITECTURE.md` Constitution §12 (Migrations Expand & Contract)
> **Sévérité** : 🟡 P1 (risque migration cassée en prod)
> **Effort** : S (<2h)

## Constat

Le script `scripts/ci/check-migration-safety.sh` existe (7.5 KB, logique complète) et il est référencé par `check-test-mapping.sh` (ligne 259) qui essaie de l'appeler en transitif. **MAIS** il n'est **pas appelé directement** dans `.github/workflows/veridian-ci.yml`.

Résultat : un commit Veridian qui ajoute une migration `internal/migrations/v33.go` avec un `DROP COLUMN` passe la CI sans gate dédié. La sécurité repose uniquement sur le mapping test (qui ne vérifie pas la sémantique destructive).

## Travail

1. Ajouter un job `migration-safety` dans `veridian-ci.yml`, en parallèle de `constitution-mapping` :
   ```yaml
   migration-safety:
     name: Constitution CI §12 (migrations Expand & Contract)
     runs-on: ubuntu-latest
     if: github.event_name == 'push'
     steps:
       - uses: actions/checkout@v4
         with:
           fetch-depth: 0
       - name: Check migration safety
         run: BASE_REF=origin/veridian scripts/ci/check-migration-safety.sh
   ```

2. Ajouter `migration-safety` aux `needs` de `deploy-staging` (avec les autres gates Constitution §1).

3. Tester : créer un commit factice avec `ALTER TABLE foo DROP COLUMN bar` dans une migration → CI doit fail.

## Bonus

Le script bloque les opérations destructives. Pour les autoriser quand légitime (rare), ajouter convention `[safe-migration]` dans le subject de commit qui bypass le gate, documenté dans `CLAUDE.md`.

## Risque

P0 — addition pure d'un job, ne casse rien. Le gate révèlera peut-être des migrations passées qui ne respectent pas Expand & Contract — à traiter au cas par cas.

---

## Update — 2026-05-20 — Livré (commit 9db7d0ec)

Job `migration-safety` ajouté dans `.github/workflows/veridian-ci.yml`
après `compose-validate`. `test-go` dépend désormais de
`[test-mapping, compose-validate, migration-safety]` — gate avant build.

Override `[safe-migration]` dans subject de commit câblé pour les
opérations légitimes (rollback ciblé, suppression colonne déjà migrée
2 deploys en avance). Documenté dans le step CI.

À déplacer vers `todo/done/` après confirmation 1-2 runs CI verts.
