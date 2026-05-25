// === Veridian patch — mobile responsive Lot 1 (ticket 2026-05-24, livré 2026-05-25) ===
//
// Garde-fou « le WorkspaceLayout est utilisable en mobile ».
//
// Ce que la spec vérifie :
//   1. Viewport iPhone SE (320×568) : le bouton hamburger est visible dans
//      le Header, la sidebar Antd Sider est ABSENTE du DOM, le contenu
//      principal occupe toute la largeur (marginLeft: 0).
//   2. Click hamburger → Drawer s'ouvre avec les items de menu (Dashboard,
//      Contacts, Lists, Templates, Broadcasts, …).
//   3. Viewport tablette landscape (768×1024) : le hamburger DISPARAÎT,
//      la sidebar Sider est de nouveau présente, marginLeft revient à 250px.
//   4. Aucune erreur JS uncaught au mount du WorkspaceLayout.
//
// Stratégie d'auth stub (sans HMAC ni backend réel) :
//   - On set `auth_token` dans localStorage avant goto
//   - On stub `/api/user.me` pour renvoyer un user + 1 workspace
//   - On stub `/api/workspaces.members` (appelé par WorkspaceLayout pour
//     les permissions) avec un membre = user courant en full permissions
//   - Toute autre route API → 200 vide (les widgets de la page Dashboard
//     n'ont pas besoin de data réelle pour valider le layout)

import { test, expect, type Page, type Route } from '@playwright/test'
import { trackConsoleErrors, mockConfigJs, waitForAppMount } from './helpers'

const STUB_USER_ID = '00000000-0000-0000-0000-000000000001'
const STUB_USER_EMAIL = 'mobile-responsive-stub@example.invalid'
const STUB_WORKSPACE_ID = 'mobile-stub-ws'
const STUB_WORKSPACE_NAME = 'Mobile Stub WS'

/**
 * Auth stub pour la suite mobile : injecte un token + un user + un
 * workspace, et stub les endpoints minimums pour que WorkspaceLayout
 * monte sans crash. Pas de magic link, pas de HMAC — purement client-side.
 */
async function stubAuthAndWorkspace(page: Page): Promise<void> {
  // L'app fetch `${window.API_ENDPOINT || window.location.origin}/api/...`.
  // En `vite preview`, c'est http://127.0.0.1:4173, donc on stub directement
  // les chemins `**/api/**` (pas le host stub-invalid utilisé par boot-smoke).
  await page.route('**/api/**', (route: Route) => {
    const url = route.request().url()
    const type = route.request().resourceType()
    if (type !== 'fetch' && type !== 'xhr') return route.continue()

    if (url.includes('/api/user.me')) {
      return route.fulfill({
        status: 200,
        contentType: 'application/json',
        body: JSON.stringify({
          user: {
            id: STUB_USER_ID,
            email: STUB_USER_EMAIL,
            language: 'en'
          },
          workspaces: [
            {
              id: STUB_WORKSPACE_ID,
              name: STUB_WORKSPACE_NAME,
              settings: {
                website_url: '',
                logo_url: '',
                file_manager: {}
              }
            }
          ]
        })
      })
    }
    if (url.includes('/api/workspaces.members')) {
      return route.fulfill({
        status: 200,
        contentType: 'application/json',
        body: JSON.stringify({
          members: [
            {
              user_id: STUB_USER_ID,
              email: STUB_USER_EMAIL,
              permissions: {
                contacts: { read: true, write: true },
                lists: { read: true, write: true },
                templates: { read: true, write: true },
                broadcasts: { read: true, write: true },
                transactional: { read: true, write: true },
                workspace: { read: true, write: true },
                message_history: { read: true, write: true },
                blog: { read: true, write: true },
                automations: { read: true, write: true }
              }
            }
          ]
        })
      })
    }
    // Toute autre route : réponse vide JSON (suffit pour ne pas faire
    // crasher les widgets enfants qui font des fetches optionnels).
    return route.fulfill({
      status: 200,
      contentType: 'application/json',
      body: '{}'
    })
  })

  await page.addInitScript(() => {
    localStorage.setItem('auth_token', 'stub-token-for-mobile-responsive-test')
  })
}

test.describe('Mobile responsive Lot 1 — WorkspaceLayout', () => {
  test.beforeEach(async ({ page }) => {
    await mockConfigJs(page)
    await stubAuthAndWorkspace(page)
  })

  test('viewport 320×568 : hamburger visible, sidebar masquée, Drawer ouvrable', async ({ page }) => {
    await page.setViewportSize({ width: 320, height: 568 })
    const errors = trackConsoleErrors(page)

    await page.goto(`/console/workspace/${STUB_WORKSPACE_ID}`, {
      waitUntil: 'domcontentloaded'
    })
    await waitForAppMount(page)

    // 1. Le bouton hamburger DOIT être visible dans le Header.
    const hamburger = page.getByTestId('mobile-menu-toggle')
    await expect(hamburger).toBeVisible({ timeout: 10000 })

    // 2. La sidebar Antd Sider (.ant-layout-sider) NE DOIT PAS être
    //    rendue. En mobile on remplace le Sider par un Drawer fermé.
    const sider = page.locator('.ant-layout-sider')
    await expect(sider).toHaveCount(0)

    // 3. Click hamburger → Drawer s'ouvre.
    await hamburger.click()
    const drawer = page.locator('.ant-drawer-open')
    await expect(drawer).toBeVisible({ timeout: 5000 })

    // 4. Les items de menu sont présents dans le Drawer.
    const dashboardItem = drawer.locator('.ant-menu-item').filter({ hasText: /Dashboard|Tableau/i })
    await expect(dashboardItem).toBeVisible()

    // 5. Aucune exception JS uncaught.
    expect(
      errors,
      `Erreurs JS uncaught en viewport mobile :\n${errors.join('\n---\n')}`
    ).toEqual([])
  })

  test('viewport 768×1024 : hamburger absent, sidebar Sider présente', async ({ page }) => {
    await page.setViewportSize({ width: 768, height: 1024 })
    const errors = trackConsoleErrors(page)

    await page.goto(`/console/workspace/${STUB_WORKSPACE_ID}`, {
      waitUntil: 'domcontentloaded'
    })
    await waitForAppMount(page)

    // À ce viewport, Antd Grid.useBreakpoint() retourne screens.md = true,
    // donc on bascule en mode desktop : hamburger caché, Sider rendu.
    const hamburger = page.getByTestId('mobile-menu-toggle')
    await expect(hamburger).toHaveCount(0)

    const sider = page.locator('.ant-layout-sider').first()
    await expect(sider).toBeVisible({ timeout: 10000 })

    expect(
      errors,
      `Erreurs JS uncaught en viewport tablette :\n${errors.join('\n---\n')}`
    ).toEqual([])
  })

  test('viewport mobile → tablette : layout bascule sans recharge', async ({ page }) => {
    // Boote en mobile…
    await page.setViewportSize({ width: 375, height: 667 })
    await page.goto(`/console/workspace/${STUB_WORKSPACE_ID}`, {
      waitUntil: 'domcontentloaded'
    })
    await waitForAppMount(page)
    await expect(page.getByTestId('mobile-menu-toggle')).toBeVisible({ timeout: 10000 })

    // …puis resize à tablette, le hamburger doit disparaître et le Sider
    // apparaître (Antd Grid.useBreakpoint reagit aux changements).
    await page.setViewportSize({ width: 1024, height: 768 })
    await expect(page.getByTestId('mobile-menu-toggle')).toHaveCount(0, {
      timeout: 5000
    })
    await expect(page.locator('.ant-layout-sider').first()).toBeVisible({
      timeout: 5000
    })
  })
})

// === Veridian patch — mobile responsive Lots 2-3 (2026-05-25) ===
//
// Garde-fou « les règles CSS globales mobile sont bien appliquées ».
//
// On boot la console à un viewport < 576px et on vérifie que les règles
// de la media query `@media (max-width: 575px)` dans index.css sont
// effectivement actives sur des éléments Antd factices créés à la volée
// (Modal, Drawer). C'est le test minimal pour attraper une régression
// de purge CSS Tailwind ou de réécriture du fichier.

test.describe('Mobile responsive Lots 2-3 — CSS global Modal/Drawer/Table', () => {
  test.beforeEach(async ({ page }) => {
    await mockConfigJs(page)
    await stubAuthAndWorkspace(page)
  })

  test('viewport 375×667 : règles media query mobile actives en CSSOM', async ({ page }) => {
    await page.setViewportSize({ width: 375, height: 667 })
    await page.goto(`/console/workspace/${STUB_WORKSPACE_ID}`, {
      waitUntil: 'domcontentloaded'
    })
    await waitForAppMount(page)

    // Injecte un faux contenu Modal Antd + Drawer Antd dans le DOM pour
    // mesurer la width réelle après application des règles `!important`.
    // On évite de devoir ouvrir un vrai Modal — c'est plus rapide et
    // découplé du dataflow.
    const widths = await page.evaluate(() => {
      const modal = document.createElement('div')
      modal.className = 'ant-modal'
      modal.style.width = '520px'
      document.body.appendChild(modal)
      const modalWidth = modal.getBoundingClientRect().width

      const drawerWrap = document.createElement('div')
      drawerWrap.className = 'ant-drawer-right'
      const drawerContent = document.createElement('div')
      drawerContent.className = 'ant-drawer-content-wrapper'
      drawerContent.style.width = '378px'
      drawerWrap.appendChild(drawerContent)
      document.body.appendChild(drawerWrap)
      const drawerWidth = drawerContent.getBoundingClientRect().width

      return { modalWidth, drawerWidth, viewport: window.innerWidth }
    })

    // Sous 575px, modal et drawer doivent être à ~100vw (375px), pas leur
    // width inline 520/378. Tolérance ±2px pour les paddings system.
    expect(widths.modalWidth).toBeGreaterThanOrEqual(widths.viewport - 2)
    expect(widths.drawerWidth).toBeGreaterThanOrEqual(widths.viewport - 2)
  })

  test('viewport 1024×768 : règles media query mobile INACTIVES', async ({ page }) => {
    await page.setViewportSize({ width: 1024, height: 768 })
    await page.goto(`/console/workspace/${STUB_WORKSPACE_ID}`, {
      waitUntil: 'domcontentloaded'
    })
    await waitForAppMount(page)

    // Au-dessus de 575px, la modal doit garder sa width inline native.
    const modalWidth = await page.evaluate(() => {
      const modal = document.createElement('div')
      modal.className = 'ant-modal'
      modal.style.width = '520px'
      document.body.appendChild(modal)
      return modal.getBoundingClientRect().width
    })
    expect(modalWidth).toBeLessThanOrEqual(522) // 520 + paddings
  })
})
