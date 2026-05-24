// === Veridian patch — console-suite : section Plan settings ===
//
// Couverture demandée par le team lead :
//   - Page Settings > Plan affiche le label du plan (Pro, Business, Free…)
//   - Badge plan_source visible si non-stripe (lifetime / manual / internal)
//   - Bouton "Manage subscription" présent + pointe vers le Hub
//
// Anti-régression spécifique : ce composant (`veridian_plan_settings.tsx`)
// a été touché par le pivot pricing 2026-05-21 + le polish UI 2026-05-22.
// C'est ici qu'on a vu les hash Lingui (« GdgCoi », « 8wOKeG ») en prod.
//
// 1 tenant Pro éphémère + 1 tenant Enterprise éphémère (pour tester aussi
// la branche `plan_source` non-stripe via grant-unlimited).
// NON @prod-safe.

import { test, expect } from '@playwright/test'
import {
  NOTIFUSE_URL,
  newTenantId,
  provisionTenant,
  hmacFetch,
  wipeTenants,
  loginViaAutoLogin,
  trackConsoleErrors,
  waitForAppMount,
  findVisibleLinguiHashes,
  type ProvisioningResponse
} from './helpers'

const proTenantId = newTenantId()
const proOwner = `${proTenantId}@e2e.veridian.test`
const lifetimeTenantId = newTenantId()
const lifetimeOwner = `${lifetimeTenantId}@e2e.veridian.test`
const provisioned: string[] = [proTenantId, lifetimeTenantId]
let proProvisioning: ProvisioningResponse
let lifetimeProvisioning: ProvisioningResponse

test.beforeAll(async () => {
  proProvisioning = await provisionTenant(proTenantId, proOwner, 'pro')
  lifetimeProvisioning = await provisionTenant(lifetimeTenantId, lifetimeOwner, 'free')

  // Élève le 2e tenant en Enterprise via grant-unlimited → plan_source=lifetime_partner.
  // Le badge "Lifetime" devrait apparaître sur Settings > Plan.
  const res = await hmacFetch('/api/veridian/admin/grant-unlimited', 'POST', {
    tenant_id: lifetimeTenantId,
    reason: 'console-suite plan_source badge coverage'
  })
  if (res.status !== 200) {
    const txt = await res.text()
    throw new Error(`grant-unlimited failed (${res.status}): ${txt}`)
  }
})

test.afterAll(async () => {
  await wipeTenants(provisioned)
})

test.describe('Console — Settings > Plan section', () => {
  test('Tenant Pro affiche label "Pro" + bouton Manage subscription', async ({ page }) => {
    const errors = trackConsoleErrors(page)

    await loginViaAutoLogin(page, proProvisioning.auto_login_url)
    await waitForAppMount(page)
    await page.waitForTimeout(2000)

    await page.goto(`${NOTIFUSE_URL}/console/workspace/${proTenantId}/settings/plan`)
    await waitForAppMount(page)
    await page.waitForLoadState('networkidle', { timeout: 20_000 }).catch(() => {})

    // Le composant VeridianPlanSettings affiche le label "Pro" pour plan='pro'.
    // On cherche dans le body — sélecteur stable cross-i18n.
    const body = await page.locator('body').innerText()
    expect(
      body,
      `Page Plan ne contient pas "Pro" — section absente ou pas chargée. Body extrait :\n${body.slice(0, 500)}`
    ).toMatch(/\bPro\b/)

    // "Manage subscription" est le label EN (Lingui sourceLocale=en).
    // Si Robert traduit en FR, la chaîne devient "Gérer l'abonnement" — on
    // tolère les deux. Pattern volontairement large.
    expect(
      body,
      `Bouton Manage subscription absent — CTA vers Hub manquant ?`
    ).toMatch(/Manage subscription|Gérer.*abonnement|Manage.*plan/i)

    const hashes = await findVisibleLinguiHashes(page)
    expect(
      hashes,
      `Hash Lingui sur Settings > Plan (Pro tenant) : ${hashes.join(', ')}`
    ).toEqual([])

    expect(errors, `Erreurs JS Settings > Plan :\n${errors.join('\n---\n')}`).toEqual([])
  })

  test('Tenant lifetime_partner affiche badge plan_source non-Stripe', async ({ page }) => {
    const errors = trackConsoleErrors(page)

    await loginViaAutoLogin(page, lifetimeProvisioning.auto_login_url)
    await waitForAppMount(page)
    await page.waitForTimeout(2000)

    await page.goto(`${NOTIFUSE_URL}/console/workspace/${lifetimeTenantId}/settings/plan`)
    await waitForAppMount(page)
    await page.waitForLoadState('networkidle', { timeout: 20_000 }).catch(() => {})

    const body = await page.locator('body').innerText()

    // Plan = enterprise après grant-unlimited.
    expect(
      body,
      `Page Plan ne reflète pas le grant Enterprise — body :\n${body.slice(0, 500)}`
    ).toMatch(/\bEnterprise\b/)

    // Badge VeridianPlanSourceBadge affiche un terme reconnaissable pour
    // plan_source=lifetime_partner. Le composant peut afficher "Lifetime",
    // "Lifetime partner", "Partner", ou un emoji. Pattern souple — l'idée
    // est de confirmer que le badge n'est pas vide.
    expect(
      body,
      `Badge plan_source absent ou vide pour lifetime_partner — body :\n${body.slice(0, 500)}`
    ).toMatch(/Lifetime|Partner|Internal|Manual/i)

    const hashes = await findVisibleLinguiHashes(page)
    expect(
      hashes,
      `Hash Lingui sur Settings > Plan (lifetime) : ${hashes.join(', ')}`
    ).toEqual([])

    expect(errors, `Erreurs JS Settings > Plan lifetime :\n${errors.join('\n---\n')}`).toEqual([])
  })
})
