// === Signature Hub des E2E : l'UNIQUE endroit qui signe ===
//
// Source de vérité : internal/http/middleware/veridian_hmac.go (veridianHubMACv2, SignVeridianHubRequest).
// Vérifiée contre des vecteurs produits PAR LE GO (fixtures/hub-hmac-v2-vectors.json, hub-signature.test.mjs).
//
// v2 : X-Veridian-Timestamp (ms), X-Veridian-Sig-Version: 2, X-Veridian-Hub-Signature =
//   hex(HMAC-SHA256(secret, ts \n MÉTHODE \n chemin encodé \n query brute \n sha256hex(corps))).
// Une v2 est à usage unique : chaque requête signe à neuf (jamais d'en-têtes réutilisés, retries compris).
//
// La v1 (`${ts}.${corps}`, sans version) n'existe ici QUE pour les tests de rétrocompatibilité volontaires
// (signHubV1, nommés `[v1-volontaire]`). Quand VERIDIAN_HMAC_REQUIRE_V2 sera posé, ils attendront 401 :
// basculer HUB_V1_STILL_ACCEPTED à false.
import crypto from 'node:crypto'

export type HubHeaders = Record<string, string>

/** Tant que VERIDIAN_HMAC_REQUIRE_V2 est absent en prod, la v1 passe encore. Passer à false à la bascule. */
export const HUB_V1_STILL_ACCEPTED = true

/** Chemin encodé et query brute de l'URL, comme Go (URL.EscapedPath, URL.RawQuery). */
export function splitHubUrl(url: string): { path: string; rawQuery: string } {
  const u = new URL(url, 'http://placeholder.invalid')
  return { path: u.pathname, rawQuery: u.search.startsWith('?') ? u.search.slice(1) : '' }
}

/** Les trois en-têtes v2 pour (méthode, URL complète, corps brut). `timestamp` : surcharge (tests d'horodatage périmé). */
export function signHubV2(
  secret: string,
  method: string,
  url: string,
  body: string = '',
  opts: { timestamp?: string } = {}
): HubHeaders {
  const ts = opts.timestamp ?? Date.now().toString()
  const { path, rawQuery } = splitHubUrl(url)
  const bodyHash = crypto.createHash('sha256').update(body).digest('hex')
  const sig = crypto
    .createHmac('sha256', secret)
    .update(`${ts}\n${method.toUpperCase()}\n${path}\n${rawQuery}\n${bodyHash}`)
    .digest('hex')
  return {
    'X-Veridian-Timestamp': ts,
    'X-Veridian-Sig-Version': '2',
    'X-Veridian-Hub-Signature': sig,
  }
}

/** v1, RÉSERVÉE aux tests de rétrocompatibilité volontaires. */
export function signHubV1(secret: string, body: string = '', opts: { timestamp?: string } = {}): HubHeaders {
  const ts = opts.timestamp ?? Date.now().toString()
  return {
    'X-Veridian-Timestamp': ts,
    'X-Veridian-Hub-Signature': crypto.createHmac('sha256', secret).update(`${ts}.${body}`).digest('hex'),
  }
}

export interface HubFetchOptions {
  /** Corps JSON (sérialisé) ou chaîne brute. */
  body?: object | string | null
  headers?: Record<string, string>
  /** Surcharges de test : mauvais secret, horodatage périmé. */
  secret?: string
  timestamp?: string
  /** Rend la v1 au lieu de la v2 (tests `[v1-volontaire]` uniquement). */
  v1?: boolean
}

/** Appel signé v2 (signé à neuf à chaque appel). `url` complète. */
export async function hubFetch(
  baseUrl: string,
  hubSecret: string,
  path: string,
  method: string,
  opts: HubFetchOptions = {}
): Promise<Response> {
  const raw = opts.body == null ? '' : typeof opts.body === 'string' ? opts.body : JSON.stringify(opts.body)
  const url = `${baseUrl}${path}`
  const secret = opts.secret ?? hubSecret
  const sigHeaders = opts.v1
    ? signHubV1(secret, raw, { timestamp: opts.timestamp })
    : signHubV2(secret, method, url, raw, { timestamp: opts.timestamp })
  return fetch(url, {
    method,
    headers: { 'Content-Type': 'application/json', ...sigHeaders, ...(opts.headers ?? {}) },
    body: raw || undefined,
  })
}

/** Client lié à (baseUrl, secret) : `hmacFetch(path, method, body?, extraHeaders?)`, v2. */
export function hubClient(baseUrl: string, hubSecret: string) {
  return {
    hmacFetch: (path: string, method: string, body: object | string | null = null, headers: Record<string, string> = {}) =>
      hubFetch(baseUrl, hubSecret, path, method, { body, headers }),
    hubFetch: (path: string, method: string, opts: HubFetchOptions = {}) => hubFetch(baseUrl, hubSecret, path, method, opts),
  }
}

/** Vérifie la signature d'un webhook SORTANT de Notifuse (`X-Veridian-Notifuse-Signature` = HMAC(secret, `${ts}.${corps}`)) :
 *  protocole distinct de la signature Hub, gardé ici pour que createHmac ne vive qu'en un endroit. */
export function verifyNotifuseWebhookSignature(secret: string, ts: string, rawBody: string, received: string): boolean {
  return crypto.createHmac('sha256', secret).update(`${ts}.${rawBody}`).digest('hex') === received
}
