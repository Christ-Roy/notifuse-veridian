// === Veridian patch — suite E2E « production-build smoke » (ticket 2026-05-22) ===
//
// LE test qui aurait attrapé les 2 incidents prod du 2026-05-22 :
//
//   1. Console morte au boot (manualChunks Vite mal configuré) :
//      `Cannot read properties of undefined (reading 'createContext')`
//      → ce test fail sur `pageerror` (trackConsoleErrors capte les
//      uncaught exceptions JS).
//
//   2. Caractères aléatoires i18n (catalogues Lingui non extraits) :
//      `GdgCoi`, `8wOKeG` affichés en lieu et place du texte
//      → ce test fail sur la regex `LINGUI_HASH_REGEX`.
//
// Ce qui se passe en réel :
//   - Vite build (npm run build) → dist/ avec chunks découpés
//   - Vite preview sert dist/ exactement comme Caddy/Nginx en prod
//   - Playwright headfull charge /console/signin
//   - On vérifie : pas d'uncaught JS, splash remplacé, wordmark visible,
//     zéro hash i18n affiché

import { test, expect } from '@playwright/test'
import {
  trackConsoleErrors,
  mockConfigJs,
  defaultApiStub,
  waitForAppMount,
  findVisibleLinguiHashes
} from './helpers'

test.describe('Production build — boot smoke', () => {
  test.beforeEach(async ({ page }) => {
    await mockConfigJs(page)
    await defaultApiStub(page)
  })

  test('SignIn page monte sur le build de prod sans erreur JS ni hash i18n', async ({ page }) => {
    const errors = trackConsoleErrors(page)

    await page.goto('/console/signin', { waitUntil: 'domcontentloaded' })

    // 1. La SPA doit monter (splash remplacé par le tree React).
    await waitForAppMount(page)

    // 2. Le wordmark Veridian est visible — c'est notre signal que le
    //    layout SignIn est rendu (`<VeridianLogo>` avec aria-label).
    const wordmark = page.locator('[aria-label="veridian.mail"]').first()
    await expect(wordmark).toBeVisible({ timeout: 10000 })

    // 3. La carte « Sign In » est rendue (composant Antd, dépend de
    //    React.createContext — c'est CE composant qui crashait dans
    //    l'incident #1).
    const signInCard = page
      .locator('.ant-card-head-title')
      .filter({ hasText: /Sign In|Connexion/i })
    await expect(signInCard).toBeVisible({ timeout: 10000 })

    // 4. Input email présent (validation que Form/Input Antd marchent).
    await expect(page.locator('input[type="email"]')).toBeVisible()

    // 5. Aucune uncaught exception JS (`pageerror`). C'EST la garde
    //    contre l'incident #1 « createContext undefined ».
    expect(
      errors,
      `Erreurs JS uncaught au boot du build de prod :\n${errors.join('\n---\n')}`
    ).toEqual([])

    // 6. Aucun hash i18n affiché. C'EST la garde contre l'incident #2
    //    « GdgCoi / 8wOKeG ».
    const hashes = await findVisibleLinguiHashes(page)
    expect(
      hashes,
      `Hashes Lingui suspects visibles à l'écran (catalogues pas extraits ?) : ${JSON.stringify(hashes)}`
    ).toEqual([])
  })

  test('Page racine /console/ monte sans erreur JS', async ({ page }) => {
    const errors = trackConsoleErrors(page)

    await page.goto('/console/', { waitUntil: 'domcontentloaded' })
    await waitForAppMount(page)

    // L'URL doit avoir bougé vers signin (utilisateur non loggé) ou rester
    // sur / avec une page valide. Dans tous les cas : pas d'erreur JS.
    expect(
      errors,
      `Erreurs JS uncaught au boot de /console/ :\n${errors.join('\n---\n')}`
    ).toEqual([])

    // Le wordmark doit être visible quelque part (splash, sidebar ou
    // signin — selon où on a atterri).
    const anyWordmark = page.locator('[aria-label="veridian.mail"]').first()
    await expect(anyWordmark).toBeVisible({ timeout: 10000 })
  })
})
