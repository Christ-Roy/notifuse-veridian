# Suite E2E Playwright complète — console Veridian Mail

> **Sévérité** : 🟡 P1
> **Owner** : agent Notifuse
> **Créé** : 2026-05-22
> **Déclencheur** : incident console morte au boot en prod (cf. ci-dessous)

---

## Pourquoi ce ticket

**DEUX incidents en prod le même jour (2026-05-22), aucun détecté par
les 225 tests unitaires :**

**Incident #1 — console morte au boot.** Le code-splitting Vite
(`manualChunks`) a été mal configuré — React et TanStack/Antd dans des
chunks séparés. Rollup ne garantit pas l'ordre d'évaluation entre chunks
frères → crash au boot `Cannot read properties of undefined (reading
'createContext')`. Console morte EN PROD.

**Incident #2 — caractères aléatoires (« GdgCoi », « 8wOKeG »).** Les
chaînes de texte ajoutées au polish n'avaient jamais été extraites dans
les catalogues Lingui (`build` fait `lingui compile` mais pas
`lingui extract`). Lingui affichait le messageId hash brut en prod au
lieu du texte. Visible sur la page Plan et le lien de retour.

**Pourquoi les tests unitaires ne les ont pas vus** : Vitest/jsdom monte
les composants depuis le code source, avec les `t\`...\`` résolus à
l'extraction — jamais depuis le build réel (chunks découpés + catalogues
compilés) servi par un vrai navigateur.

**Trou identifié** : zéro test qui exerce la console comme un vrai
utilisateur, sur un vrai build, dans un vrai navigateur — et qui
vérifierait que les textes affichés sont du vrai texte, pas des hash.

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
   - **Zéro hash i18n affiché** : assertion que le texte visible ne
     contient pas de messageId brut (chaîne courte alphanumérique type
     `GdgCoi`) — couvre l'incident #2 en conditions réelles.
   - C'est le test qui aurait attrapé les deux incidents.

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

---

## Livraison — phase 1 : smoke de boot production-build (2026-05-23)

**Périmètre §1 « Smoke de boot »** : livré et bloquant en CI. Les
parcours auth (§2), métier (§3) et responsive (§4) restent à faire —
ticket gardé pending.

### Ce qui est en place

Une suite Playwright dédiée qui exerce la console sur le **vrai build**
(non plus sur le dev server Vite, qui ne reproduisait ni les chunks
découpés ni les catalogues Lingui compilés). Tourne via `vite preview`
sur le `dist/`.

- `console/playwright.prod-smoke.config.ts` — config Playwright dédiée
  (`webServer` = `npm run build && vite preview --config vite.preview.config.ts`)
- `console/vite.preview.config.ts` — config Vite minimale **sans** le
  `server.https` du dev server (sinon le healthcheck `webServer.url`
  Playwright timeout sur cert self-signed)
- `console/e2e-prod-smoke/helpers.ts` — `trackConsoleErrors` (capte
  `console.error` + `pageerror` uncaught), `mockConfigJs`, `defaultApiStub`,
  `waitForAppMount` (attend que le splash inline soit remplacé par le
  tree React), `findVisibleLinguiHashes` (heuristique 6-8 chars
  alphanum + 2 majuscules + 2 minuscules)
- `console/e2e-prod-smoke/boot-smoke.spec.ts` — 2 specs : SignIn et
  `/console/` racine. Assert wordmark visible, card `Sign In` rendue,
  zéro uncaught JS, zéro hash i18n.
- `console/e2e-prod-smoke/chunks-integrity.spec.ts` — 2 specs : aucun
  chunk JS/CSS en 4xx/5xx, aucun chunk de taille 0, et si `react-vendor-*`
  existe alors aucun chunk `antd-*` / `tanstack-*` / `rc-*` séparé
  (ceinture runtime du check statique).
- `console/e2e-prod-smoke/i18n-no-hashes.spec.ts` — itère sur les
  routes publiques (signin, logout), assert aucun hash Lingui visible
  + aucune erreur JS Lingui.

### Scripts npm

```bash
cd console
npm run test:prod-smoke              # build complet + tests
npm run test:prod-smoke:skip-build   # réutilise dist/ existant (itération locale)
```

### Wiring CI — bloquant

Job `e2e-console-prod-smoke` ajouté dans
`.github/workflows/veridian-ci.yml` :

- `needs: console-build` (récupère le `dist/` via `actions/upload-artifact` →
  `actions/download-artifact`, pas de rebuild = pas de +5 min)
- Installe Playwright Chromium, lance `npm run test:prod-smoke:skip-build`
- Artefact `playwright-report-prod-smoke` uploadé en cas d'échec
- Ajouté aux `needs:` du job `build` → si rouge, **pas de deploy staging**

### Pourquoi cette couche aurait attrapé les 2 incidents

| Incident | Mécanisme de détection |
|---|---|
| #1 `createContext undefined` (manualChunks) | `trackConsoleErrors` capte `pageerror` (uncaught exception JS) → `boot-smoke.spec.ts` fail dès le 1er goto |
| #2 hash Lingui (`GdgCoi`, `8wOKeG`) | `findVisibleLinguiHashes` scanne le DOM textNode-par-textNode → `boot-smoke.spec.ts` ET `i18n-no-hashes.spec.ts` fail |

### Validation locale (2026-05-23)

```
Running 6 tests using 1 worker
  ✓  boot-smoke.spec.ts › SignIn page monte sans erreur JS ni hash i18n (13.1s)
  ✓  boot-smoke.spec.ts › Page racine /console/ monte sans erreur JS (6.3s)
  ✓  chunks-integrity.spec.ts › aucun chunk JS/CSS ne 404 ou body vide (3.6s)
  ✓  chunks-integrity.spec.ts › react-vendor atomique (3.5s)
  ✓  i18n-no-hashes.spec.ts › SignIn aucun hash Lingui (3.2s)
  ✓  i18n-no-hashes.spec.ts › Logout aucun hash Lingui (2.4s)
  6 passed (59.8s)
```

### Reste à faire (re-priorisation)

- §2 Parcours authentification (login magic code, auto-login token,
  token expiré) → demande un tenant staging dédié + mock magic code
- §3 Parcours workspace (sidebar nav, écrans Dashboard/Contacts/Lists…)
  → demande fixture `authenticatedPage` étendue pour le mode prod-build
- §4 Parcours métier (créer contact, template, broadcast)
- §5 Responsive 375 / 768 / 1440 px

Une bonne suite de ces 4 chantiers tournerait sur staging (data réelle)
plutôt que sur le preview local — voir convention `@prod-safe` dans
`tests/e2e-veridian/`.
