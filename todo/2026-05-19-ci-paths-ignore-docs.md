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
