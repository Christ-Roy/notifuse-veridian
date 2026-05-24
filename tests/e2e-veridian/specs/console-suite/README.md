# console-suite — specs front console Veridian

Ce dossier est réservé aux tests E2E qui exercent l'**UI console** (React,
WorkspaceLayout, drawers, etc.) plutôt que les endpoints backend HMAC.

## Spec mobile-responsive

La spec qui valide le Lot 1 (sidebar hamburger sous 768px) vit dans :

```
console/e2e-prod-smoke/mobile-responsive.spec.ts
```

**Pourquoi pas dans ce dossier `console-suite/`** :

- La suite `tests/e2e-veridian/` n'a pas de Vite webServer câblé — elle
  tape une URL réelle (`NOTIFUSE_URL`, staging ou prod) via HMAC.
- Tester le WorkspaceLayout (rendu après auth) requiert soit un magic
  link valide côté staging (fragile, brûle un token réel à chaque run),
  soit un stub `localStorage + /api/user.me + /api/workspaces.members`
  (clean, déterministe, mais ne marche QUE si on contrôle le browser
  context et les routes — exactement ce que fait `e2e-prod-smoke`).
- La suite `console/e2e-prod-smoke/` build le bundle de prod, lance
  `vite preview`, stub les API, et tourne déjà en CI via le job
  `e2e-console-prod-smoke` (workflow `veridian-ci.yml`). Tout est en
  place — pas la peine de réinventer ça ici.

**Run local** :

```bash
cd console
npm run test:prod-smoke               # build + tous les tests prod-smoke
npm run test:prod-smoke:skip-build    # réutilise dist/ existant
npx playwright test mobile-responsive --config playwright.prod-smoke.config.ts
```

## Convention

Ce dossier reste prêt à accueillir des specs E2E console qui auront besoin
du backend RÉEL (par ex. flow de provisioning → login → first-time setup
via magic link). Pour le pur layout/responsive, garder dans
`console/e2e-prod-smoke/`.
