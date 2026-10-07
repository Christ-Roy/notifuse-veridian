// Lot 3 « page Profils d'envoi » : parcours sur le BUILD DE PRODUCTION (chunks
// découpés, catalogues Lingui compilés), API simulée. C'est le seul endroit où
// la vraie macro `t` (variables nommées) et le catalogue français compilé
// s'exécutent: les tests vitest mockent la macro.

import { test, expect, type Page, type Route } from '@playwright/test'
import { trackConsoleErrors, mockConfigJs, waitForAppMount, findVisibleLinguiHashes } from './helpers'
import { mockUser, mockWorkspace, mockWorkspaceMembers } from '../e2e/fixtures/mock-data'
import {
  overviewOf,
  planClass,
  profile
} from '../src/components/sending_profiles/veridian_profile_test_fixtures'

const WORKSPACE_ID = mockWorkspace.id

const overview = overviewOf(
  [
    profile('nord', { name: 'nord-propre-1' }, {
      sent_today: 149,
      reserved_today: 149,
      daily_cap_today: 300,
      classes: [
        planClass('google'),
        planClass('security_gateway', { slowdown_factor: 2, slowdown_reason: 'hard_bounce_rate', slowdown_rate: 0.099 })
      ],
      blocked_by: ['window_closed'],
      window: {
        configured: true,
        open_now: false,
        next_open_at: '2099-01-02T06:00:00Z',
        timezone: 'Europe/Paris',
        source: 'profile'
      }
    }),
    profile('relais', { name: 'relais-agence-2', in_rotation: true }, {
      sent_today: 138,
      reserved_today: 138,
      daily_cap_today: 200,
      limiting_gate: 'profile_cap',
      warmup: { active: false },
      excluded_classes: ['ionos']
    }),
    profile('transac', { name: 'transactionnel-asd', usage: 'transactional', in_rotation: false }, {
      applicable: false,
      mode: 'transactional',
      sent_today: 4,
      daily_cap_today: null
    })
  ],
  {
    global_inboxes: [
      {
        integration_id: 'g1',
        name: 'Return inbox',
        host: 'imap.larksuite.com',
        address: 'robert.brunon@veridian.site',
        folder: 'INBOX',
        linked_profiles: []
      }
    ]
  }
)

const workspace = {
  ...mockWorkspace,
  settings: { ...mockWorkspace.settings, timezone: 'UTC', email_tracking_enabled: false, default_language: 'en', languages: ['en'] },
  integrations: []
}

async function stubApi(page: Page, locale: 'en' | 'fr') {
  await mockConfigJs(page)
  await page.addInitScript((loc) => {
    localStorage.setItem('auth_token', 'test-token-for-e2e')
    localStorage.setItem('locale', loc)
  }, locale)
  // La console appelle l'origine qui la sert (window.API_ENDPOINT absent): on intercepte /api/**.
  await page.route('**/api/**', (route: Route) => {
    const url = route.request().url()
    const type = route.request().resourceType()
    if (type !== 'fetch' && type !== 'xhr') return route.continue()
    // La console (127.0.0.1:4173) appelle un autre hôte avec Authorization: il faut le CORS et le préflight.
    const cors = {
      'access-control-allow-origin': '*',
      'access-control-allow-headers': '*',
      'access-control-allow-methods': 'GET,POST,OPTIONS'
    }
    if (route.request().method() === 'OPTIONS') return route.fulfill({ status: 204, headers: cors })
    const json = (data: unknown) =>
      route.fulfill({ status: 200, contentType: 'application/json', headers: cors, body: JSON.stringify(data) })
    if (url.includes('/api/user.me')) return json({ user: mockUser, workspaces: [workspace] })
    if (url.includes('/api/veridian/emailProfiles.overview')) return json(overview)
    if (url.includes('/api/workspaces.members')) return json(mockWorkspaceMembers)
    if (url.includes('/api/workspaces.get')) return json({ workspace })
    if (url.includes('/api/workspaces.list')) return json({ workspaces: [workspace] })
    return json({})
  })
}

test.describe('Production build — page Profils d\'envoi', () => {
  test('anglais: cartes, porte limitante, réputation, sections et boîtes globales', async ({ page }) => {
    const errors = trackConsoleErrors(page)
    await stubApi(page, 'en')
    await page.goto(`/console/workspace/${WORKSPACE_ID}/sending-profiles`, { waitUntil: 'domcontentloaded' })
    await waitForAppMount(page)

    await expect(page.getByText('nord-propre-1')).toBeVisible({ timeout: 20000 })
    const nord = page.getByTestId('profile-card-nord')
    await expect(nord.getByText('149 / 300 sent')).toBeVisible()
    await expect(nord.getByText('Warmup, day 3/5')).toBeVisible()
    await expect(nord.getByText('Anti-spam gateways: rate ÷2, 9.9% bounces')).toBeVisible()
    await expect(nord.getByText(/^Window closed until /)).toBeVisible()

    const relais = page.getByTestId('profile-card-relais')
    await expect(relais.getByText('138 / 200 sent')).toBeVisible()
    await expect(relais.getByText('Profile cap')).toBeVisible()
    await expect(relais.getByText('IONOS / 1&1')).toBeVisible()

    await expect(page.getByTestId('section-commercial').getByText('relais-agence-2')).toBeVisible()
    await expect(page.getByTestId('section-transactional').getByText('transactionnel-asd')).toBeVisible()
    await expect(page.getByTestId('global-inboxes').getByText('robert.brunon@veridian.site')).toBeVisible()

    // Capture facultative (SHOT_DIR=/chemin) pour relire le rendu, sans effet en CI.
    if (process.env.SHOT_DIR) await page.screenshot({ path: `${process.env.SHOT_DIR}/profils-en.png`, fullPage: true })

    // la sidebar porte l'entrée, sélectionnée
    await expect(page.locator('.ant-menu-item-selected').getByText('Sending profiles')).toBeVisible()

    // assistant: trois choix, OAuth grisé
    await page.getByRole('button', { name: /Add a profile/ }).first().click()
    await expect(page.getByTestId('choice-smtp-imap')).toBeVisible()
    await page.getByTestId('choice-gmail-app-password').click()
    await expect(page.getByRole('link', { name: 'Open Google app passwords' })).toHaveAttribute(
      'href',
      'https://myaccount.google.com/apppasswords'
    )

    expect(await findVisibleLinguiHashes(page)).toEqual([])
    expect(errors).toEqual([])
  })

  test('français: le catalogue compilé et les variables nommées', async ({ page }) => {
    const errors = trackConsoleErrors(page)
    await stubApi(page, 'fr')
    await page.goto(`/console/workspace/${WORKSPACE_ID}/sending-profiles`, { waitUntil: 'domcontentloaded' })
    await waitForAppMount(page)

    await expect(page.getByText('nord-propre-1')).toBeVisible({ timeout: 20000 })
    const nord = page.getByTestId('profile-card-nord')
    await expect(nord.getByText('149 / 300 envoyés')).toBeVisible()
    await expect(nord.getByText('Chauffe, jour 3/5')).toBeVisible()
    await expect(nord.getByText(/^Passerelles anti-spam : débit ÷2, 9,9\s?% de rejets$/)).toBeVisible()
    await expect(nord.getByText(/^Fenêtre fermée jusqu'à /)).toBeVisible()
    await expect(page.getByTestId('section-transactional')).toContainText('Transactionnel')
    await expect(page.locator('.ant-menu-item-selected').getByText("Profils d'envoi")).toBeVisible()
    await expect(page.getByRole('button', { name: /Ajouter un profil/ }).first()).toBeVisible()

    if (process.env.SHOT_DIR) await page.screenshot({ path: `${process.env.SHOT_DIR}/profils-fr.png`, fullPage: true })
    await page.getByRole('button', { name: /Ajouter un profil/ }).first().click()
    await expect(page.getByText('Bientôt')).toBeVisible()
    if (process.env.SHOT_DIR) await page.screenshot({ path: `${process.env.SHOT_DIR}/assistant-fr.png` })

    expect(await findVisibleLinguiHashes(page)).toEqual([])
    expect(errors).toEqual([])
  })
})
