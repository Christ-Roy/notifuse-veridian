// === Veridian patch — helpers suite E2E console (staging réel) ===
//
// Cette suite est complémentaire de `console/e2e-prod-smoke/` :
//   - prod-smoke : tourne en local sur `vite preview` du dist/, juge le boot
//     sans backend. Bloquant en CI principale.
//   - console-suite (ici) : tourne sur staging RÉEL (notifuse.staging.veridian.site),
//     authentifie un tenant fraîchement provisionné, exerce les écrans
//     post-login. Informative d'abord (workflow CI séparé non-bloquant).
//
// Pourquoi sur staging et pas sur preview local : on veut attraper les
// régressions qui n'apparaissent qu'avec un vrai backend (catalogues Lingui
// servis live, tenant qui existe vraiment, sidebar qui se monte avec le
// vrai user, etc.). Les 2 incidents 2026-05-22 (createContext + hash Lingui)
// étaient invisibles en jsdom mais visibles ici.
//
// Le pattern réutilise les helpers prod-smoke (trackConsoleErrors,
// findVisibleLinguiHashes) mais dupliqué côté tests/e2e-veridian pour rester
// autonome : la suite console-suite a sa propre config Playwright et ne
// dépend pas de console/node_modules.

import type { Page, ConsoleMessage } from '@playwright/test'
import * as crypto from 'crypto'

const NOTIFUSE_URL = process.env.NOTIFUSE_URL!
const HUB_API_SECRET = process.env.HUB_API_SECRET!

if (!NOTIFUSE_URL || !HUB_API_SECRET) {
  throw new Error('NOTIFUSE_URL and HUB_API_SECRET env vars required')
}

export { NOTIFUSE_URL, HUB_API_SECRET }

/**
 * Sign un body HMAC SHA256 pour les endpoints `/api/tenants/*` et
 * `/api/veridian/admin/*` côté Notifuse. Format `timestamp.body`,
 * timestamp en millisecondes Unix (drift accepté ±5min côté serveur).
 */
export function signHMAC(body: string): { timestamp: string; signature: string } {
  const timestamp = Date.now().toString()
  const signature = crypto
    .createHmac('sha256', HUB_API_SECRET)
    .update(`${timestamp}.${body}`)
    .digest('hex')
  return { timestamp, signature }
}

/**
 * HMAC fetch helper : sign + envoie en POST/GET/DELETE avec headers
 * `X-Veridian-Hub-Signature` + `X-Veridian-Timestamp`. Retourne la Response
 * brute (l'appelant fait .text/.json selon besoin).
 */
export async function hmacFetch(
  path: string,
  method: string,
  body: object | null = null
): Promise<Response> {
  const rawBody = body ? JSON.stringify(body) : ''
  const { timestamp, signature } = signHMAC(rawBody)
  return fetch(`${NOTIFUSE_URL}${path}`, {
    method,
    headers: {
      'Content-Type': 'application/json',
      'X-Veridian-Hub-Signature': signature,
      'X-Veridian-Timestamp': timestamp
    },
    body: rawBody || undefined
  })
}

/**
 * Provisionne un tenant frais via `POST /api/tenants/provision` et retourne
 * le payload complet (workspace_id, owner_user_id, api_key, magic_link,
 * auto_login_url). Le test consommera principalement `auto_login_url` pour
 * authentifier le navigateur sans saisie manuelle de code.
 */
export interface ProvisioningResponse {
  workspace_id: string
  owner_user_id: string
  api_key: string
  magic_link: string
  auto_login_url: string
  created: boolean
}

export async function provisionTenant(
  tenantId: string,
  ownerEmail: string,
  plan: 'free' | 'pro' | 'business' | 'enterprise' = 'pro'
): Promise<ProvisioningResponse> {
  const res = await hmacFetch('/api/tenants/provision', 'POST', {
    tenant_id: tenantId,
    owner_email: ownerEmail,
    plan
  })
  const txt = await res.text()
  if (res.status !== 200) {
    throw new Error(`provisionTenant failed (${res.status}): ${txt}`)
  }
  return JSON.parse(txt)
}

/**
 * Wipe un (ou plusieurs) tenants via l'endpoint admin HMAC.
 * `safety_client_prefixes` reste sur les prefixes connus pour ne JAMAIS
 * toucher accidentellement un client réel ou un canary witness.
 */
export async function wipeTenants(tenantIds: string[]): Promise<void> {
  if (tenantIds.length === 0) return
  try {
    await hmacFetch('/api/veridian/admin/wipe-test-tenants', 'POST', {
      tenant_ids: tenantIds,
      safety_client_prefixes: ['canary', 'robertbrunon', 'robertstagingtest']
    })
  } catch (err) {
    // afterAll cleanup non-fatal — le wipe global CI passera derrière.
    console.warn(`wipeTenants failed (non-fatal): ${err}`)
  }
}

/**
 * Génère un tenant id unique avec prefix `cnsl` (CoNSoLe suite). Conforme
 * convention Lot N (3+ chars, sera matché par wipe par prefix si l'afterAll
 * fait défaut). 6 chars suffisants vu qu'un seul tenant par run.
 */
export function newTenantId(): string {
  return `cnsl${Date.now().toString(36).slice(-6)}`
}

// ─────────────────────────────────────────────────────────────────────────────
// Browser-side helpers : track erreurs console + détection hash Lingui
// ─────────────────────────────────────────────────────────────────────────────

/**
 * Erreurs console bénignes — on garde la liste STRICTE pour ne pas masquer
 * de vrai bug. Tout ce qui n'est pas listé ici fait failer le test.
 */
const BENIGN_CONSOLE_ERROR_PATTERNS = [
  /favicon/i,
  /net::ERR_/i,
  /Failed to load resource/i,
  // Les 401/404 sur endpoints API peuvent survenir (user.me transitoire,
  // workspace pas encore loaded). Ils n'indiquent pas un boot cassé.
  /\b(401|404)\b/,
  /strict mode/i,
  // Antd "Warning: [antd: ..." en dev — pas en prod, garde-fou tout de même.
  /\[antd:/i,
  // Vite HMR (jamais en prod build mais sécurité).
  /vite/i,
  // Sentry ou autres trackers qui peuvent logger sans backend configuré.
  /sentry/i,
  // Logout page sans token actif → backend refuse l'appel logout (normal).
  // Le frontend logge cette erreur mais elle n'indique pas un boot cassé,
  // au contraire ça prouve que le client API marche.
  /Failed to logout on backend.*Authorization header is required/i,
  // Idem pour les calls workspaces.list ou user.me déclenchés sur les pages
  // publiques avant que l'auth context ait validé qu'on n'a pas de token —
  // le frontend logge mais c'est attendu sur signin/logout.
  /Authorization header is required/i,
  // Warning Chrome bénin : la page intermédiaire /veridian/auto-login.html
  // utilise un <meta http-equiv="X-Frame-Options"> que Chrome refuse depuis
  // 2020. Pas un bug — Chrome veut juste le header HTTP au lieu du meta tag.
  /X-Frame-Options may only be set via an HTTP header/i,
  // Idem pour CSP via meta tag (Chrome préfère header HTTP).
  /Content-Security-Policy.*meta/i
]

/**
 * Installe le listener `console.error` + `pageerror`. C'est `pageerror`
 * qui aurait remonté `Cannot read properties of undefined (reading
 * 'createContext')` de l'incident #1. Retourne le tableau qui se remplit
 * en temps réel — l'appelant assert qu'il est vide en fin de test.
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
 * Heuristique anti-hash Lingui :
 *   - 6-8 chars alphanumériques purs
 *   - >= 2 majuscules ET >= 2 minuscules (mots français/anglais courants
 *     ne mélangent jamais autant de casses sur si peu de chars)
 * Évite les faux positifs sur les abbréviations type "GMT", "URL" (que
 * majuscules) ou les mots courts type "Plan" (1 maj + 3 min).
 */
export async function findVisibleLinguiHashes(page: Page): Promise<string[]> {
  return await page.evaluate(() => {
    function isVisible(el: Element): boolean {
      const style = window.getComputedStyle(el)
      if (style.display === 'none' || style.visibility === 'hidden') return false
      const rect = (el as HTMLElement).getBoundingClientRect()
      return rect.width > 0 && rect.height > 0
    }

    function looksLikeHash(t: string): boolean {
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
      if (looksLikeHash(text)) suspects.push(text)
    }
    return suspects
  })
}

/**
 * Attend que la SPA console ait monté (splash inline remplacé par React).
 * Réutilise la même condition que `console/e2e-prod-smoke/helpers.ts` pour
 * la cohérence cross-suite.
 */
export async function waitForAppMount(page: Page, timeout = 30_000): Promise<void> {
  await page.waitForFunction(
    () => {
      const splash = document.querySelector('.preload-splash')
      if (!splash) return true
      const root = document.getElementById('root')
      return !!root && root.children.length > 0 && !root.contains(splash)
    },
    { timeout }
  )
}

/**
 * Login un tenant via auto-login URL : navigate, attend redirect /console,
 * vérifie que localStorage.auth_token est posé. Retourne quand le navigateur
 * est dans l'état "authentifié et workspace sélectionné".
 *
 * Pattern éprouvé dans `saasification.spec.ts` test 3.
 */
export async function loginViaAutoLogin(page: Page, autoLoginUrl: string): Promise<void> {
  await page.goto(autoLoginUrl)
  // Page intermédiaire HTML stocke auth_token puis redirect /console (root).
  // Le frontend pick ensuite le workspace tout seul.
  await page.waitForURL(/\/console(\/|$|\?)/, { timeout: 30_000 })
  const authToken = await page.evaluate(() => localStorage.getItem('auth_token'))
  if (!authToken || authToken.length < 50) {
    throw new Error(
      `auto-login échoué : auth_token=${authToken?.slice(0, 20) ?? 'null'}... (length=${authToken?.length ?? 0})`
    )
  }
  // Laisse le frontend hydrater + charger workspaces.
  await page.waitForTimeout(2000)
}
