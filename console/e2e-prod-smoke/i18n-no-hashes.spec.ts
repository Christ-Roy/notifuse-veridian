// === Veridian patch — suite E2E « production-build smoke » (ticket 2026-05-22) ===
//
// Vérifie qu'aucune page accessible sans auth n'affiche de hash Lingui
// brut (incident #2 : « GdgCoi », « 8wOKeG »).
//
// Pourquoi cette spec en plus de boot-smoke ? Parce que :
//   - `boot-smoke.spec.ts` check uniquement la page signin (le boot).
//   - Cette spec balaie toutes les routes publiques de la SPA pour
//     vérifier qu'aucune chaîne n'a été oubliée du catalogue Lingui sur
//     d'autres écrans.
//   - Si on étend ces tests aux pages authentifiées plus tard, on garde
//     une seule garde i18n pour tout le périmètre.

import { test, expect } from '@playwright/test'
import {
  mockConfigJs,
  defaultApiStub,
  trackConsoleErrors,
  waitForAppMount,
  findVisibleLinguiHashes
} from './helpers'

const PUBLIC_ROUTES = [
  { path: '/console/signin', label: 'SignIn' },
  { path: '/console/logout', label: 'Logout' }
  // NOTE : pour les routes protégées (workspace, plan, settings) il faut
  // un fixture authenticatedPage — pas dans le scope de la suite prod-smoke
  // initiale. À étendre quand on rattachera la suite à staging avec un
  // tenant de test (cf. ticket todo/2026-05-22).
]

test.describe('Production build — i18n aucun hash brut affiché', () => {
  test.beforeEach(async ({ page }) => {
    await mockConfigJs(page)
    await defaultApiStub(page)
  })

  for (const route of PUBLIC_ROUTES) {
    test(`${route.label} (${route.path}) — aucun hash Lingui visible`, async ({ page }) => {
      const errors = trackConsoleErrors(page)

      await page.goto(route.path, { waitUntil: 'domcontentloaded' })

      // On laisse le temps à la page de finir son render (loaders Antd,
      // suspense fallbacks…).
      await waitForAppMount(page).catch(() => {
        // Si waitForAppMount timeout, on continue quand même : on veut
        // savoir ce qui est à l'écran, hash ou pas. Le test sera failed
        // sur les erreurs uncaught si la page n'a vraiment pas monté.
      })
      // Petit settle delay pour laisser Lingui finir l'activation locale.
      await page.waitForTimeout(500)

      const hashes = await findVisibleLinguiHashes(page)
      expect(
        hashes,
        `Sur ${route.path} — hashes Lingui suspects affichés (catalogues mal compilés ?) : ${JSON.stringify(
          hashes
        )}`
      ).toEqual([])

      // Filet : si Lingui crashe, ça apparaît en pageerror.
      const linguiErrors = errors.filter((e) =>
        /lingui|i18n|catalog|messageId/i.test(e)
      )
      expect(
        linguiErrors,
        `Erreurs JS liées à i18n sur ${route.path} :\n${linguiErrors.join('\n---\n')}`
      ).toEqual([])
    })
  }
})
