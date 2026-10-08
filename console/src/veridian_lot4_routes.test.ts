import { describe, it, expect } from 'vitest'
import { isRedirect } from '@tanstack/react-router'
import {
  router,
  workspaceLogsRoute,
  workspaceSmtpBridgeRedirectRoute,
  workspaceTemplatesRoute,
  workspaceTransactionalNotificationsRoute
} from './router'

// Lot 4 : Modeles, Journal et API d'envoi / SMTP Bridge sont des pages uniques pilotees par
// des parametres de recherche, et l'ancienne URL des Reglages SMTP Bridge redirige.

describe('route /settings/smtp-bridge', () => {
  it("existe et redirige vers l'onglet SMTP Bridge de la page transactionnelle", async () => {
    expect(Object.keys(router.routesByPath)).toContain(
      '/console/workspace/$workspaceId/settings/smtp-bridge'
    )
    const beforeLoad = workspaceSmtpBridgeRedirectRoute.options.beforeLoad as (ctx: unknown) => unknown
    let thrown: unknown
    try {
      await beforeLoad({ params: { workspaceId: 'ws-1' } })
    } catch (e) {
      thrown = e
    }
    expect(isRedirect(thrown)).toBe(true)
    const opts = (thrown as { options: Record<string, unknown> }).options
    expect(opts.to).toBe('/console/workspace/$workspaceId/transactional-notifications')
    expect(opts.params).toEqual({ workspaceId: 'ws-1' })
    expect(opts.search).toEqual({ tab: 'smtp-bridge' })
  })
})

describe('parametres de recherche des pages', () => {
  const validate = (route: { options: { validateSearch?: unknown } }, input: Record<string, unknown>) =>
    (route.options.validateSearch as (s: Record<string, unknown>) => Record<string, unknown>)(input)

  it('journal : type commercial ou transactionnel, le reste passe', () => {
    expect(validate(workspaceLogsRoute, { type: 'transactional' }).type).toBe('transactional')
    expect(validate(workspaceLogsRoute, { type: 'commercial' }).type).toBe('commercial')
    expect(validate(workspaceLogsRoute, { type: 'zzz' }).type).toBeUndefined()
    expect(validate(workspaceLogsRoute, { is_failed: 'true' }).is_failed).toBe('true')
  })

  it('modeles : family et category', () => {
    expect(validate(workspaceTemplatesRoute, { family: 'transactional' }).family).toBe('transactional')
    expect(validate(workspaceTemplatesRoute, { family: 'zzz' }).family).toBeUndefined()
    expect(validate(workspaceTemplatesRoute, { category: 'welcome' }).category).toBe('welcome')
  })

  it("API d'envoi : onglet smtp-bridge ou rien", () => {
    expect(validate(workspaceTransactionalNotificationsRoute, { tab: 'smtp-bridge' }).tab).toBe('smtp-bridge')
    expect(validate(workspaceTransactionalNotificationsRoute, { tab: 'x' }).tab).toBeUndefined()
  })
})
