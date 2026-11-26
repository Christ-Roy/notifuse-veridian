// domaines-capture.mjs : capture de Réglages > Domaines d'envoi (VER-166, fiche 97) dans un vrai navigateur, ordinateur et téléphone.
// Lancé par lourd --navigateur, le lien de connexion passe en mémoire (variable LIEN, jamais écrite) :
//   LIEN=$(lien SSO) lourd --navigateur --secrets LIEN sh -c 'cd tests/e2e-veridian && npm ci >/dev/null 2>&1 && node domaines-capture.mjs <espace>'
// Sortie : lignes « CAPTURE nom base64 » (JPEG) et une ligne « RESULTAT {...} ». Codes : 0 = page affichée avec un domaine vérifié,
// 1 = page sans domaine vérifié, 2 = connexion impossible.
import { chromium } from '@playwright/test'

const WS = process.argv[2]
const LIEN = process.env.LIEN
if (!WS || !LIEN) { console.log('NON ABOUTI : espace ou LIEN absent'); process.exit(2) }
const browser = await chromium.launch({ args: ['--no-sandbox'] })
const ctx = await browser.newContext({ viewport: { width: 1440, height: 1000 }, locale: 'fr-FR' })
const page = await ctx.newPage()
const errors = []
page.on('pageerror', (e) => errors.push(String(e.message).slice(0, 160)))
const shot = async (nom, full = true) => {
  const b = await page.screenshot({ type: 'jpeg', quality: 70, fullPage: full })
  console.log(`CAPTURE ${nom} ${b.toString('base64')}`)
}
await page.goto(LIEN, { timeout: 90000 })
try { await page.waitForURL('**/console/**', { timeout: 90000 }) } catch { console.log('NON ABOUTI : connexion'); process.exit(2) }
const base = page.url().split('/console')[0]
await page.goto(`${base}/console/workspace/${WS}/settings/domains`, { timeout: 90000 })
// La console suit la langue du compte : on passe en français si elle ne l'est pas (sélecteur « EN » de l'en-tête).
await page.waitForLoadState('networkidle', { timeout: 30000 }).catch(() => {})
if (await page.getByRole('button', { name: /^EN$/ }).count()) {
  await page.getByRole('button', { name: /^EN$/ }).first().click()
  await page.getByText('Français', { exact: true }).click()
  await page.waitForTimeout(2000)
  await page.goto(`${base}/console/workspace/${WS}/settings/domains`, { timeout: 90000 })
}
await page.waitForLoadState('networkidle', { timeout: 30000 }).catch(() => {})
await page.getByText("Domaines d'envoi").first().waitFor({ timeout: 30000 }).catch(() => {})
await page.waitForTimeout(2500)
const verifie = await page.getByText('Vérifié : les mails partent signés').count()
const complet = await page.getByText('DKIM, SPF et DMARC en place').count()
await shot('domaines-ordinateur')
await page.setViewportSize({ width: 390, height: 844 })
await page.waitForTimeout(1500)
const deborde = await page.evaluate(() => document.documentElement.scrollWidth > window.innerWidth + 1)
await shot('domaines-telephone')
console.log('RESULTAT ' + JSON.stringify({ verifie, complet, deborde_telephone: deborde, erreurs: errors, url: page.url().replace(base, '') }))
await browser.close()
process.exit(verifie > 0 ? 0 : 1)
