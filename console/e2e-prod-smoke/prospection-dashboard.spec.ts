// Lot 5 : tableau de bord de prospection sur le BUILD DE PRODUCTION (chunks découpés,
// catalogues Lingui compilés), API simulée. Seul endroit où la vraie macro `t` (variables
// nommées) et le catalogue compilé s'exécutent: les tests vitest mockent la macro.

import { test, expect, type Page, type Route } from '@playwright/test'
import { trackConsoleErrors, mockConfigJs, waitForAppMount, findVisibleLinguiHashes } from './helpers'
import { mockUser, mockWorkspace, mockWorkspaceMembers } from '../e2e/fixtures/mock-data'
import { overviewOf, planClass, profile } from '../src/components/sending_profiles/veridian_profile_test_fixtures'

const WORKSPACE_ID = mockWorkspace.id

const overview = {
  ...overviewOf(
    [
      profile('nord', { name: 'nord-propre-1' }, {
        sent_today: 149,
        reserved_today: 149,
        daily_cap_today: 300,
        classes: [
          planClass('google'),
          planClass('microsoft', { slowdown_factor: 4, slowdown_reason: 'hard_bounce_rate', slowdown_rate: 0.099, sent_7d: 40 })
        ]
      }),
      profile('transac', { name: 'transactionnel-asd', usage: 'transactional', in_rotation: false }, {
        applicable: false,
        mode: 'transactional',
        sent_today: 900,
        daily_cap_today: null
      })
    ],
    {
      totals: {
        commercial_sent_today: 149,
        transactional_sent_today: 900,
        commercial_capacity_today: 300,
        active_commercial_profiles: 1,
        paused_profiles: 0
      }
    }
  ),
  transactional_watch: {
    profile_id: 'transac',
    profile_name: 'transactionnel-asd',
    level: 'alert',
    blocking: false,
    sent_today: 900,
    baseline_per_day: 10,
    sent_7d: 970,
    hard_bounce_rate_7d: 0,
    complaint_rate_7d: 0,
    policy_refusal_rate_7d: 0,
    alerts: [{ code: 'volume_spike', level: 'alert', value: 900, watch_threshold: 100, alert_threshold: 200, message: 'x' }]
  }
}

const stats = {
  generated_at: '2026-10-08T08:00:00Z',
  since: null,
  until: null,
  totals: { stock_remaining: 20000, queued_first_mail: 27000 },
  sequences: [
    {
      automation_id: 'devenir',
      name: 'E-commerce à devenir',
      status: 'live',
      list_id: 'ecomdevenir',
      enrolled: 3590,
      completed: 0,
      failed: 1,
      stages: [
        { stage: 1, label: 'J0', node_ids: ['j0a'], sent: 759, queued: 2773, waiting: 0, exited_after: 16 },
        { stage: 2, label: 'J+4', node_ids: ['j4'], sent: 10, queued: 10, waiting: 733, exited_after: 2 }
      ],
      exits: { replied: 16, rejected: 0, unsubscribed: 0, excluded: 83, other: 0, total: 99, before_first_mail: 83 },
      replies_human: 12,
      replies_auto: 1,
      sent_contacts: 240,
      reply_rate_human: 0.05
    }
  ],
  segments: [
    {
      list_id: 'ecomdevenir',
      name: 'E-commerce à devenir',
      sequence_ids: ['devenir'],
      active: 3300,
      bounced: 133,
      unsubscribed: 181,
      complained: 0,
      never_contacted: 2500,
      replies_human: 12,
      replies_auto: 1,
      sent_contacts: 240,
      reply_rate_human: 0.05
    }
  ]
}

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
  await page.route('**/api/**', async (route: Route) => {
    const request = route.request()
    const url = request.url()
    const type = request.resourceType()
    if (type !== 'fetch' && type !== 'xhr') return route.continue()
    const cors = {
      'access-control-allow-origin': '*',
      'access-control-allow-headers': '*',
      'access-control-allow-methods': 'GET,POST,OPTIONS'
    }
    if (request.method() === 'OPTIONS') return route.fulfill({ status: 204, headers: cors })
    const json = (data: unknown) =>
      route.fulfill({ status: 200, contentType: 'application/json', headers: cors, body: JSON.stringify(data) })
    if (url.includes('/api/user.me')) return json({ user: mockUser, workspaces: [workspace] })
    if (url.includes('/api/veridian/emailProfiles.overview')) return json(overview)
    if (url.includes('/api/veridian/prospection.stats')) return json(stats)
    if (url.includes('/api/veridian/messages.replyStats')) return json({ replied: 13, replied_human: 12 })
    if (url.includes('/api/workspaces.members')) return json(mockWorkspaceMembers)
    if (url.includes('/api/workspaces.get')) return json({ workspace })
    if (url.includes('/api/workspaces.list')) return json({ workspaces: [workspace] })
    if (url.includes('/api/analytics.query')) {
      const body = request.postDataJSON() as { query: { dimensions: string[]; measures: string[]; timeDimensions?: Array<{ granularity: string }> } }
      const q = body.query
      if (q.dimensions?.includes('veridian_profile_id')) {
        return json({
          data: [{ veridian_profile_id: 'nord', [`sent_at_${q.timeDimensions?.[0]?.granularity}`]: '2026-10-08T09:00:00', count_sent: 149 }],
          meta: { total: 1, query: '', params: [] }
        })
      }
      return json({ data: [], meta: { total: 0, query: '', params: [] } })
    }
    return json({})
  })
}

test.describe('Production build — tableau de bord de prospection', () => {
  test('anglais: tuiles, envois par relais, avancement, réponses, réputation, bandeau transactionnel', async ({ page }) => {
    const errors = trackConsoleErrors(page)
    await stubApi(page, 'en')
    await page.goto(`/console/workspace/${WORKSPACE_ID}`, { waitUntil: 'domcontentloaded' })
    await waitForAppMount(page)

    await expect(page.getByTestId('tile-sent-today')).toContainText('149 / 300', { timeout: 20000 })
    await expect(page.getByTestId('tile-stock')).toContainText('20,000')
    await expect(page.getByText('Sends per relay')).toBeVisible()
    await expect(page.getByTestId('today-nord')).toHaveText('149 / 300')
    await expect(page.getByTestId('stage-1')).toContainText('2,773 in the queue, 0 waiting, 16 left after')
    await expect(page.getByTestId('exits-devenir')).toContainText('16 replied, 0 rejected, 0 unsubscribed, 83 excluded')
    await expect(page.getByText('Replies by sequence')).toBeVisible()
    await expect(page.getByText('Slowed ÷4')).toBeVisible()
    await expect(page.getByTestId('transactional-watch-banner')).toContainText(
      'Unusual volume: 900 mails today, 10 per day on average over the previous 7 days'
    )

    if (process.env.SHOT_DIR) await page.screenshot({ path: `${process.env.SHOT_DIR}/prospection-en.png`, fullPage: true })
    expect(await findVisibleLinguiHashes(page)).toEqual([])
    expect(errors).toEqual([])
  })

  test('français: le catalogue compilé et les variables nommées', async ({ page }) => {
    const errors = trackConsoleErrors(page)
    await stubApi(page, 'fr')
    await page.goto(`/console/workspace/${WORKSPACE_ID}`, { waitUntil: 'domcontentloaded' })
    await waitForAppMount(page)

    await expect(page.getByText('Envois par relais')).toBeVisible({ timeout: 20000 })
    await expect(page.getByTestId('stage-1')).toContainText('2')
    await expect(page.getByTestId('stage-1')).toContainText('en file')
    await expect(page.getByTestId('exits-devenir')).toContainText('16 réponses, 0 rejets, 0 désinscriptions, 83 exclusions')
    await expect(page.getByText('Avancement des séquences')).toBeVisible()
    await expect(page.getByText('Réponses par séquence')).toBeVisible()
    await expect(page.getByText('Ralenti ÷4')).toBeVisible()
    await expect(page.getByTestId('transactional-watch-banner')).toContainText('Volume inhabituel : 900 mails aujourd')

    if (process.env.SHOT_DIR) await page.screenshot({ path: `${process.env.SHOT_DIR}/prospection-fr.png`, fullPage: true })
    expect(await findVisibleLinguiHashes(page)).toEqual([])
    expect(errors).toEqual([])
  })
})
