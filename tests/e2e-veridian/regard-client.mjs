// regard-client.mjs : regard d'un CLIENT sur la console servie, lecture seule (aucun clic d'écriture).
// Lancé par scripts/e2e/csp-parcours-prod.py --parcours regard-client.mjs --env LARGEUR=1440 (lien SSO en mémoire, LIEN).
//   PAGES=dashboard,inbox,... (défaut : toutes les pages de l'espace). Sortie : « CAPTURE nom base64 » (JPEG, pleine page)
//   et une ligne « RESULTAT {...} » : par page, débordement horizontal, erreurs console, réponses HTTP >= 400,
//   mots anglais ou jargon restés visibles, texte visible (tronqué).
import { chromium } from '@playwright/test'

const WS = process.argv[2]
const LIEN = process.env.LIEN
if (!WS || !LIEN) { console.log('NON ABOUTI : espace ou LIEN absent'); process.exit(2) }
const LARGEUR = Number(process.env.LARGEUR || 1440)
const PAGES = (process.env.PAGES || 'dashboard,inbox,contacts,lists,templates,automations,broadcasts,analytics,sending-profiles,send-queue,logs,file-manager,transactional-notifications,settings').split(',')
const JARGON = /\b(Loading|Save|Cancel|No data|Search|Filter|undefined|NaN|null|\[object|TODO|Lorem|Custom String|Error|Something went wrong|Submit|Delete|Actions|Status|Name|Email)\b/g

const browser = await chromium.launch({ args: ['--no-sandbox'] })
const ctx = await browser.newContext({ viewport: { width: LARGEUR, height: 900 }, deviceScaleFactor: 1, isMobile: LARGEUR < 768, hasTouch: LARGEUR < 768, locale: 'fr-FR' })
const page = await ctx.newPage()
const erreurs = []
let courante = ''
page.on('console', (m) => { if (m.type() === 'error') erreurs.push(`${courante}: ${m.text().slice(0, 160)}`) })
page.on('response', (r) => { if (r.status() >= 400 && r.url().includes('/api/')) erreurs.push(`${courante}: HTTP ${r.status()} ${new URL(r.url()).pathname}`) })
await page.goto(LIEN, { timeout: 90000 })
try { await page.waitForURL('**/console/**', { timeout: 90000 }) } catch { console.log('NON ABOUTI : connexion'); process.exit(2) }
const base = page.url().split('/console')[0]
const res = { largeur: LARGEUR, pages: [] }
for (const p of PAGES) {
  courante = p
  erreurs.length = 0
  await page.goto(`${base}/console/workspace/${WS}/${p}`, { timeout: 90000 })
  await page.waitForLoadState('networkidle', { timeout: 30000 }).catch(() => {})
  await page.waitForTimeout(2500)
  const m = await page.evaluate(() => ({ doc: document.documentElement.scrollWidth, ecran: window.innerWidth }))
  const txt = await page.evaluate(() => (document.querySelector('main') || document.body).innerText || '')
  const mots = [...new Set((txt.match(JARGON) || []))]
  res.pages.push({ page: p, deborde_px: Math.max(0, m.doc - m.ecran), texte: txt.replace(/\s+/g, ' ').slice(0, 900), mots_suspects: mots, erreurs: [...erreurs].slice(0, 6) })
  const b = await page.screenshot({ type: 'jpeg', quality: 70, fullPage: true })
  console.log(`CAPTURE ${p}_${LARGEUR} ${b.toString('base64')}`)
}
await browser.close()
console.log('RESULTAT ' + JSON.stringify(res))
