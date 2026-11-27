// modeles-recherche.mjs : recherche des modèles par nom sur la liste (reprise amont 35.0), dans un vrai navigateur, lecture seule.
// Lancé par scripts/e2e/csp-parcours-prod.py --parcours modeles-recherche.mjs (lien SSO à usage unique en mémoire, LIEN).
// Tape une partie du nom du premier modèle dans la recherche, vérifie que chaque ligne affichée la contient,
// puis un terme introuvable doit afficher l'état vide. Aucune écriture.
// Sortie : « CAPTURE nom base64 » (JPEG) et « RESULTAT {...} ». Codes : 0 vert, 1 rouge, 2 non abouti.
import { chromium } from '@playwright/test'

const WS = process.argv[2]
const LIEN = process.env.LIEN
if (!WS || !LIEN) { console.log('NON ABOUTI : espace ou LIEN absent'); process.exit(2) }

const browser = await chromium.launch({ args: ['--no-sandbox'] })
const ctx = await browser.newContext({ viewport: { width: 1280, height: 900 }, deviceScaleFactor: 1, locale: 'fr-FR' })
const page = await ctx.newPage()
const cap = async (nom) => console.log(`CAPTURE ${nom} ${(await page.screenshot({ type: 'jpeg', quality: 70 })).toString('base64')}`)

await page.goto(LIEN, { timeout: 90000 })
try { await page.waitForURL('**/console/**', { timeout: 90000 }) } catch { console.log('NON ABOUTI : connexion'); process.exit(2) }
const base = page.url().split('/console')[0]
await page.goto(`${base}/console/workspace/${WS}/templates`, { timeout: 90000 })
await page.waitForLoadState('networkidle', { timeout: 30000 }).catch(() => {})
const boite = page.getByPlaceholder(/Rechercher par nom|Search by name/)
try { await boite.waitFor({ timeout: 30000 }) } catch { await cap('modeles_sans_recherche'); console.log('NON ABOUTI : champ de recherche absent (liste vide ou version non déployée)'); await browser.close(); process.exit(2) }

const lignes = () => page.locator('.ant-table-tbody tr.ant-table-row').allInnerTexts()
const avant = await lignes()
if (avant.length === 0) { console.log('NON ABOUTI : aucun modèle à chercher'); await browser.close(); process.exit(2) }
await cap('modeles_avant_recherche')

// Terme : les 4 premiers caractères du premier nom affiché (le nom complet est dans la première cellule)
const premier = (avant[0].split('\n').find((l) => l.trim().length >= 4) || '').trim()
const terme = premier.slice(0, 4)
await boite.fill(terme)
await page.waitForTimeout(800)
const apres = await lignes()
const tous = apres.every((l) => l.toLowerCase().includes(terme.toLowerCase()))
await cap('modeles_recherche_' + terme.replace(/\W/g, '_'))

await boite.fill('zzqxw-introuvable')
await page.waitForTimeout(800)
const vide = await page.getByText(/Aucun modèle ne correspond|No templates match your search/).count()
await cap('modeles_recherche_vide')

const res = { terme, avant: avant.length, apres: apres.length, tous_contiennent_terme: tous, etat_vide: vide > 0 }
await browser.close()
console.log('RESULTAT ' + JSON.stringify(res))
process.exit(tous && apres.length > 0 && apres.length <= avant.length && vide > 0 ? 0 : 1)
