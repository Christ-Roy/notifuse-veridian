// csp-parcours.mjs : vrai parcours de la console sous sa CSP (VER-265), avec relevé des violations et contrôle positif.
// Lancé par node02 (lourd --navigateur), le lien de connexion passe en mémoire (variable LIEN, jamais écrite) :
//   LIEN=$(lien SSO) lourd --navigateur --secrets LIEN sh -c 'cd tests/e2e-veridian && npm ci >/dev/null 2>&1 && node csp-parcours.mjs <espace>'
// Sortie : une ligne JSON « RESULTAT {...} » (pages, violations, contrôle positif) et des lignes « CAPTURE nom base64 »
// (JPEG), que l'appelant décode. Codes : 0 = parcours fait et contrôle positif rapporté (ou bloqué en enforce),
// 1 = violation inattendue ou contrôle positif muet, 2 = connexion impossible.
import { chromium, firefox } from '@playwright/test'

const WS = process.argv[2]
const LIEN = process.env.LIEN
if (!WS || !LIEN) { console.log('NON ABOUTI : espace ou LIEN absent'); process.exit(2) }
const PAGES = ['dashboard', 'contacts', 'lists', 'templates', 'broadcasts', 'automations', 'inbox', 'sending-profiles', 'file-manager', 'settings']

// NAVIGATEUR=firefox : Firefox envoie ses rapports par report-uri (requête visible) ; Chrome par l'API Reporting (hors page).
const browser = process.env.NAVIGATEUR === 'firefox' ? await firefox.launch() : await chromium.launch({ args: ['--no-sandbox'] })
const ctx = await browser.newContext({ viewport: { width: 1440, height: 900 }, locale: 'fr-FR' })
// Relevé au niveau du CONTEXTE (fonction exposée) : il survit aux navigations. Le 26/11, un relevé par page (window.__csp)
// perdait les violations de la page d'atterrissage /veridian/auto-login, et la connexion a été coupée en enforce.
const toutes = []
await ctx.exposeFunction('__cspRapport', (v) => { toutes.push(v) })
await ctx.addInitScript(() => {
  document.addEventListener('securitypolicyviolation', (e) => {
    window.__cspRapport({ d: e.effectiveDirective, b: (e.blockedURI || '').split('?')[0].slice(0, 120), s: (e.sourceFile || '').split('?')[0].slice(-80), disp: e.disposition, doc: location.pathname })
  })
})
const page = await ctx.newPage()
// Envoi des rapports par le navigateur jusqu'au serveur (report-uri) : chaque requête vers le point de rapport et son statut.
const envois = []
ctx.on('request', (q) => { if (q.url().includes('/api/veridian/csp-report')) envois.push({ type: q.resourceType(), ct: q.headers()['content-type'] || '' }) })
ctx.on('requestfinished', async (q) => { if (q.url().includes('/api/veridian/csp-report')) { const r = await q.response(); envois.push({ statut: r ? r.status() : null }) } })
ctx.on('requestfailed', (q) => { if (q.url().includes('/api/veridian/csp-report')) envois.push({ echec: q.failure()?.errorText }) })
// VER-265 : d'où vient Monaco ? La console le sert elle-même ; une seule requête vers un CDN public est un échec.
const monacoSource = { local: 0, cdn: 0 }
ctx.on('request', (q) => {
  const u = q.url()
  if (u.includes('/console/monaco/vs/')) monacoSource.local++
  if (u.includes('cdn.jsdelivr.net') || u.includes('unpkg.com')) monacoSource.cdn++
})
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
const violations = async () => { await page.waitForTimeout(300); return toutes.splice(0) }

await page.goto(LIEN, { timeout: 90000 })
try { await page.waitForURL('**/console/**', { timeout: 90000 }) } catch { console.log('NON ABOUTI : connexion'); process.exit(2) }
const base = page.url().split('/console')[0]
const res = { csp: null, pages: [], violations: [], erreurs: errors, monaco: false, controle: null }
const atterrissage = await violations() // /veridian/auto-login puis le premier écran de la console
res.pages.push({ page: 'connexion', violations: atterrissage.length })
res.violations.push(...atterrissage.map((x) => ({ page: 'connexion', ...x })))
for (const p of PAGES) {
  await page.goto(`${base}/console/workspace/${WS}/${p}`, { timeout: 90000 })
  await page.waitForLoadState('networkidle', { timeout: 30000 }).catch(() => {})
  await page.waitForTimeout(2500)
  const v = await violations()
  res.pages.push({ page: p, violations: v.length, titre: (await page.title()).slice(0, 60) })
  res.violations.push(...v.map((x) => ({ page: p, ...x })))
  if (['dashboard', 'templates', 'inbox'].includes(p)) await shot(p)
}
// Éditeur de modèle : Monaco, servi par la console depuis /console/monaco/vs depuis VER-265 (27/11), plus par jsdelivr.
// 27/11 : un gabarit en texte brut n'a pas de Monaco (zone de saisie simple). On essaie les premiers gabarits, chacun
// refermé par « Annuler » (rien n'est enregistré), jusqu'à ce qu'un éditeur Monaco soit monté.
for (let i = 0; i < 6 && !res.monaco; i++) {
  await page.goto(`${base}/console/workspace/${WS}/templates`, { timeout: 90000 })
  await page.waitForTimeout(3000)
  const ligne = page.locator('table tbody tr').nth(i)
  if (!(await ligne.count())) break
  await ligne.locator('button, a').first().click({ timeout: 10000 }).catch(() => ligne.click().catch(() => {}))
  await page.waitForTimeout(6000)
  // Étape 2 (le constructeur), puis l'aperçu et son onglet MJML : c'est là que Monaco se charge.
  const etape2 = page.getByText(/^\s*2\.\s*(Template|Modèle)\s*$/).first()
  if (await etape2.count()) await etape2.click({ timeout: 5000 }).catch(() => {})
  else await page.getByRole('button', { name: /^(Next|Suivant)$/ }).first().click({ timeout: 5000 }).catch(() => {})
  await page.waitForTimeout(6000)
  await page.getByText(/^(Preview|Aperçu)$/).first().click({ timeout: 5000 }).catch(() => {}) // sélecteur segmenté Edit / Preview
  await page.waitForTimeout(3000)
  await page.getByRole('tab', { name: /^MJML$/ }).first().click({ timeout: 5000 }).catch(() => {})
  await page.waitForTimeout(3000)
  // Données de test : l'éditeur JSON est un Monaco (Preview.tsx), chargé à la demande.
  await page.getByRole('tab', { name: /Template Data|Données/ }).first().click({ timeout: 5000 }).catch(() => {})
  await page.waitForTimeout(2000)
  await page.getByRole('button', { name: /Edit Test Data|Modifier les données/ }).first().click({ timeout: 5000 }).catch(() => {})
  await page.waitForTimeout(10000)
  res.monaco = (await page.locator('.monaco-editor').count()) > 0
  const v = await violations()
  res.pages.push({ page: `editeur-modele-${i + 1}`, violations: v.length, monaco: res.monaco })
  res.violations.push(...v.map((x) => ({ page: `editeur-modele-${i + 1}`, ...x })))
  if (res.monaco) await shot('editeur-modele')
  await page.keyboard.press('Escape').catch(() => {})
  await page.getByRole('button', { name: /^(Cancel|Annuler)$/ }).first().click({ timeout: 3000 }).catch(() => {})
}
// Aucun gabarit existant n'a monté Monaco (tous en texte brut, cas de l'espace Veridian le 27/11) : nouveau gabarit en
// mode « Code (MJML) », étape 2 (l'éditeur MJML est un Monaco), puis « Annuler ». Rien n'est enregistré.
if (!res.monaco) {
  await page.goto(`${base}/console/workspace/${WS}/templates`, { timeout: 90000 })
  await page.waitForTimeout(3000)
  await page.getByRole('button', { name: /^(Create Template|Créer un modèle)$/ }).first().click({ timeout: 10000 }).catch(() => {})
  await page.waitForTimeout(3000)
  await page.getByLabel(/^(Template name|Nom du modèle)$/).first().fill('csp-parcours-sans-enregistrement').catch(() => {})
  await page.getByLabel(/^(Email subject|Objet de l'email)/).first().fill('CSP parcours').catch(() => {})
  await page.getByText(/^Code \(MJML\)$/).first().click({ timeout: 5000 }).catch(() => {})
  await page.getByRole('button', { name: /^(Next|Suivant)$/ }).first().click({ timeout: 5000 }).catch(() => {})
  await page.waitForTimeout(12000)
  res.monaco = (await page.locator('.monaco-editor').count()) > 0
  const v = await violations()
  res.pages.push({ page: 'nouveau-modele-mjml', violations: v.length, monaco: res.monaco })
  res.violations.push(...v.map((x) => ({ page: 'nouveau-modele-mjml', ...x })))
  await shot('editeur-modele')
  await page.getByRole('button', { name: /^(Cancel|Annuler)$/ }).first().click({ timeout: 3000 }).catch(() => {})
  await page.waitForTimeout(1500)
  // Une confirmation de fermeture sans enregistrer peut s'ouvrir : on la valide, toujours sans enregistrer.
  await page.getByRole('button', { name: /^(OK|Oui|Yes|Discard|Abandonner|Quitter)$/ }).first().click({ timeout: 2000 }).catch(() => {})
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
// Chrome envoie ses rapports (report-to) par lots, en différé : fermer tout de suite les perdait (26/11 : aucun rapport
// navigateur n'atteignait le journal). ATTENTE_RAPPORTS secondes avant fermeture, pour éprouver la chaîne jusqu'au serveur.
const attente = Number(process.env.ATTENTE_RAPPORTS || 0)
if (attente > 0) await page.waitForTimeout(attente * 1000)
await browser.close()
const inattendues = res.violations.filter((x) => x.d !== 'script-src-attr')
res.envois_rapports = envois.slice(0, 20)
res.monaco_source = monacoSource
console.log('RESULTAT ' + JSON.stringify(res))
// EXIGER_MONACO=1 (preuve VER-265) : un parcours qui n'a pas ouvert Monaco n'a rien prouvé sur sa provenance.
const monacoLocal = monacoSource.cdn === 0 && (!res.monaco || monacoSource.local > 0) && (process.env.EXIGER_MONACO !== '1' || res.monaco)
const ok = res.controle.rapporte > 0 && (header !== 'enforce' || !res.controle.execute) && inattendues.length === 0 && monacoLocal
process.exit(ok ? 0 : 1)
