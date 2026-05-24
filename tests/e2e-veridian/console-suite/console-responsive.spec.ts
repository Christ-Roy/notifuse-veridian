// === Veridian patch — console-suite : responsive layout ===
//
// Couverture (§5 du ticket) :
//   - 375 px (iPhone SE-ish) : mobile portrait
//   - 768 px (iPad portrait)
//   - 1440 px (desktop standard) — baseline
//
// Sentinelle scroll horizontal : un overflow X visible sur écran <= 768
// indique un layout cassé (composant trop large, table sans wrap, etc.).
// Le polish UI 2026-05-22 n'a validé qu'à 523px — ce test couvre les 3
// breakpoints standards mobile/tablet/desktop.
//
// 1 tenant éphémère (cleanup afterAll). NON @prod-safe.

import { test, expect, type Page } from '@playwright/test'
import {
  NOTIFUSE_URL,
  newTenantId,
  provisionTenant,
  wipeTenants,
  loginViaAutoLogin,
  trackConsoleErrors,
  waitForAppMount,
  findVisibleLinguiHashes,
  type ProvisioningResponse
} from './helpers'

const tenantId = newTenantId()
const ownerEmail = `${tenantId}@e2e.veridian.test`
const provisioned: string[] = [tenantId]
let provisioning: ProvisioningResponse

test.beforeAll(async () => {
  provisioning = await provisionTenant(tenantId, ownerEmail, 'pro')
})

test.afterAll(async () => {
  await wipeTenants(provisioned)
})

const VIEWPORTS = [
  { name: 'mobile-375', width: 375, height: 667 },
  { name: 'tablet-768', width: 768, height: 1024 },
  { name: 'desktop-1440', width: 1440, height: 900 }
]

const PAGES_TO_CHECK = [
  { slug: '', label: 'Dashboard' },
  { slug: 'contacts', label: 'Contacts' },
  { slug: 'templates', label: 'Templates' },
  { slug: 'settings', label: 'Settings' }
]

async function authenticate(page: Page): Promise<void> {
  await loginViaAutoLogin(page, provisioning.auto_login_url)
  await waitForAppMount(page)
  await page.waitForTimeout(3000)
}

async function expectNoHorizontalScroll(page: Page, label: string, viewport: string): Promise<void> {
  const overflow = await page.evaluate(() => {
    // documentElement.scrollWidth > clientWidth = overflow horizontal forcé.
    // On accepte 1px de slack pour les bordures sub-pixel des navigateurs.
    return {
      scrollWidth: document.documentElement.scrollWidth,
      clientWidth: document.documentElement.clientWidth
    }
  })
  expect(
    overflow.scrollWidth,
    `Overflow horizontal sur ${label} @ ${viewport} : scrollWidth=${overflow.scrollWidth} > clientWidth=${overflow.clientWidth}`
  ).toBeLessThanOrEqual(overflow.clientWidth + 1)
}

for (const vp of VIEWPORTS) {
  test.describe(`Console responsive @ ${vp.name} (${vp.width}px)`, () => {
    test.use({ viewport: { width: vp.width, height: vp.height } })

    for (const target of PAGES_TO_CHECK) {
      test(`${target.label} monte sans overflow horizontal`, async ({ page }) => {
        const errors = trackConsoleErrors(page)

        await authenticate(page)

        const url = `${NOTIFUSE_URL}/console/workspace/${tenantId}${target.slug ? '/' + target.slug : ''}`
        await page.goto(url)
        await waitForAppMount(page)
        await page.waitForLoadState('networkidle', { timeout: 20_000 }).catch(() => {})
        // Laisse le temps aux composants antd de finir leur layout (Table,
        // Sider collapse, etc.).
        await page.waitForTimeout(1500)

        await expectNoHorizontalScroll(page, target.label, vp.name)

        const bodyText = await page.locator('body').innerText()
        expect(
          bodyText.length,
          `Body vide sur ${target.label} @ ${vp.name}`
        ).toBeGreaterThan(50)

        const hashes = await findVisibleLinguiHashes(page)
        expect(
          hashes,
          `Hash Lingui sur ${target.label} @ ${vp.name} : ${hashes.join(', ')}`
        ).toEqual([])

        expect(
          errors,
          `Erreurs JS sur ${target.label} @ ${vp.name} :\n${errors.join('\n---\n')}`
        ).toEqual([])
      })
    }
  })
}
