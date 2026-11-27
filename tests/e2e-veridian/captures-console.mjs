// captures-console.mjs : captures de la console servie, dans un vrai navigateur, à une largeur donnée (lecture seule).
// Lancé par scripts/e2e/csp-parcours-prod.py --parcours captures-console.mjs (lien SSO à usage unique en mémoire, LIEN).
//   LARGEUR=390 (téléphone, défaut) ; PAGES=dashboard,inbox,... (défaut : les écrans principaux).
// Sortie : « CAPTURE nom base64 » (JPEG) et une ligne « RESULTAT {...} » : par page, débordement horizontal mesuré
// (document plus large que l'écran = défilement latéral, le défaut mobile le plus courant).
import { chromium } from '@playwright/test'

const WS = process.argv[2]
const LIEN = process.env.LIEN
if (!WS || !LIEN) { console.log('NON ABOUTI : espace ou LIEN absent'); process.exit(2) }
const LARGEUR = Number(process.env.LARGEUR || 390)
const PAGES = (process.env.PAGES || 'dashboard,inbox,contacts,lists,templates,automations,sending-profiles,settings').split(',')

const browser = await chromium.launch({ args: ['--no-sandbox'] })
const ctx = await browser.newContext({ viewport: { width: LARGEUR, height: 844 }, deviceScaleFactor: 2, isMobile: LARGEUR < 768, hasTouch: LARGEUR < 768, locale: 'fr-FR' })
const page = await ctx.newPage()
await page.goto(LIEN, { timeout: 90000 })
try { await page.waitForURL('**/console/**', { timeout: 90000 }) } catch { console.log('NON ABOUTI : connexion'); process.exit(2) }
const base = page.url().split('/console')[0]
const res = { largeur: LARGEUR, pages: [] }
for (const p of PAGES) {
  await page.goto(`${base}/console/workspace/${WS}/${p}`, { timeout: 90000 })
  await page.waitForLoadState('networkidle', { timeout: 30000 }).catch(() => {})
  await page.waitForTimeout(2500)
  const m = await page.evaluate(() => ({ doc: document.documentElement.scrollWidth, ecran: window.innerWidth }))
  res.pages.push({ page: p, deborde_px: Math.max(0, m.doc - m.ecran) })
  const b = await page.screenshot({ type: 'jpeg', quality: 60 })
  console.log(`CAPTURE ${p}_${LARGEUR} ${b.toString('base64')}`)
}
await browser.close()
console.log('RESULTAT ' + JSON.stringify(res))
