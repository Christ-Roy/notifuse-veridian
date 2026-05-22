# Suite E2E Playwright complète — console Veridian Mail

> **Sévérité** : 🟡 P1
> **Owner** : agent Notifuse
> **Créé** : 2026-05-22
> **Déclencheur** : incident console morte au boot en prod (cf. ci-dessous)

---

## Pourquoi ce ticket

**Incident 2026-05-22** : le code-splitting Vite (`manualChunks`) a été mal
configuré — React et TanStack/Antd dans des chunks séparés. Rollup ne
garantit pas l'ordre d'évaluation entre chunks frères → crash au boot
`Cannot read properties of undefined (reading 'createContext')`. **La
console est restée morte EN PROD**, et les **225 tests unitaires
(Vitest/jsdom) ne l'ont pas vu** : ils montent les composants depuis le
code source, jamais depuis le build réel découpé en chunks servi par un
navigateur.

**Trou identifié** : zéro test qui exerce la console comme un vrai
utilisateur, sur un vrai build, dans un vrai navigateur.

## Ce qui a déjà été fait (garde-fou léger — livré 2026-05-22)

- `scripts/ci/check-console-build.sh` — analyse statique du `dist/` :
  build OK, intégrité des chunks, **chunk React atomique** (détecte
  exactement le bug de l'incident). Zéro navigateur, rapide.
- Branché en **pre-push Husky** (si `console/` modifié) + **job CI
  `console-build`** (en `needs` de `build` → bloque l'image).

C'est le filet rapide. **Ce ticket = la couche lourde complémentaire.**

## Objectif de ce ticket — suite E2E Playwright sur staging

Une suite Playwright **complète**, qui tourne **sur staging** (jamais
bloquante pour la prod tant qu'elle n'est pas mûre — informative
d'abord, bloquante ensuite quand elle sera fiable et qu'on aura décidé
« on ne casse plus la prod »).

### Périmètre cible (à construire progressivement, écran par écran)

1. **Smoke de boot** (🔴 priorité, le minimum vital)
   - Ouvrir `/console/` → l'app monte réellement (le splash disparaît,
     un élément React rendu apparaît).
   - **Zéro erreur console** au chargement.
   - C'est le test qui aurait attrapé l'incident en conditions réelles.

2. **Parcours authentification**
   - Login par magic code (le flow réel).
   - Auto-login token (le flow Hub→Notifuse).
   - Page token expiré → `<Result>` correct.

3. **Parcours workspace**
   - Sélection de workspace, navigation sidebar (Dashboard, Contacts,
     Lists, Templates, Broadcasts, Automations, Settings…).
   - Chaque écran : monte sans erreur, pas d'overflow horizontal.

4. **Parcours métier clés**
   - Créer / éditer un contact.
   - Créer un template (email builder).
   - Créer un broadcast.
   - Vérifier les états vides (`Empty`) et de chargement (`Skeleton`).

5. **Responsive** — 375 / 768 / 1440 px sur les écrans principaux
   (le polish UI 2026-05-22 n'a été validé qu'à 523px faute de fenêtre
   Chrome large — à reprendre ici).

### Cadre technique

- Playwright est **déjà installé** (`console/playwright.config.ts`,
  `tests/e2e-veridian/`, `@playwright/test`). Réutiliser, ne pas
  réinventer.
- Cible : `notifuse.staging.veridian.site` (data staging réelle).
- Provisionner un tenant de test dédié via l'API admin HMAC
  (`/api/tenants/provision`) — pattern déjà éprouvé.
- Tag `@prod-safe` pour les tests read-only autorisés sur prod
  (convention existante `tests/e2e-veridian/`).
- Le job CI `e2e-staging` existe déjà dans `veridian-ci.yml` — y
  brancher cette suite.

### Séquençage suggéré

1. Smoke de boot d'abord (1 test, ROI maximal) → branché en CI staging.
2. Parcours auth + navigation workspace.
3. Parcours métier.
4. Responsive.
5. Quand la suite est stable et fiable → la passer **bloquante** sur
   staging (la promesse « on ne casse plus la prod »).

## Effort estimé

- Smoke de boot : ~1-2h
- Parcours auth + navigation : ~0.5j
- Parcours métier : ~1j
- Responsive : ~0.5j
- **Total : ~2-3j**, à étaler — la suite se construit écran par écran.

## Lien

- Garde-fou léger livré : `scripts/ci/check-console-build.sh`
- Incident : régression `manualChunks` dans le code-splitting perf
  (ticket `done/2026-05-22-perf-ui-baseline-saine.md`).
