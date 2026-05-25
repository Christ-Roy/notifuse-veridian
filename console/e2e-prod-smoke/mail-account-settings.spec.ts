// === Veridian patch — suite E2E prod-smoke pour Mail account settings ===
//
// Vague 6 (ticket `2026-05-25-mail-send-as-user-via-hub-gateway.md` §3.1).
//
// Vérifie que la section "Mail account" du WorkspaceSettingsPage monte en
// prod (build Vite servi par preview) sans erreur JS, avec :
//   - Le titre "Mail account"
//   - Le radio sender (smtp_generic / hub_gmail)
//   - Le bouton "Connect my Gmail"
//   - Aucun hash i18n affiché
//
// On NE teste PAS le redirect réel vers Hub (test prod réel = trop complexe :
// OAuth Google consent + callback). On stub la route mail-provider-choice.
//
// Mode auth : on injecte un token bidon dans localStorage avant nav vers
// /console/workspace/<id>/settings/mail-account et on stub les endpoints
// API attendus. Ça permet de valider que le composant monte avec ses i18n,
// son radio et son bouton, sans tester le flow auth réel.

import { test, expect } from '@playwright/test'
import {
  trackConsoleErrors,
  mockConfigJs,
  defaultApiStub,
  waitForAppMount,
  findVisibleLinguiHashes
} from './helpers'

const WORKSPACE_ID = 'ws-smoke-mail-account'

test.describe('Mail account settings — prod-smoke', () => {
  test.beforeEach(async ({ page }) => {
    await mockConfigJs(page)
    await defaultApiStub(page)

    // Stub spécifique : auth user.me OK + workspaces + mail-provider-choice
    await page.route('https://prod-smoke-stub.invalid/**', (route) => {
      const url = route.request().url()
      if (url.includes('/api/user.me')) {
        return route.fulfill({
          status: 200,
          contentType: 'application/json',
          body: JSON.stringify({
            user: { id: 'u1', email: 'test@example.com', name: 'Smoke User' }
          })
        })
      }
      if (url.includes('/api/workspaces.list')) {
        return route.fulfill({
          status: 200,
          contentType: 'application/json',
          body: JSON.stringify({
            workspaces: [{ id: WORKSPACE_ID, name: 'Smoke Workspace' }]
          })
        })
      }
      if (url.includes('/api/workspaces.members')) {
        return route.fulfill({
          status: 200,
          contentType: 'application/json',
          body: JSON.stringify({
            members: [{ user_id: 'u1', role: 'owner' }]
          })
        })
      }
      if (url.includes('/mail-provider-choice')) {
        return route.fulfill({
          status: 200,
          contentType: 'application/json',
          body: JSON.stringify({
            workspace_id: WORKSPACE_ID,
            choice: 'smtp_generic',
            updated_at: '2026-05-25T00:00:00Z'
          })
        })
      }
      return route.fulfill({
        status: 200,
        contentType: 'application/json',
        body: '{}'
      })
    })

    // Inject auth token avant nav pour bypass redirect signin.
    await page.addInitScript(() => {
      window.localStorage.setItem('auth_token', 'smoke-token')
    })
  })

  test('section Mail account monte avec radio + bouton + sans hash i18n', async ({ page }) => {
    const errors = trackConsoleErrors(page)

    await page.goto(`/console/workspace/${WORKSPACE_ID}/settings/mail-account`, {
      waitUntil: 'domcontentloaded'
    })
    await waitForAppMount(page)

    // Titre H2 "Mail account"
    await expect(page.getByRole('heading', { name: /Mail account/i })).toBeVisible({
      timeout: 15000
    })

    // Radio "Veridian generic sender (default)"
    await expect(
      page.getByRole('radio', { name: /Veridian generic sender/i })
    ).toBeVisible({ timeout: 10000 })

    // Radio "My Gmail connected via Hub"
    await expect(
      page.getByRole('radio', { name: /My Gmail connected via Hub/i })
    ).toBeVisible()

    // Bouton "Connect my Gmail"
    await expect(page.getByRole('button', { name: /Connect my Gmail/i })).toBeVisible()

    // Aucune uncaught JS exception
    expect(
      errors,
      `Erreurs JS uncaught sur /settings/mail-account :\n${errors.join('\n---\n')}`
    ).toEqual([])

    // Aucun hash i18n affiché
    const hashes = await findVisibleLinguiHashes(page)
    expect(
      hashes,
      `Hashes Lingui suspects visibles sur /settings/mail-account : ${JSON.stringify(hashes)}`
    ).toEqual([])
  })
})
