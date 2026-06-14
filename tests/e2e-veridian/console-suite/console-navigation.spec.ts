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
// `contentMarker` : un texte PROPRE au contenu de la page (pas la sidebar,
// qui est identique partout). Sa présence prouve que la page a réellement
// rendu SON écran — pas le defaultNotFoundComponent de TanStack Router, qui
// affiche "Not Found" dans l'Outlet tout en gardant la sidebar complète
// (donc un test qui ne vérifie que `body.length > 50` PASSE sur un "Not
// Found" — c'est précisément le faux négatif qui a laissé filer le bug P0
// Dashboard→Not Found audité par Robert le 2026-06-14).
//
// Les markers sont des sous-chaînes stables, insensibles à la casse, et
// suffisamment spécifiques pour ne pas matcher la sidebar. Si Robert renomme
// un écran, mettre à jour le marker en parallèle.
const WORKSPACE_PAGES: Array<{ slug: string; label: string; contentMarker: RegExp }> = [
  // Dashboard est la racine workspace ($workspaceId/), pas de slug.
  { slug: '', label: 'Dashboard', contentMarker: /Total Contacts|Email Metrics|Transactional Provider/i },
  { slug: 'contacts', label: 'Contacts', contentMarker: /Email|Import|No contacts|contact/i },
  { slug: 'lists', label: 'Lists', contentMarker: /list/i },
  { slug: 'templates', label: 'Templates', contentMarker: /template/i },
  { slug: 'broadcasts', label: 'Broadcasts', contentMarker: /broadcast/i },
  { slug: 'automations', label: 'Automations', contentMarker: /automation|workflow/i },
  { slug: 'transactional-notifications', label: 'Transactional', contentMarker: /transactional|notification/i },
  { slug: 'blog', label: 'Blog', contentMarker: /blog|post|article/i },
  { slug: 'file-manager', label: 'File Manager', contentMarker: /file|folder|upload|storage/i },
  { slug: 'logs', label: 'Logs', contentMarker: /log|message|sent|delivered/i },
  { slug: 'settings', label: 'Settings', contentMarker: /setting|team|member|workspace/i }
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

      // ⚠️ On juge le CONTENU (zone .ant-layout-content = l'Outlet), PAS le
      // body entier : la sidebar contient "Lists", "Templates", "Broadcasts"…
      // donc un contentMarker testé sur le body matcherait toujours via la
      // sidebar, même sur une page "Not Found". TanStack rend "Not Found" DANS
      // l'Outlet (= dans .ant-layout-content) tout en gardant la sidebar — donc
      // c'est bien cette zone qu'il faut inspecter pour attraper le bug P0
      // Dashboard→Not Found audité par Robert le 2026-06-14.
      const mainText = await page.locator('.ant-layout-content').last().innerText()

      // Garde-fou anti faux-négatif : la zone de contenu ne doit JAMAIS dire
      // "Not Found".
      expect(
        mainText,
        `"Not Found" rendu sur ${target.label} — route manquante ou lien sidebar pointant vers une route inexistante. C'est le bug Dashboard→Not Found.`
      ).not.toMatch(/Not Found/i)

      // Validation par RENDU RÉEL (pas comptage DOM) : la zone de contenu doit
      // afficher un texte PROPRE à son écran (hors sidebar).
      expect(
        mainText,
        `Contenu attendu absent sur ${target.label} (marker ${target.contentMarker}). La page a monté la sidebar mais pas son écran ?`
      ).toMatch(target.contentMarker)

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

// ─────────────────────────────────────────────────────────────────────────────
// CLIC RÉEL sur l'item "Dashboard" de la sidebar (≠ navigation par URL directe)
// ─────────────────────────────────────────────────────────────────────────────
//
// Le bloc ci-dessus navigue par `page.goto(url)` : le router résout l'URL
// depuis un cold start. Mais le bug P0 2026-06-14 a été vu en CLIQUANT sur le
// menu "Dashboard" — une navigation CLIENT-SIDE via <Link> de TanStack. Si le
// lien sidebar pointe vers une route qui n'existe pas (ex: /dashboard alors que
// l'écran vit sur l'index $workspaceId/), le clic mène à "Not Found" alors que
// la navigation URL directe vers la bonne route marcherait. Ce test reproduit
// EXACTEMENT le geste de Robert : entrer dans le workspace, puis cliquer le 1er
// item du menu.
test.describe.serial('Console — clic sidebar "Dashboard" (geste réel utilisateur)', () => {
  test('Entrée workspace → clic menu Dashboard → écran Dashboard (pas Not Found)', async ({
    page
  }) => {
    const errors = trackConsoleErrors(page)
    await authenticate(page)

    // On part d'un autre écran pour que le clic Dashboard soit une vraie
    // navigation (pas un no-op si on était déjà sur l'index).
    await page.goto(`${NOTIFUSE_URL}/console/workspace/${tenantId}/contacts`)
    await waitForAppMount(page)
    await page.waitForLoadState('networkidle', { timeout: 20_000 }).catch(() => {})

    // Clic sur le <Link> "Dashboard" de la sidebar — navigation client-side.
    const dashboardLink = page
      .locator('.ant-menu-item a', { hasText: /^Dashboard$/ })
      .first()
    await expect(
      dashboardLink,
      'Lien "Dashboard" introuvable dans la sidebar — item retiré ou label changé ?'
    ).toBeVisible({ timeout: 15_000 })
    await dashboardLink.click()

    // Après clic : on doit être sur l'index workspace et voir le Dashboard.
    await page.waitForLoadState('networkidle', { timeout: 20_000 }).catch(() => {})
    // Zone de contenu (Outlet), pas le body : c'est là que "Not Found" se rend.
    const mainText = await page.locator('.ant-layout-content').last().innerText()

    expect(
      mainText,
      '"Not Found" après clic sur le menu Dashboard — le lien sidebar pointe vers une route inexistante (bug P0 Dashboard→Not Found).'
    ).not.toMatch(/Not Found/i)

    expect(
      mainText,
      'Écran Dashboard non rendu après clic menu — métriques absentes.'
    ).toMatch(/Total Contacts|Email Metrics|Transactional Provider/i)

    expect(
      errors,
      `Erreurs JS après clic menu Dashboard :\n${errors.join('\n---\n')}`
    ).toEqual([])
  })

  // Fail-safe : la route /dashboard (ancien bundle en cache, bookmark, lien
  // externe) doit REDIRIGER vers l'index workspace, jamais afficher "Not Found".
  // Verrouille le patch router 2026-06-14 (WorkspaceDashboardRedirect).
  test('Route /dashboard redirige vers l\'index workspace (pas Not Found)', async ({ page }) => {
    await authenticate(page)
    await page.goto(`${NOTIFUSE_URL}/console/workspace/${tenantId}/dashboard`)
    await waitForAppMount(page)
    await page.waitForLoadState('networkidle', { timeout: 20_000 }).catch(() => {})

    // Le redirect (replace:true) doit avoir ramené sur l'index workspace.
    expect(
      page.url(),
      'La route /dashboard ne redirige pas vers l\'index — fail-safe cassé.'
    ).not.toMatch(/\/dashboard(\?|$)/)

    const mainText = await page.locator('.ant-layout-content').last().innerText()
    expect(
      mainText,
      '"Not Found" sur /dashboard — le redirect fail-safe ne couvre pas cette route.'
    ).not.toMatch(/Not Found/i)
    expect(
      mainText,
      'Dashboard non rendu après redirect /dashboard.'
    ).toMatch(/Total Contacts|Email Metrics|Transactional Provider/i)
  })
})
