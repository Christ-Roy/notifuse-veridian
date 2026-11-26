// csp-parcours.mjs : vrai parcours de la console sous sa CSP (VER-265), avec relevé des violations et contrôle positif.
// Lancé par node02 (lourd --navigateur), le lien de connexion passe en mémoire (variable LIEN, jamais écrite) :
//   LIEN=$(lien SSO) lourd --navigateur --secrets LIEN sh -c 'cd tests/e2e-veridian && npm ci >/dev/null 2>&1 && node ../../scripts/e2e/csp-parcours.mjs <espace>'
// Sortie : une ligne JSON « RESULTAT {...} » (pages, violations, contrôle positif) et des lignes « CAPTURE nom base64 »
// (JPEG), que l'appelant décode. Codes : 0 = parcours fait et contrôle positif rapporté (ou bloqué en enforce),
// 1 = violation inattendue ou contrôle positif muet, 2 = connexion impossible.
import { chromium } from '@playwright/test'

const WS = process.argv[2]
const LIEN = process.env.LIEN
if (!WS || !LIEN) { console.log('NON ABOUTI : espace ou LIEN absent'); process.exit(2) }
const PAGES = ['dashboard', 'contacts', 'lists', 'templates', 'broadcasts', 'automations', 'inbox', 'sending-profiles', 'file-manager', 'settings']

const browser = await chromium.launch({ args: ['--no-sandbox'] })
const ctx = await browser.newContext({ viewport: { width: 1440, height: 900 }, locale: 'fr-FR' })
await ctx.addInitScript(() => {
  window.__csp = []
  document.addEventListener('securitypolicyviolation', (e) => {
    window.__csp.push({ d: e.effectiveDirective, b: (e.blockedURI || '').split('?')[0].slice(0, 120), s: (e.sourceFile || '').split('?')[0].slice(-80), disp: e.disposition })
  })
})
const page = await ctx.newPage()
const errors = []
page.on('pageerror', (e) => errors.push(String(e.message).slice(0, 160)))
let header = null
page.on('response', (r) => {
  if (!header && r.request().resourceType() === 'document' && r.url().includes('/console')) {
    const h = r.headers()
    header = h['content-security-policy'] ? 'enforce' : h['content-security-policy-report-only'] ? 'report' : 'absente'
  }
})
const shot = async (nom) => {
  const b = await page.screenshot({ type: 'jpeg', quality: 55 })
  console.log(`CAPTURE ${nom} ${b.toString('base64')}`)
}
const violations = async () => page.evaluate(() => window.__csp.splice(0))

await page.goto(LIEN, { timeout: 90000 })
try { await page.waitForURL('**/console/**', { timeout: 90000 }) } catch { console.log('NON ABOUTI : connexion'); process.exit(2) }
const base = page.url().split('/console')[0]
const res = { csp: null, pages: [], violations: [], erreurs: errors, monaco: false, controle: null }
for (const p of PAGES) {
  await page.goto(`${base}/console/workspace/${WS}/${p}`, { timeout: 90000 })
  await page.waitForLoadState('networkidle', { timeout: 30000 }).catch(() => {})
  await page.waitForTimeout(2500)
  const v = await violations()
  res.pages.push({ page: p, violations: v.length, titre: (await page.title()).slice(0, 60) })
  res.violations.push(...v.map((x) => ({ page: p, ...x })))
  if (['dashboard', 'templates', 'inbox'].includes(p)) await shot(p)
}
// Éditeur de modèle : Monaco se charge depuis cdn.jsdelivr.net (la directive la plus fragile).
await page.goto(`${base}/console/workspace/${WS}/templates`, { timeout: 90000 })
await page.waitForTimeout(3000)
const ligne = page.locator('table tbody tr').first()
if (await ligne.count()) {
  await ligne.locator('button, a').first().click({ timeout: 10000 }).catch(() => ligne.click().catch(() => {}))
  await page.waitForTimeout(6000)
  const code = page.getByText(/code|mjml|html/i).first()
  if (await code.count()) await code.click({ timeout: 5000 }).catch(() => {})
  await page.waitForTimeout(6000)
  res.monaco = (await page.locator('.monaco-editor').count()) > 0
  const v = await violations()
  res.pages.push({ page: 'editeur-modele', violations: v.length, monaco: res.monaco })
  res.violations.push(...v.map((x) => ({ page: 'editeur-modele', ...x })))
  await shot('editeur-modele')
}
// Contrôle positif : un onerror injecté doit être rapporté (Report-Only) ou bloqué (enforce).
await page.goto(`${base}/console/workspace/${WS}/dashboard`, { timeout: 90000 })
await page.waitForTimeout(3000)
await violations()
await page.evaluate(() => { const d = document.createElement('div'); d.innerHTML = '<img src="x-csp-controle:" onerror="window.__pwned = 1">'; document.body.appendChild(d) })
await page.waitForTimeout(3000)
const ctl = await violations()
res.controle = { execute: await page.evaluate(() => window.__pwned === 1), rapporte: ctl.filter((x) => x.d === 'script-src-attr').length }
res.csp = header
await browser.close()
const inattendues = res.violations.filter((x) => x.d !== 'script-src-attr')
console.log('RESULTAT ' + JSON.stringify(res))
const ok = res.controle.rapporte > 0 && (header !== 'enforce' || !res.controle.execute) && inattendues.length === 0
process.exit(ok ? 0 : 1)
