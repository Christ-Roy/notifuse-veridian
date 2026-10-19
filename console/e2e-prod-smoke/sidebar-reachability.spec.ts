// === Veridian — garde-fou « la sidebar est atteignable a toute taille » (19/10/2026) ===
//
// Bug signale : sur certains ecrans, le bouton Settings n'etait pas cliquable car le pied de
// sidebar (version + bouton de repli, en position fixed) le recouvrait. Cette spec balaie
// les tailles d'ecran (dont petites hauteurs et zoom navigateur 125 %/150 %, equivalents a
// un viewport CSS reduit), sidebar depliee ET repliee, et verifie pour CHAQUE element
// cliquable de la sidebar (entrees de menu, bouton de repli) que le point central est bien
// celui de l'element lui-meme (elementFromPoint), apres un defilement eventuel de la nav.
// Settings recoit en plus un vrai clic qui doit naviguer vers les reglages.

import { test, expect, type Page, type Route } from '@playwright/test'
import { mockConfigJs, waitForAppMount } from './helpers'

const USER_ID = '00000000-0000-0000-0000-000000000002'
const WS_ID = 'sidebar-stub-ws'

async function stubAuthAndWorkspace(page: Page): Promise<void> {
  await page.route('**/api/**', (route: Route) => {
    const url = route.request().url()
    const type = route.request().resourceType()
    if (type !== 'fetch' && type !== 'xhr') return route.continue()
    const json = (body: unknown) =>
      route.fulfill({ status: 200, contentType: 'application/json', body: JSON.stringify(body) })
    if (url.includes('/api/user.me')) {
      return json({
        user: { id: USER_ID, email: 'sidebar-stub@example.invalid', language: 'en' },
        workspaces: [{ id: WS_ID, name: 'Sidebar Stub WS', settings: { website_url: '', logo_url: '', file_manager: {} } }]
      })
    }
    if (url.includes('/api/workspaces.members')) {
      const rw = { read: true, write: true }
      return json({
        members: [
          {
            user_id: USER_ID,
            email: 'sidebar-stub@example.invalid',
            permissions: {
              contacts: rw, lists: rw, templates: rw, broadcasts: rw, transactional: rw,
              workspace: rw, message_history: rw, blog: rw, automations: rw
            }
          }
        ]
      })
    }
    if (url.includes('/api/veridian/mode')) return json({})
    // Tout le reste echoue proprement (500) : les widgets du tableau de bord gerent l erreur,
    // alors qu un corps vide `{}` les fait planter (« Something went wrong »).
    return route.fulfill({ status: 500, contentType: 'application/json', body: JSON.stringify({ error: 'stub' }) })
  })
  await page.addInitScript(() => localStorage.setItem('auth_token', 'stub-token-sidebar-reachability'))
}

// [largeur, hauteur] en pixels CSS. Les zooms navigateur sont exprimes en viewport CSS equivalent
// (1920x1080 a 125 % = 1536x864, a 150 % = 1280x720 ; 1366x768 a 125 % = 1093x614, a 150 % = 911x512).
const SIZES: Array<[number, number]> = [
  [1920, 1080], [1536, 864], [1440, 900], [1366, 768], [1280, 720], [1280, 600],
  [1024, 768], [820, 1180], [390, 844],
  [1093, 614], [911, 512], [960, 600], [1280, 480], [1024, 400]
]

for (const [width, height] of SIZES) {
  for (const collapsed of [false, true]) {
    // Sous 768 px la sidebar devient un Drawer sans etat replie.
    if (collapsed && width < 768) continue
    test(`sidebar atteignable ${width}x${height} ${collapsed ? 'repliee' : 'depliee'}`, async ({ page }) => {
      await mockConfigJs(page)
      await stubAuthAndWorkspace(page)
      await page.setViewportSize({ width, height })
      await page.goto(`/console/workspace/${WS_ID}`, { waitUntil: 'domcontentloaded' })
      await waitForAppMount(page)

      const mobile = width < 768
      if (mobile) {
        const burger = page.getByTestId('mobile-menu-toggle')
        await expect(burger).toBeVisible({ timeout: 10000 })
        await expect(page.locator('.ant-layout-sider')).toHaveCount(0)
        await burger.click()
      }
      const root = mobile ? '.ant-drawer-content' : '.ant-layout-sider'
      await expect(page.locator(`${root} li.ant-menu-item`).first()).toBeVisible({ timeout: 15000 })

      if (collapsed) {
        await page.locator(`${root} button`).last().click({ force: true })
        await expect(page.locator('.ant-layout-sider-collapsed')).toHaveCount(1)
      }

      // Inventaire de TOUT ce qui est cliquable dans la sidebar.
      const count = await page.evaluate((r) => {
        const el = document.querySelector(r)
        if (!el) return 0
        const nodes = el.querySelectorAll('li.ant-menu-item, button:not(.ant-drawer-close)')
        nodes.forEach((n, i) => n.setAttribute('data-sbt', String(i)))
        return nodes.length
      }, root)
      expect(count, 'la sidebar doit contenir des elements cliquables').toBeGreaterThan(5)

      const failures: string[] = []
      let settingsSeen = false
      let toggleSeen = false
      for (let i = 0; i < count; i++) {
        const loc = page.locator(`${root} [data-sbt="${i}"]`).first()
        // Comme un utilisateur : la nav peut defiler pour amener l'element a l'ecran.
        await loc.scrollIntoViewIfNeeded({ timeout: 3000 }).catch(() => undefined)
        const r = await loc.evaluate((el) => {
          const b = el.getBoundingClientRect()
          const cx = b.left + b.width / 2
          const cy = b.top + b.height / 2
          const inView = cx >= 0 && cy >= 0 && cx <= innerWidth && cy <= innerHeight
          const hit = document.elementFromPoint(cx, cy)
          const label = (el.textContent || el.getAttribute('aria-label') || el.tagName).trim().slice(0, 30)
          return {
            ok: inView && !!hit && (hit === el || el.contains(hit)),
            label,
            href: el.querySelector('a')?.getAttribute('href') || '',
            isToggle: el.tagName === 'BUTTON',
            hit: hit ? `${hit.tagName}.${String((hit as HTMLElement).className).slice(0, 40)}` : 'null'
          }
        })
        if (r.href.endsWith('/settings')) settingsSeen = true
        if (r.isToggle) toggleSeen = true
        if (!r.ok) failures.push(`${r.label || r.href} recouvert par ${r.hit}`)
      }
      expect(failures, `elements de sidebar inatteignables a ${width}x${height}`).toEqual([])
      expect(settingsSeen, 'Settings doit etre dans la sidebar').toBe(true)
      if (!mobile) expect(toggleSeen, 'le bouton de repli doit etre dans la sidebar').toBe(true)

      // Vrai clic sur Settings : doit naviguer.
      const settings = page.locator(`${root} li.ant-menu-item:has(a[href$="/settings"])`).first()
      await settings.scrollIntoViewIfNeeded()
      await settings.click({ timeout: 5000 })
      await expect(page).toHaveURL(/\/settings/, { timeout: 10000 })
    })
  }
}
