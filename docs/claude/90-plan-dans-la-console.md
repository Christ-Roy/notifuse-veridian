# Réglages > Plan : le vrai plan de l'espace (25/11/2026)

`GET /api/veridian/me/workspace-plan?workspace_id=` (membre de l'espace, module `internal/veridian/planview`, CLI `plan:get`) rend `plan`, `plan_source`,
`status`, ni quota ni compteur (règle 8, fiche 47). 403 hors de l'espace, 404 sans plan, 500 en panne.
**Pourquoi** : la console appelait cette route depuis le 21/05/2026, elle n'avait jamais été écrite ; le repli de l'écran affichait « Free, essai de 15
jours » à tout le monde, les espaces payants compris. Règle : un plan non lu se dit (« Plan indisponible pour le moment »), jamais « Free » par défaut.
**Ce qui l'avait caché** : la suite console (E2E sur staging) le voyait à chaque run, mais son étape est en `continue-on-error` : le job finissait vert.
Tablette (768 à 991 px) : menu replié au départ, tables et en-têtes ne débordent plus (`console/src/index.css`).
