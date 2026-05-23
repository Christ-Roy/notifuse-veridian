// === Veridian patch — helpers suite E2E production-build smoke ===
//
// Helpers partagés entre les specs prod-smoke. Pas de fixtures lourdes ici
// (ce n'est pas le rôle de cette suite — voir e2e/fixtures/auth.ts pour les
// parcours métier authentifiés sur dev server).
//
// La seule chose qu'on mocke, c'est `/config.js` : sans ça, le build de
// prod essaie de joindre l'API backend réelle, et on veut tester la
// MONTÉE de l'app, pas le backend.

import type { Page, Route, ConsoleMessage } from '@playwright/test'

/**
 * Erreurs console attendues / bénignes qui ne doivent PAS faire échouer
 * le test. On reste **strict** : on n'ajoute ici que les patterns dont on
 * est certain qu'ils ne signalent pas un boot cassé.
 */
const BENIGN_CONSOLE_ERROR_PATTERNS = [
  /favicon/i,
  /net::ERR_/i,
  /Failed to load resource/i,
  // L'app peut tenter de joindre l'API ; un 401/404 sur un endpoint
  // legitimate (user.me, workspaces.list…) ne casse pas le boot.
  /\b(401|404)\b/,
  // React/Antd peuvent logguer un strict-mode warning en dev — pas en
  // prod, mais on garde la garde anti-faux-positif.
  /strict mode/i
]

/**
 * Installe un listener sur les erreurs console et page errors d'une page.
 * Retourne un tableau qui se remplit en temps réel.
 *
 * IMPORTANT : on capte AUSSI `pageerror` (uncaught exceptions JS), pas
 * juste les `console.error`. C'est `pageerror` qui aurait remonté le
 * `Cannot read properties of undefined (reading 'createContext')` de
 * l'incident #1.
 */
export function trackConsoleErrors(page: Page): string[] {
  const errors: string[] = []

  page.on('console', (msg: ConsoleMessage) => {
    if (msg.type() !== 'error') return
    const text = msg.text()
    if (BENIGN_CONSOLE_ERROR_PATTERNS.some((rx) => rx.test(text))) return
    errors.push(`[console.error] ${text}`)
  })

  page.on('pageerror', (err: Error) => {
    errors.push(`[pageerror] ${err.message}\n${err.stack ?? ''}`)
  })

  return errors
}

/**
 * Mocke `/config.js` — fichier runtime servi par le backend Go en prod
 * qui injecte `window.API_URL`. Sans ce mock, le build essaie de fetch
 * `/config.js` sur le serveur preview qui répond 404 → l'app ne sait pas
 * où taper. On simule un backend installé sur un host inexistant — toutes
 * les requêtes API seront stub par defaultApiStub() ci-dessous.
 */
export async function mockConfigJs(page: Page): Promise<void> {
  await page.route('**/config.js**', (route: Route) =>
    route.fulfill({
      status: 200,
      contentType: 'application/javascript',
      body: [
        'window.API_URL = "https://prod-smoke-stub.invalid";',
        'window.ROOT_EMAIL = "test@example.com";',
        'window.IS_INSTALLED = true;'
      ].join('\n')
    })
  )
}

/**
 * Stub par défaut pour toute requête vers le backend (host invalide).
 * Renvoie 401 pour user.me (pas loggé), 200 vide pour le reste. Suffit
 * pour faire monter la page de signin sans qu'elle tente de joindre un
 * vrai serveur.
 */
export async function defaultApiStub(page: Page): Promise<void> {
  await page.route('https://prod-smoke-stub.invalid/**', (route: Route) => {
    const url = route.request().url()
    const type = route.request().resourceType()
    if (type !== 'fetch' && type !== 'xhr') return route.continue()

    if (url.includes('/api/user.me')) {
      return route.fulfill({
        status: 401,
        contentType: 'application/json',
        body: JSON.stringify({ error: 'Unauthorized' })
      })
    }
    return route.fulfill({
      status: 200,
      contentType: 'application/json',
      body: '{}'
    })
  })
}

/**
 * Attend que la SPA ait monté — le splash HTML est remplacé par le tree
 * React. On considère que le boot est OK quand `.preload-splash` a
 * disparu OU quand `#root` contient un élément React rendu (autre que le
 * splash).
 */
export async function waitForAppMount(page: Page, timeout = 20000): Promise<void> {
  // Le splash est inline dans index.html ; React remplace #root entier
  // au mount. On attend que le splash disparaisse.
  await page.waitForFunction(
    () => {
      const splash = document.querySelector('.preload-splash')
      // splash disparu OU déplacé/écrasé par React = monté
      if (!splash) return true
      // si splash existe encore mais #root a d'autres enfants, c'est OK
      const root = document.getElementById('root')
      return !!root && root.children.length > 0 && !root.contains(splash)
    },
    { timeout }
  )
}

/**
 * Regex qui matche un msgId Lingui non-traduit affiché en prod.
 * Lingui génère des hash courts (~6-8 chars) en base64-url-safe. Si le
 * texte visible matche cette regex, c'est que `lingui extract` n'a pas
 * été lancé avant le build → catalogue compilé incomplet → hash brut
 * affiché. C'était l'incident #2 (« GdgCoi », « 8wOKeG »).
 */
export const LINGUI_HASH_REGEX = /^[A-Za-z0-9_+/=]{6,8}$/

/**
 * Heuristique : un texte est-il un hash Lingui suspect ?
 * - Longueur 6-8
 * - Alphanumérique pur
 * - Au moins 2 majuscules ET 2 minuscules (les mots français/anglais
 *   communs n'ont jamais ce mix)
 */
export function looksLikeLinguiHash(text: string): boolean {
  const trimmed = text.trim()
  if (!LINGUI_HASH_REGEX.test(trimmed)) return false
  const uppers = (trimmed.match(/[A-Z]/g) || []).length
  const lowers = (trimmed.match(/[a-z]/g) || []).length
  if (uppers < 2 || lowers < 2) return false
  return true
}

/**
 * Récupère tous les nodes textuels visibles dans le DOM et applique
 * `looksLikeLinguiHash` à chacun. Retourne la liste des hash suspects.
 */
export async function findVisibleLinguiHashes(page: Page): Promise<string[]> {
  return await page.evaluate(() => {
    function isVisible(el: Element): boolean {
      const style = window.getComputedStyle(el)
      if (style.display === 'none' || style.visibility === 'hidden') return false
      const rect = (el as HTMLElement).getBoundingClientRect()
      return rect.width > 0 && rect.height > 0
    }

    function looksLikeHashClient(t: string): boolean {
      const x = t.trim()
      if (!/^[A-Za-z0-9_+/=]{6,8}$/.test(x)) return false
      const up = (x.match(/[A-Z]/g) || []).length
      const lo = (x.match(/[a-z]/g) || []).length
      return up >= 2 && lo >= 2
    }

    const suspects: string[] = []
    const walker = document.createTreeWalker(document.body, NodeFilter.SHOW_TEXT)
    let node: Node | null
    while ((node = walker.nextNode())) {
      const text = node.nodeValue?.trim() ?? ''
      if (!text) continue
      const parent = node.parentElement
      if (!parent) continue
      if (parent.tagName === 'SCRIPT' || parent.tagName === 'STYLE') continue
      if (!isVisible(parent)) continue
      if (looksLikeHashClient(text)) suspects.push(text)
    }
    return suspects
  })
}
