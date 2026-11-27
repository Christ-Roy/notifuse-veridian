// voir-contact.mjs : preuve du bouton « Voir le contact » de la boîte, dans un vrai navigateur, sur la console servie
// (lecture seule : ouvre des messages et la fiche, ne modifie rien). Lancé par
//   scripts/e2e/csp-parcours-prod.py <espace> --parcours voir-contact.mjs [--env LARGEUR=1440]
// Sortie : « CAPTURE nom base64 » (JPEG : le message, puis la fiche ouverte) et « RESULTAT {...} ».
// Code : 0 = la fiche s'ouvre avec l'adresse du contact, 1 = bouton absent ou fiche vide, 2 = non abouti.
import { chromium } from '@playwright/test'

const WS = process.argv[2]
const LIEN = process.env.LIEN
if (!WS || !LIEN) { console.log('NON ABOUTI : espace ou LIEN absent'); process.exit(2) }
const LARGEUR = Number(process.env.LARGEUR || 1440)

const browser = await chromium.launch({ args: ['--no-sandbox'] })
const ctx = await browser.newContext({ viewport: { width: LARGEUR, height: 900 }, deviceScaleFactor: 1, locale: 'fr-FR' })
const page = await ctx.newPage()
await page.goto(LIEN, { timeout: 90000 })
try { await page.waitForURL('**/console/**', { timeout: 90000 }) } catch { console.log('NON ABOUTI : connexion'); process.exit(2) }
const base = page.url().split('/console')[0]
await page.goto(`${base}/console/workspace/${WS}/inbox`, { timeout: 90000 })
try { await page.getByTestId('mail-row').first().waitFor({ timeout: 45000 }) } catch { console.log('NON ABOUTI : aucun message dans la boîte'); process.exit(2) }

const res = { largeur: LARGEUR, messages_ouverts: 0, bouton: false, fiche: false }
const lignes = await page.getByTestId('mail-row').count()
for (let i = 0; i < Math.min(lignes, 10) && !res.bouton; i++) {
  await page.getByTestId('mail-row').nth(i).click()
  await page.getByTestId('message-view').waitFor({ timeout: 20000 }).catch(() => {})
  await page.waitForTimeout(1200)
  res.messages_ouverts++
  res.bouton = await page.getByTestId('mail-contact-button').isVisible().catch(() => false)
}
if (!res.bouton) { console.log('RESULTAT ' + JSON.stringify(res)); await browser.close(); process.exit(1) }
const b1 = await page.screenshot({ type: 'jpeg', quality: 70 })
console.log(`CAPTURE message_${LARGEUR} ${b1.toString('base64')}`)
await page.getByTestId('mail-contact-button').getByRole('button').click()
const tiroir = page.locator('.ant-drawer-open .ant-drawer-content')
await tiroir.waitFor({ timeout: 20000 }).catch(() => {})
await page.waitForLoadState('networkidle', { timeout: 20000 }).catch(() => {})
await page.waitForTimeout(2000)
const texte = (await tiroir.innerText().catch(() => '')) || ''
res.fiche = /@/.test(texte)
res.fiche_caracteres = texte.length
const b2 = await page.screenshot({ type: 'jpeg', quality: 70 })
console.log(`CAPTURE fiche_${LARGEUR} ${b2.toString('base64')}`)
await browser.close()
console.log('RESULTAT ' + JSON.stringify(res))
process.exit(res.fiche ? 0 : 1)
