// Incident 19/10/2026 : « Failed to fetch dynamically imported module » apres un deploiement.
// Le build de prod doit se recharger UNE fois sur une erreur de preload (evenement Vite), puis
// afficher un message et non boucler si l'erreur persiste.
import { test, expect } from '@playwright/test'
import { mockConfigJs, defaultApiStub, waitForAppMount } from './helpers'

test.describe('Production build — chunk perime apres deploiement', () => {
  test.beforeEach(async ({ page }) => {
    await mockConfigJs(page)
    await defaultApiStub(page)
  })

  test('vite:preloadError : un rechargement, puis message sans boucle', async ({ page }) => {
    let documentLoads = 0
    page.on('request', (r) => {
      if (r.resourceType() === 'document' && r.url().includes('/console/signin')) documentLoads++
    })
    await page.goto('/console/signin', { waitUntil: 'domcontentloaded' })
    await waitForAppMount(page)
    expect(documentLoads).toBe(1)

    // 1re erreur : la page se recharge toute seule.
    await Promise.all([
      page.waitForEvent('load'),
      page.evaluate(() => window.dispatchEvent(new Event('vite:preloadError', { cancelable: true })))
    ])
    await waitForAppMount(page)
    expect(documentLoads).toBe(2)

    // 2e erreur dans la minute : message visible, pas de 3e chargement.
    await page.evaluate(() => window.dispatchEvent(new Event('vite:preloadError', { cancelable: true })))
    await expect(page.locator('#veridian-chunk-banner')).toBeVisible()
    await page.waitForTimeout(1500)
    expect(documentLoads).toBe(2)
  })
})
