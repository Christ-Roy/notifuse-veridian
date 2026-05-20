# 2026-05-19 — CI : `paths-ignore` pour docs-only push

> **Spec** : `../CI-ARCHITECTURE.md` Constitution §8 (Path-based skip docs)
> **Sévérité** : 🟢 P2
> **Effort** : S (<2h)

## Constat

Aujourd'hui, un commit `docs(todo): bla` ou un update README déclenche le pipeline complet (~10 min, build image GHCR + push, deploy staging, e2e staging). Gaspillage de temps + cycles GHCR.

## Travail

1. Au top de `.github/workflows/veridian-ci.yml`, ajouter :
   ```yaml
   on:
     push:
       branches: [veridian]
       paths-ignore:
         - '**.md'
         - 'docs/**'
         - 'todo/**'
         - 'CHANGELOG.md'
   ```

2. **Garder le job `constitution-mapping`** qui doit tourner même sur docs (vérifie qu'il n'y a pas de fichier critique modifié sans test). Sinon un commit "docs" qui touche aussi du Go passerait sans check.

3. **Garder le pre-push hook** qui bloque déjà localement les pushs invalides.

## Risque

P3 — fonctionnellement safe : si quelqu'un veut force un re-deploy sans changement code, il peut utiliser `workflow_dispatch` manuel.

**Alternative plus paranoïaque** : utiliser `paths` au lieu de `paths-ignore`, et lister explicitement les chemins qui déclenchent la CI. Plus verbeux mais plus prévisible.

---

## Update — 2026-05-20 — Livré (commit 9976490e)

`paths-ignore` ajouté au workflow sur `push` ET `pull_request` :
- `**/*.md`, `docs/**`, `todo/**`, `CHANGELOG.md`, `runbooks/**`, `plans/**`

Sécurité maintenue :
- Pre-push hook local check-test-mapping.sh continue de tourner
- Commits mixtes (docs + Go) déclenchent quand même la CI (AND filter
  GitHub Actions = run si au moins un path ne match pas paths-ignore)
- Bot ci(gitops): utilise `[skip ci]` → pas de boucle infinie

À déplacer vers `todo/done/` après confirmation que le prochain commit
docs-only ne déclenche pas de run CI.
