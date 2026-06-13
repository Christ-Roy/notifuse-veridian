# E2E mobile-responsive : test Drawer 320px hang systématiquement (skippé)

> **Sévérité** : 🟡 P1
> **Owner** : agent notifuse-veridian
> **Créé** : 2026-05-31

## Problème

`console/e2e-prod-smoke/mobile-responsive.spec.ts:114` ("viewport 320×568 :
hamburger visible, sidebar masquée, Drawer ouvrable") **échoue
systématiquement** en CI (timeout 60s sur `hamburger.click()` ligne 133, échoue
aussi en retry #1). Le `await expect(drawer).toBeVisible()` ne passe jamais : le
Drawer Antd ne s'ouvre pas de façon fiable sous le runner headless à 320px.

Introduit par le lot mobile responsive (commits 6ef902bd, 8f90b538). Bloquait la
promo prod de fixes CRITIQUES (fix HubSyncDead qui débloque les broadcasts, flow
Gmail direct). **Skippé le 2026-05-31** (`test.skip`) pour débloquer la chaîne.

## Ce qui reste couvert (le skip ne crée pas de trou béant)

Les autres tests mobile du même fichier passent : hamburger absent + Sider
présent à 768px, bascule mobile→tablette, media queries CSSOM. Seule
**l'ouverture du Drawer au click** à 320px est instable.

## À faire pour réparer

1. Reproduire en local : `cd console && npx playwright test e2e-prod-smoke/mobile-responsive.spec.ts --project=chromium-prod-build` à 320px.
2. Hypothèses : le `getByTestId('mobile-menu-toggle').click()` cible un élément
   pas encore interactif (re-render Antd), OU le Drawer a une animation/transition
   qui fait rater le `.ant-drawer-open` dans la fenêtre de 5s. Essayer
   `force: true` sur le click, ou attendre la fin de transition, ou augmenter le
   timeout du toBeVisible.
3. Une fois fiable, retirer le `test.skip` (ligne 114).

## Note

Ce n'est PAS lié au refactor mail stand-alone (2026-05-31) — c'est un test
préexistant fragile qui s'est révélé bloquant en le déclenchant.

## Réponse — 2026-06-13 (cleaner)

Correctif posé (commit bde96c0c) mais test **toujours `.skip`** : on ne dé-skip
pas à l'aveugle un test qui a bloqué la promo prod.

**Cause identifiée** : race entre `Grid.useBreakpoint()` (Antd évalue `isMobile`
async après mount via matchMedia) et `hamburger.click()`. Le Drawer mobile est
rendu `{isMobile && <Drawer>}` ; cliquer avant la stabilisation du breakpoint
passe `drawerOpen=true` sans que le Drawer soit monté → `.ant-drawer-open`
n'apparaît jamais → timeout 60s.

**Correctif** (dans le corps du test) : (1) attendre la disparition du Sider
desktop AVANT le click (barrière de synchro : Sider absent ⇒ isMobile=true ⇒
Drawer monté), (2) attendre l'état FINAL (menu item visible) avec timeout
généreux pour l'animation headless.

**Pour clore** : lancer le E2E prod-smoke réel
(`cd console && npx playwright test e2e-prod-smoke/mobile-responsive.spec.ts`
avec le `.skip` retiré localement), confirmer 3 runs verts d'affilée, PUIS
retirer le `.skip` (L121) + archiver ce ticket. Non fait ici : machine saturée
(bridage build E2E lourd interdit en parallèle des autres agents).
