// === Veridian patch — console-suite : boot console sur staging réel ===
//
// Couverture (§1 du ticket 2026-05-22-suite-e2e-playwright-console.md) :
//   - SignIn page monte sans erreur JS (chunks intègres, contextes init OK)
//   - Page racine /console/ monte (même sans auth — doit rediriger /signin)
//   - Aucun hash Lingui visible (incident #2)
//   - Aucun uncaught exception (incident #1 createContext)
//
// Tagué `@prod-safe` : aucun side-effect, aucun tenant créé, juste navigation
// vers des URLs publiques (signin, /console/) qui ne mutent rien. Peut tourner
// sur prod après promotion pour validation post-deploy.

import { test, expect } from '@playwright/test'
import {
  NOTIFUSE_URL,
  findVisibleLinguiHashes,
  trackConsoleErrors,
  waitForAppMount
} from './helpers'

test.describe('Console boot smoke — staging réel', () => {
  test('@prod-safe SignIn page monte sans erreur JS ni hash Lingui', async ({ page }) => {
    const errors = trackConsoleErrors(page)

    await page.goto(`${NOTIFUSE_URL}/console/signin`)
    await waitForAppMount(page)

    // Wordmark "Notifuse" doit être visible — preuve que React a hydraté la
    // header (composant assez haut dans le tree pour valider que createContext
    // et tous les providers parents marchent).
    // On utilise un sélecteur stable côté text/role plutôt que data-testid
    // (la console upstream n'a pas de testid, ajouter en patch Veridian
    // créerait des merge conflicts à chaque sync upstream).
    await expect(page.locator('body')).toContainText(/notifuse|sign in/i, { timeout: 15_000 })

    // Donne le temps aux chunks lazy de finir de charger (settings page,
    // toolbar antd, etc.) avant de scanner les hash.
    await page.waitForLoadState('networkidle', { timeout: 15_000 }).catch(() => {
      // networkidle peut timeout si l'app poll un endpoint en arrière-plan
      // (analytics, heartbeat). Non-fatal pour cette assertion.
    })

    const hashes = await findVisibleLinguiHashes(page)
    expect(
      hashes,
      `Hash Lingui détectés à l'écran (catalogue incomplet ou non extrait) : ${hashes.join(', ')}`
    ).toEqual([])

    expect(
      errors,
      `Erreurs JS au boot (regex bénignes appliquées) :\n${errors.join('\n---\n')}`
    ).toEqual([])
  })

  test('@prod-safe Page racine /console/ monte sans erreur JS', async ({ page }) => {
    const errors = trackConsoleErrors(page)

    await page.goto(`${NOTIFUSE_URL}/console/`)
    await waitForAppMount(page)

    // Sans auth, /console/ doit rediriger vers /signin OU /setup (si l'app
    // n'est pas encore installée). Les deux sont acceptables — on vérifie
    // juste que ce N'EST PAS un blank/crash.
    await page.waitForURL(/\/console\/(signin|setup|$)/, { timeout: 15_000 })
    await expect(page.locator('body')).not.toBeEmpty()

    await page.waitForLoadState('networkidle', { timeout: 15_000 }).catch(() => {})

    const hashes = await findVisibleLinguiHashes(page)
    expect(hashes, `Hash Lingui détectés : ${hashes.join(', ')}`).toEqual([])

    expect(errors, `Erreurs JS :\n${errors.join('\n---\n')}`).toEqual([])
  })

  test('@prod-safe Logout page monte sans erreur', async ({ page }) => {
    // Page très simple, mais souvent oubliée — couverture i18n + boot.
    const errors = trackConsoleErrors(page)

    await page.goto(`${NOTIFUSE_URL}/console/logout`)
    await waitForAppMount(page)
    await page.waitForLoadState('networkidle', { timeout: 10_000 }).catch(() => {})

    // Logout efface le token et redirect, l'URL finale peut être signin.
    const url = page.url()
    expect(url).toMatch(/\/console\/(logout|signin)/)

    const hashes = await findVisibleLinguiHashes(page)
    expect(hashes, `Hash Lingui sur Logout : ${hashes.join(', ')}`).toEqual([])

    expect(errors, `Erreurs JS sur Logout :\n${errors.join('\n---\n')}`).toEqual([])
  })
})
