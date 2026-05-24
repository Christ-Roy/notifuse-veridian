// === Veridian patch — console-suite : navigation workspace sidebar ===
//
// Couverture (§3 du ticket) :
//   - Auto-login → atterrissage workspace
//   - Click sur chaque entrée sidebar (Dashboard, Contacts, Lists,
//     Templates, Broadcasts, Automations, Transactional, Blog,
//     File Manager, Logs, Settings)
//   - Pour chaque écran : URL change correctement, page monte, pas
//     d'erreur console, pas de hash Lingui visible
//
// Sentinelle perf : la navigation entre écrans déclenche le download de
// chunks lazy (cf. patch perf-ui-baseline 2026-05-22). Si un chunk casse,
// React.lazy() throw inside Suspense → uncaught → trackConsoleErrors le voit.
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

// Pages à visiter — chemins relatifs au workspace. Le label affiché dans
// la sidebar est repris pour le diagnostic en cas d'échec ; on navigue
// par URL directe (plus stable que click sur Menu antd avec icônes).
//
// Liste alignée sur layouts/WorkspaceLayout.tsx menuItems. Si Robert
// ajoute/retire un item du sidebar, mettre à jour ici en parallèle.
const WORKSPACE_PAGES: Array<{ slug: string; label: string }> = [
  // Dashboard est la racine workspace ($workspaceId/), pas de slug.
  { slug: '', label: 'Dashboard' },
  { slug: 'contacts', label: 'Contacts' },
  { slug: 'lists', label: 'Lists' },
  { slug: 'templates', label: 'Templates' },
  { slug: 'broadcasts', label: 'Broadcasts' },
  { slug: 'automations', label: 'Automations' },
  { slug: 'transactional-notifications', label: 'Transactional' },
  { slug: 'blog', label: 'Blog' },
  { slug: 'file-manager', label: 'File Manager' },
  { slug: 'logs', label: 'Logs' },
  { slug: 'settings', label: 'Settings' }
]

async function authenticate(page: Page): Promise<void> {
  await loginViaAutoLogin(page, provisioning.auto_login_url)
  await waitForAppMount(page)
  // Attend que workspaces.list ait répondu et que le frontend ait routé
  // vers /console/workspace/{id} ou affiché un sélecteur.
  await page.waitForTimeout(3000)
}

test.describe.serial('Console — navigation workspace sidebar', () => {
  for (const target of WORKSPACE_PAGES) {
    test(`Navigation → ${target.label} (${target.slug || '/'}) monte sans erreur`, async ({ page }) => {
      const errors = trackConsoleErrors(page)

      await authenticate(page)

      const workspaceUrl = `${NOTIFUSE_URL}/console/workspace/${tenantId}${target.slug ? '/' + target.slug : ''}`
      await page.goto(workspaceUrl)
      await waitForAppMount(page)

      // Attend que le chunk lazy de la page soit téléchargé et que le
      // contenu apparaisse. networkidle peut être bruyant (poll) — on
      // tolère un timeout, ce qui compte c'est le contenu visible.
      await page.waitForLoadState('networkidle', { timeout: 20_000 }).catch(() => {})

      // L'URL doit refléter notre navigation (TanStack Router redirige pas
      // discrètement vers une autre route faute d'auth).
      expect(
        page.url(),
        `URL inattendue après navigation vers ${target.label}`
      ).toContain(`/console/workspace/${tenantId}`)

      // Body non vide = la page a rendu quelque chose (Spinner, Empty, ou
      // contenu). Un body vide = lazy chunk a planté ou Suspense fallback
      // jamais résolu.
      const bodyText = await page.locator('body').innerText()
      expect(
        bodyText.length,
        `Body vide sur ${target.label} — chunk lazy planté ou Suspense bloqué ?`
      ).toBeGreaterThan(50)

      const hashes = await findVisibleLinguiHashes(page)
      expect(
        hashes,
        `Hash Lingui sur ${target.label} : ${hashes.join(', ')}`
      ).toEqual([])

      expect(
        errors,
        `Erreurs JS sur ${target.label} :\n${errors.join('\n---\n')}`
      ).toEqual([])
    })
  }
})
