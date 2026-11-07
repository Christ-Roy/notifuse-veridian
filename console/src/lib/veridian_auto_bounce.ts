/**
 * Veridian — connexion automatique par rebond vers le Hub (CONTRAT-HUB §6bis.8.0, 07/11/2026).
 *
 * Une visite sans session sur une page protégée repart vers le Hub, qui rend la main avec un lien de
 * connexion et renvoie sur la page demandée. Aucun cookie sur `.veridian.site` : l'app ne garde que deux
 * repères locaux, sur son propre hôte.
 *
 *  - `veridian-logout` : posé par « Se déconnecter », levé à la connexion réussie. Tant qu'il est là,
 *    jamais de reconnexion silencieuse.
 *  - `veridian-auto-bounce` : 5 minutes, posé avant de partir. S'il est là au retour, on affiche la page
 *    de connexion au lieu de repartir (un seul essai, pas de boucle).
 *
 * Jamais dans une iframe. Le Hub répond 204 sans rien émettre à une requête qui n'est pas une navigation.
 */

export const LOGOUT_COOKIE = 'veridian-logout'
export const AUTO_BOUNCE_COOKIE = 'veridian-auto-bounce'

const AUTO_BOUNCE_TTL_SECONDS = 300
const LOGOUT_TTL_SECONDS = 365 * 24 * 3600

/** Pages qui n'exigent pas de session : jamais de rebond depuis elles. */
const PUBLIC_PATHS = ['/console/signin', '/console/logout', '/console/setup', '/console/accept-invitation']

export function hasCookie(name: string, cookieString: string): boolean {
  return cookieString.split(';').some((part) => part.trim().startsWith(`${name}=`))
}

/** Origine du Hub : https et `*.veridian.site` seulement, jamais une valeur libre venue du réseau. */
export function hubOrigin(hubUrl: string | undefined): string | null {
  if (!hubUrl) return null
  try {
    const u = new URL(hubUrl)
    if (u.protocol !== 'https:') return null
    if (u.hostname !== 'veridian.site' && !u.hostname.endsWith('.veridian.site')) return null
    return u.origin
  } catch {
    return null
  }
}

/** URL du rebond. `auto` marque le rebond silencieux (le Hub applique son propre anti-boucle). */
export function bounceUrl(hubUrl: string | undefined, nextHref: string, auto: boolean): string | null {
  const origin = hubOrigin(hubUrl)
  if (!origin) return null
  return `${origin}/api/auth/bounce/prepare?next=${encodeURIComponent(nextHref)}${auto ? '&auto=1' : ''}`
}

export interface AutoBounceInput {
  mode?: string
  hubUrl?: string
  pathname: string
  href: string
  cookie: string
  isTopWindow: boolean
  hasToken: boolean
}

/** URL vers laquelle repartir, ou null : on affiche la page de connexion. */
export function autoBounceTarget(input: AutoBounceInput): string | null {
  if (input.mode !== 'veridian-managed') return null
  if (input.hasToken) return null
  if (!input.isTopWindow) return null
  if (!input.pathname.startsWith('/console')) return null
  if (PUBLIC_PATHS.some((p) => input.pathname === p || input.pathname.startsWith(`${p}/`))) return null
  if (hasCookie(LOGOUT_COOKIE, input.cookie)) return null
  if (hasCookie(AUTO_BOUNCE_COOKIE, input.cookie)) return null
  return bounceUrl(input.hubUrl, input.href, true)
}

function setCookie(name: string, value: string, maxAgeSeconds: number): void {
  document.cookie = `${name}=${value}; Max-Age=${maxAgeSeconds}; path=/; SameSite=Lax`
}

/** À appeler juste avant de partir : l'essai automatique est compté 5 minutes. */
export function markAutoBounceAttempt(): void {
  setCookie(AUTO_BOUNCE_COOKIE, '1', AUTO_BOUNCE_TTL_SECONDS)
}

/** Déconnexion explicite : plus aucun rebond automatique jusqu'à la prochaine connexion réussie. */
export function markExplicitLogout(): void {
  setCookie(LOGOUT_COOKIE, '1', LOGOUT_TTL_SECONDS)
}

/** Connexion réussie : on lève les deux repères. */
export function clearBounceMarks(): void {
  setCookie(LOGOUT_COOKIE, '', 0)
  setCookie(AUTO_BOUNCE_COOKIE, '', 0)
}
