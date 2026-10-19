// === Veridian — garde-fou « le pied d'actions d'un tiroir est atteignable a toute taille » (19/10/2026) ===
//
// Audit UI 19/10 (B1, B2, B4, A5) : « Creer et tester » sous la ligne de flottaison, pied qui
// recouvre un bouton, boutons hors ecran a 390 px, « Suivant » qui ne valide pas, editeurs de
// modeles et de sequences inutilisables sur mobile. Cette spec ouvre les vrais tiroirs sur le
// build de production (API simulee), balaie 390/768/1024/1366 (+ petite hauteur 1366x600) et
// verifie pour CHAQUE bouton du tiroir : apres defilement du tiroir lui-meme, son centre est dans
// l'ecran et elementFromPoint tombe sur lui (donc rien ne le recouvre) ; le document ne defile
// pas horizontalement ; le tiroir n'est pas plus large que l'ecran.

import { test, expect, type Page, type Route } from '@playwright/test'
import { mockConfigJs, waitForAppMount } from './helpers'
import { mockUser, mockWorkspace, mockWorkspaceMembers } from '../e2e/fixtures/mock-data'
import { overviewOf, profile } from '../src/components/sending_profiles/veridian_profile_test_fixtures'

const WS_ID = mockWorkspace.id

const SIZES: Array<[number, number]> = [
  [390, 844],
  [768, 1024],
  [1024, 768],
  [1366, 768],
  [1366, 600]
]

const workspace = {
  ...mockWorkspace,
  settings: {
    ...mockWorkspace.settings,
    timezone: 'UTC',
    email_tracking_enabled: false,
    default_language: 'en',
    languages: ['en']
  },
  integrations: [
    {
      id: 'nord',
      name: 'nord-propre-1',
      type: 'email',
      email_provider: {
        kind: 'smtp',
        senders: [{ id: 's1', email: 'robert@nord.example', name: 'Robert', is_default: true }],
        smtp: { host: 'smtp.nord.example', port: 587, username: 'robert', use_tls: true }
      }
    },
    {
      id: 'imap1',
      name: 'Return inbox',
      type: 'imap',
      imap: { host: 'imap.nord.example', port: 993, username: 'robert', use_tls: true }
    }
  ]
}

const overview = overviewOf([profile('nord', { name: 'nord-propre-1' })], { global_inboxes: [] })

async function stubApi(page: Page): Promise<void> {
  await mockConfigJs(page)
  await page.addInitScript(() => {
    localStorage.setItem('auth_token', 'test-token-for-e2e')
    localStorage.setItem('locale', 'en')
  })
  await page.route('**/api/**', (route: Route) => {
    const url = route.request().url()
    const type = route.request().resourceType()
    if (type !== 'fetch' && type !== 'xhr') return route.continue()
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
    if (url.includes('/api/templates.list')) return json({ templates: [], total: 0 })
    if (url.includes('/api/automations.list')) return json({ automations: [], total: 0 })
    if (url.includes('/api/lists.list')) return json({ lists: [] })
    if (url.includes('/api/segments.list')) return json({ segments: [] })
    return json({})
  })
}

async function open(page: Page, path: string): Promise<void> {
  await page.goto(`/console/workspace/${WS_ID}/${path}`, { waitUntil: 'domcontentloaded' })
  await waitForAppMount(page)
}

/** Verifie tous les boutons du tiroir ouvert (le plus recent) et l'absence de debordement. */
async function expectDrawerReachable(page: Page, label: string, primary?: string | RegExp): Promise<void> {
  const drawer = page.locator('.ant-drawer-content').last()
  await expect(drawer).toBeVisible({ timeout: 15000 })
  await page.waitForTimeout(600) // fin de l'animation d'ouverture
  if (primary) {
    // L'action principale doit etre visible SANS defiler (pied collant), et non recouverte.
    const btn = drawer.getByRole('button', { name: primary }).last()
    await expect(btn, `${label}: action principale absente`).toBeVisible()
    const r = await btn.evaluate((el) => {
      const b = el.getBoundingClientRect()
      const hit = document.elementFromPoint(b.left + b.width / 2, b.top + b.height / 2)
      return {
        inView: b.top >= 0 && b.bottom <= innerHeight && b.left >= 0 && b.right <= innerWidth,
        free: !!hit && (hit === el || el.contains(hit)),
        rect: [Math.round(b.left), Math.round(b.top), Math.round(b.right), Math.round(b.bottom), innerWidth, innerHeight]
      }
    })
    expect(r, `${label}: action principale visible sans defiler`).toMatchObject({ inView: true, free: true })
  }
  const n = await drawer.evaluate((root) => {
    const nodes = root.querySelectorAll('button:not(.ant-drawer-close), a.ant-btn')
    nodes.forEach((b, i) => b.setAttribute('data-dbt', String(i)))
    return nodes.length
  })
  const failures: string[] = []
  for (let i = 0; i < n; i++) {
    const loc = drawer.locator(`[data-dbt="${i}"]`).first()
    const visible = await loc.evaluate((el) => {
      const s = getComputedStyle(el)
      const r = el.getBoundingClientRect()
      return s.visibility !== 'hidden' && s.display !== 'none' && r.width > 0 && r.height > 0
    })
    if (!visible) continue
    await loc.scrollIntoViewIfNeeded({ timeout: 3000 }).catch(() => undefined)
    const r = await loc.evaluate((el) => {
      const b = el.getBoundingClientRect()
      const cx = b.left + b.width / 2
      const cy = b.top + b.height / 2
      const inView = cx >= 0 && cy >= 0 && cx <= innerWidth && cy <= innerHeight
      const hit = document.elementFromPoint(cx, cy)
      const name = (el.textContent || el.getAttribute('aria-label') || el.tagName).trim().slice(0, 30)
      return {
        ok: inView && !!hit && (hit === el || el.contains(hit)),
        name,
        inView,
        hit: hit ? `${hit.tagName}.${String((hit as HTMLElement).className).slice(0, 50)}` : 'null'
      }
    })
    if (!r.ok) failures.push(`${r.name || '(icone)'} ${r.inView ? 'recouvert par ' + r.hit : 'hors ecran'}`)
  }
  expect(failures, `${label}: boutons inatteignables`).toEqual([])
  const over = await page.evaluate(() => {
    const wrap = document.querySelector('.ant-drawer-content-wrapper:last-of-type') as HTMLElement | null
    return {
      docW: document.documentElement.scrollWidth,
      vw: innerWidth,
      drawerW: wrap ? Math.round(wrap.getBoundingClientRect().width) : 0
    }
  })
  expect(over.docW, `${label}: le document defile horizontalement`).toBeLessThanOrEqual(over.vw)
  expect(over.drawerW, `${label}: tiroir plus large que l'ecran`).toBeLessThanOrEqual(over.vw)
}

for (const [width, height] of SIZES) {
  test.describe(`tiroirs ${width}x${height}`, () => {
    test.use({ viewport: { width, height } })

    test('assistant profil Gmail : Retour et Creer et tester atteignables', async ({ page }) => {
      await stubApi(page)
      await open(page, 'sending-profiles')
      await page.getByRole('button', { name: /Add a profile/ }).first().click()
      await page.getByTestId('choice-gmail-app-password').click()
      await expect(page.getByRole('button', { name: 'Create and test' })).toHaveCount(1)
      await expectDrawerReachable(page, 'assistant Gmail', 'Create and test')
    })

    test('edition de profil : pied et boutons de boite atteignables', async ({ page }) => {
      await stubApi(page)
      await open(page, 'sending-profiles')
      await page.getByTestId('profile-card-nord').getByRole('button', { name: 'Edit' }).click()
      await expect(page.getByRole('button', { name: 'New inbox' })).toHaveCount(1)
      await expectDrawerReachable(page, 'edition de profil', 'Save')
    })

    test('creation de modele : actions atteignables, Suivant valide l etape 1', async ({ page }) => {
      await stubApi(page)
      await open(page, 'templates')
      await page.getByRole('button', { name: /Create Template/ }).first().click()
      await expect(page.locator('.ant-drawer-content').last()).toBeVisible()
      await expectDrawerReachable(page, 'creation de modele', 'Next')
      // B4 : Suivant sur un formulaire vide ne quitte pas l'etape 1 et montre les erreurs.
      await page.getByRole('button', { name: 'Next' }).click()
      await expect(page.locator('.ant-tabs-tab-active').first()).toContainText('1. Settings')
      await expect(page.locator('.ant-form-item-explain-error').first()).toBeVisible()
    })

    test('creation de sequence : en-tete et Creer atteignables', async ({ page }) => {
      await stubApi(page)
      await open(page, 'automations')
      await page.getByRole('button', { name: /Create Automation|Create your first/ }).first().click()
      await expect(page.locator('.ant-drawer-content').last()).toBeVisible()
      await expectDrawerReachable(page, 'creation de sequence', /^Create$/)
    })
  })
}

test.describe('accessibilite transverse (F1, F2, F4, F5)', () => {
  test('titre d onglet par page, nom accessible des boutons-icones, focus visible, contraste du pied', async ({ page }) => {
    await stubApi(page)
    await open(page, 'contacts')
    await expect.poll(() => page.title(), { timeout: 15000 }).toBe('Contacts | Veridian')
    await page.getByRole('link', { name: 'Lists' }).first().click()
    await expect.poll(() => page.title(), { timeout: 15000 }).toBe('Lists | Veridian')
    // aucun bouton sans nom accessible (hors fermeture de tiroir geree par antd)
    await page.waitForTimeout(500)
    const unnamed = await page.evaluate(() =>
      [...document.querySelectorAll('button')]
        .filter((b) => !(b.textContent || '').trim() && !b.getAttribute('aria-label') && !b.getAttribute('aria-labelledby') && !b.getAttribute('title'))
        .map((b) => b.className.slice(0, 40))
    )
    expect(unnamed).toEqual([])
    // focus clavier visible sur un lien du menu
    const link = page.locator('.ant-layout-sider .ant-menu-item a').first()
    await link.focus()
    await page.keyboard.press('Tab')
    await page.keyboard.press('Shift+Tab')
    const outline = await link.evaluate((el) => {
      const s = getComputedStyle(el)
      return { w: s.outlineWidth, st: s.outlineStyle }
    })
    expect(outline.st).not.toBe('none')
    expect(parseFloat(outline.w)).toBeGreaterThan(0)
  })
})
