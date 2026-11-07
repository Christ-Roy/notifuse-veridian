import { describe, it, expect, beforeEach } from 'vitest'
import {
  autoBounceTarget,
  bounceUrl,
  hubOrigin,
  hasCookie,
  markAutoBounceAttempt,
  markExplicitLogout,
  clearBounceMarks,
  LOGOUT_COOKIE,
  AUTO_BOUNCE_COOKIE,
  type AutoBounceInput
} from './veridian_auto_bounce'

const base: AutoBounceInput = {
  mode: 'veridian-managed',
  hubUrl: 'https://app.veridian.site/dashboard',
  pathname: '/console/workspace/ws1/contacts',
  href: 'https://notifuse.app.veridian.site/console/workspace/ws1/contacts?a=1',
  cookie: '',
  isTopWindow: true,
  hasToken: false
}

describe('autoBounceTarget', () => {
  it('repart vers le Hub avec la page demandée et auto=1', () => {
    expect(autoBounceTarget(base)).toBe(
      'https://app.veridian.site/api/auth/bounce/prepare?next=' +
        encodeURIComponent(base.href) +
        '&auto=1'
    )
  })

  it.each([
    ['instance autonome', { mode: 'self-hosted' }],
    ['session locale présente', { hasToken: true }],
    ['dans une iframe', { isTopWindow: false }],
    ['page de connexion', { pathname: '/console/signin' }],
    ['déconnexion', { pathname: '/console/logout' }],
    ['installation', { pathname: '/console/setup' }],
    ['invitation', { pathname: '/console/accept-invitation' }],
    ['hors console', { pathname: '/veridian/auto-login' }],
    ['déconnexion explicite', { cookie: `${LOGOUT_COOKIE}=1` }],
    ['un essai déjà fait', { cookie: `a=b; ${AUTO_BOUNCE_COOKIE}=1` }],
    ['Hub en http', { hubUrl: 'http://app.veridian.site' }],
    ['Hub hors veridian.site', { hubUrl: 'https://evil.example' }],
    ['Hub absent', { hubUrl: undefined }]
  ])('ne repart pas : %s', (_name, patch) => {
    expect(autoBounceTarget({ ...base, ...patch })).toBeNull()
  })
})

describe('hubOrigin / bounceUrl', () => {
  it('normalise sur l’origine et refuse les valeurs libres', () => {
    expect(hubOrigin('https://app.veridian.site/dashboard')).toBe('https://app.veridian.site')
    expect(hubOrigin('https://hub.staging.veridian.site')).toBe('https://hub.staging.veridian.site')
    expect(hubOrigin('https://veridian.site.evil.example')).toBeNull()
    expect(hubOrigin('javascript:alert(1)')).toBeNull()
  })

  it('sans auto pour le bouton « Continuer »', () => {
    expect(bounceUrl('https://app.veridian.site', 'https://x/console', false)).toBe(
      'https://app.veridian.site/api/auth/bounce/prepare?next=' + encodeURIComponent('https://x/console')
    )
  })
})

describe('repères locaux', () => {
  beforeEach(() => {
    clearBounceMarks()
  })

  it('posent, lisent et lèvent les cookies', () => {
    expect(hasCookie(AUTO_BOUNCE_COOKIE, document.cookie)).toBe(false)
    markAutoBounceAttempt()
    expect(hasCookie(AUTO_BOUNCE_COOKIE, document.cookie)).toBe(true)
    markExplicitLogout()
    expect(hasCookie(LOGOUT_COOKIE, document.cookie)).toBe(true)
    clearBounceMarks()
    expect(hasCookie(AUTO_BOUNCE_COOKIE, document.cookie)).toBe(false)
    expect(hasCookie(LOGOUT_COOKIE, document.cookie)).toBe(false)
  })

  it('boucle : après un essai, le même contexte ne repart plus', () => {
    const first = autoBounceTarget({ ...base, cookie: document.cookie })
    expect(first).not.toBeNull()
    markAutoBounceAttempt()
    expect(autoBounceTarget({ ...base, cookie: document.cookie })).toBeNull()
  })
})
