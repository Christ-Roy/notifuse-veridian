// domaines-capture.mjs : capture de Réglages > Domaines d'envoi (VER-166, fiche 97) dans un vrai navigateur, ordinateur et téléphone.
// Lancé par lourd --navigateur, le lien de connexion passe en mémoire (variable LIEN, jamais écrite) :
//   LIEN=$(lien SSO) lourd --navigateur --secrets LIEN sh -c 'cd tests/e2e-veridian && npm ci >/dev/null 2>&1 && node domaines-capture.mjs <espace>'
// Sortie : lignes « CAPTURE nom base64 » (JPEG) et une ligne « RESULTAT {...} ». Codes : 0 = page affichée (domaine vérifié ou signature
// coupée) sans texte de l'autre langue, 1 = sinon, 2 = connexion impossible. LANGUE=fr|en.
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
// La console suit la langue du compte : LANGUE=fr (défaut) ou en impose la langue par le sélecteur de l'en-tête.
const LANGUE = process.env.LANGUE === 'en' ? 'en' : 'fr'
await page.waitForLoadState('networkidle', { timeout: 30000 }).catch(() => {})
const actuel = LANGUE === 'fr' ? /^EN$/ : /^FR$/
if (await page.getByRole('button', { name: actuel }).count()) {
  await page.getByRole('button', { name: actuel }).first().click()
  await page.getByText(LANGUE === 'fr' ? 'Français' : 'English', { exact: true }).click()
  await page.waitForTimeout(2000)
  await page.goto(`${base}/console/workspace/${WS}/settings/domains`, { timeout: 90000 })
  await page.waitForLoadState('networkidle', { timeout: 30000 }).catch(() => {})
}
await page.getByText(LANGUE === 'fr' ? "Domaines d'envoi" : 'Sending domains').first().waitFor({ timeout: 30000 }).catch(() => {})
await page.waitForTimeout(2500)
const fr = LANGUE === 'fr'
const verifie = await page.getByText(fr ? 'Vérifié : les mails partent signés' : 'Verified: emails are signed').count()
const complet = await page.getByText(fr ? 'DKIM, SPF et DMARC en place' : 'DKIM, SPF and DMARC in place').count()
const coupee = await page.getByText(fr ? 'Signature coupée' : 'Signing stopped', { exact: true }).count()
const autreLangue = await page.getByText(fr ? 'Signs your emails.' : 'Signe vos mails.', { exact: false }).count()
await shot(`domaines-${LANGUE}-ordinateur`)
await page.setViewportSize({ width: 390, height: 844 })
await page.waitForTimeout(1500)
const deborde = await page.evaluate(() => document.documentElement.scrollWidth > window.innerWidth + 1)
await shot(`domaines-${LANGUE}-telephone`)
console.log('RESULTAT ' + JSON.stringify({ langue: LANGUE, verifie, complet, coupee, texte_autre_langue: autreLangue, deborde_telephone: deborde, erreurs: errors, url: page.url().replace(base, '') }))
await browser.close()
process.exit(verifie + coupee > 0 && autreLangue === 0 ? 0 : 1)
