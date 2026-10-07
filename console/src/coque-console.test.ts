import { describe, it, expect } from 'vitest'
import { readFileSync } from 'node:fs'
import { resolve } from 'node:path'
import { router } from './router'

// Lot 1 « coque de la console » (07/10/2026) : le blog et la page de debug
// ont quitté la console, la ligne native de débit marketing aussi.
// Ces gardes cassent si l'un d'eux revient par un merge ou un copier-coller.

const read = (rel: string) => readFileSync(resolve(__dirname, rel), 'utf8')

describe('coque de la console : routes', () => {
  const paths = Object.keys(router.routesByPath)

  it('connaît les routes de base (la liste lue est valide)', () => {
    expect(paths).toContain('/console/workspace/$workspaceId/contacts')
    expect(paths).toContain('/console/workspace/$workspaceId/broadcasts')
    expect(paths).toContain('/console/workspace/$workspaceId/analytics')
  })

  it('ne sert plus ni /blog ni /debug-segment', () => {
    expect(paths.some((p) => p.includes('/blog'))).toBe(false)
    expect(paths.some((p) => p.includes('debug-segment'))).toBe(false)
  })
})

describe('coque de la console : sidebar et réglages', () => {
  const layout = read('./layouts/WorkspaceLayout.tsx')
  const settingsSidebar = read('./components/settings/SettingsSidebar.tsx')
  const settingsPage = read('./pages/WorkspaceSettingsPage.tsx')

  it("n'a plus d'entrée Blog ni Gestionnaire de fichiers dans la sidebar", () => {
    expect(layout).not.toMatch(/key: 'blog'/)
    expect(layout).not.toMatch(/key: 'file-manager'/)
    expect(layout).not.toMatch(/\/blog/)
  })

  it('regroupe la sidebar : Prospection, Transactionnel, Envoi', () => {
    expect(layout).toMatch(/makeGroup\('prospection'/)
    expect(layout).toMatch(/makeGroup\('transactional'/)
    expect(layout).toMatch(/makeGroup\('sending'/)
  })

  it("n'a plus de section Blog dans les réglages", () => {
    expect(settingsSidebar).not.toMatch(/'blog'/)
    expect(settingsPage).not.toMatch(/'blog'/)
    expect(settingsPage).not.toMatch(/BlogSettings/)
  })

  it("n'affiche plus la limite de débit marketing native ni ses extrapolations", () => {
    const integrations = read('./components/settings/Integrations.tsx')
    expect(integrations).not.toMatch(/emails per minute/)
    expect(integrations).not.toMatch(/emails per hour/)
    expect(integrations).not.toMatch(/emails per day/)
    expect(integrations).not.toMatch(/Rate Limit for Marketing/)
    // Le champ reste éditable, replié, avec le libellé « frein technique ».
    expect(integrations).toMatch(/SMTP technical brake \(messages\/minute\)/)
  })

  it('ne propose plus Supabase, LLM ni Firecrawl à la création', () => {
    const integrations = read('./components/settings/Integrations.tsx')
    expect(integrations).not.toMatch(/handleSelectSupabase|handleSelectLLMProvider|handleSelectFirecrawl/)
  })
})
