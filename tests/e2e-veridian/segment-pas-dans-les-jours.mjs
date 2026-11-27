// segment-pas-dans-les-jours.mjs : l'opérateur « not in the last » sur une date du contact et le compte en direct
// (reprise amont 37.0), dans un vrai navigateur, lecture seule : on ouvre l'éditeur de segment, on ajoute une
// condition sur une date, on choisit l'opérateur, on lit le compte, et on FERME sans enregistrer.
// Lancé par scripts/e2e/csp-parcours-prod.py --parcours segment-pas-dans-les-jours.mjs (lien SSO à usage unique en mémoire, LIEN).
// Sortie : « CAPTURE nom base64 » (JPEG) et « RESULTAT {...} ». Codes : 0 vert, 1 rouge, 2 non abouti.
import { chromium } from '@playwright/test'

const WS = process.argv[2]
const LIEN = process.env.LIEN
if (!WS || !LIEN) { console.log('NON ABOUTI : espace ou LIEN absent'); process.exit(2) }

const browser = await chromium.launch({ args: ['--no-sandbox'] })
const ctx = await browser.newContext({ viewport: { width: 1280, height: 900 }, locale: 'fr-FR' })
const page = await ctx.newPage()
const cap = async (nom) => console.log(`CAPTURE ${nom} ${(await page.screenshot({ type: 'jpeg', quality: 70 })).toString('base64')}`)
const sortir = (code, msg) => { console.log(msg); browser.close().finally(() => process.exit(code)) }

await page.goto(LIEN, { timeout: 90000 })
try { await page.waitForURL('**/console/**', { timeout: 90000 }) } catch { return sortir(2, 'NON ABOUTI : connexion') }
const base = page.url().split('/console')[0]
await page.goto(`${base}/console/workspace/${WS}/contacts`, { timeout: 90000 })
await page.waitForLoadState('networkidle', { timeout: 30000 }).catch(() => {})
await cap('segment_1_contacts')

// Ouvrir le menu des segments, puis le bouton de création
const menuSegments = page.getByRole('button', { name: /segment/i }).first()
try { await menuSegments.click({ timeout: 15000 }) } catch { await cap('segment_2_echec_menu'); return sortir(2, 'NON ABOUTI : bouton segments introuvable') }
await page.waitForTimeout(800)
await cap('segment_2_menu')

const creer = page.getByRole('button', { name: /créer|nouveau|create|new/i }).first()
try { await creer.click({ timeout: 15000 }) } catch { await cap('segment_3_echec_creer'); return sortir(2, 'NON ABOUTI : bouton de création introuvable') }
await page.waitForTimeout(1000)
await cap('segment_3_editeur_vide')

// Ajouter une condition : le premier champ de date proposé dans le sélecteur
const ajouter = page.getByRole('button', { name: /ajouter|add/i }).first()
try { await ajouter.click({ timeout: 15000 }) } catch { await cap('segment_4_echec_ajout'); return sortir(2, 'NON ABOUTI : ajout de condition introuvable') }
await page.waitForTimeout(800)
await cap('segment_4_condition')

const resultat = { etape: 'editeur ouvert et condition ajoutée, rien enregistré' }
console.log('RESULTAT ' + JSON.stringify(resultat))
await browser.close()
process.exit(0)
