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

  it("sert la page Profils d'envoi (lot 3)", () => {
    expect(paths).toContain('/console/workspace/$workspaceId/sending-profiles')
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

  it("regroupe la sidebar en deux groupes : Prospection et Transactionnel, plus de groupe Envoi", () => {
    expect(layout).toMatch(/makeGroup\('prospection'/)
    expect(layout).toMatch(/makeGroup\('transactional'/)
    expect(layout).not.toMatch(/makeGroup\('sending'/)
  })

  it("range « Profils d'envoi » dans Prospection, avant le journal, et le sélectionne sur sa route", () => {
    expect(layout).toMatch(/key: 'sending-profiles'/)
    expect(layout).toMatch(/to="\/console\/workspace\/\$workspaceId\/sending-profiles"/)
    const model = read('./layouts/veridian_sidebar_model.ts')
    expect(model).toMatch(/'sending-profiles',\s*'logs'\s*\]/)
    expect(model).toMatch(/pathname\.includes\('\/sending-profiles'\)/)
  })

  it("le groupe Transactionnel porte trois entrées : modèles, API d'envoi / SMTP Bridge, journal", () => {
    expect(layout).toMatch(/key: 'templates-transactional'/)
    expect(layout).toMatch(/key: 'logs-transactional'/)
    expect(layout).toMatch(/search=\{\{ family: 'transactional' \}\}/)
    expect(layout).toMatch(/search=\{\{ type: 'transactional' \}\}/)
  })

  it("SMTP Bridge a quitté les réglages et vit sur la page API d'envoi", () => {
    expect(settingsSidebar).not.toMatch(/smtp-bridge/)
    expect(settingsPage).not.toMatch(/smtp-bridge|SMTPBridgeSettings/)
    const page = read('./pages/TransactionalNotificationsPage.tsx')
    expect(page).toMatch(/SMTPBridgeSettings/)
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
    // Le champ reste éditable, replié dans les réglages avancés du profil, libellé « frein technique ».
    const advanced = read('./components/sending_profiles/veridian_profile_advanced.tsx')
    expect(advanced).toMatch(/SMTP technical brake \(messages\/minute\)/)
    const card = read('./components/sending_profiles/veridian_profile_card.tsx')
    expect(card).not.toMatch(/native_rate_per_min|rate_limit_per_minute/)
  })

  it("Réglages > Intégrations ne porte plus ni profils d'envoi ni boîtes IMAP, et renvoie vers la page", () => {
    const integrations = read('./components/settings/Integrations.tsx')
    expect(integrations).not.toMatch(/EmailIntegration|handleSelectGmailAppPassword|handleSelectProviderType/)
    expect(integrations).not.toMatch(/renderEmailProviderForm|constructProviderFromForm|toggleMarketingProfile/)
    expect(integrations).not.toMatch(/Type: \$\{integration\.type\}/)
    expect(integrations).not.toMatch(/emailProviders/)
    expect(integrations).toMatch(/sending-profiles/)
  })

  it("la page Profils d'envoi n'affiche aucun secret et ne relit jamais un mot de passe", () => {
    for (const file of [
      './pages/SendingProfilesPage.tsx',
      './components/sending_profiles/veridian_profile_card.tsx',
      './components/sending_profiles/veridian_profile_wizard.tsx'
    ]) {
      const source = read(file)
      expect(source).not.toMatch(/encrypted_password|has_password/)
    }
  })

  it('ne propose plus Supabase, LLM ni Firecrawl à la création', () => {
    const integrations = read('./components/settings/Integrations.tsx')
    expect(integrations).not.toMatch(/handleSelectSupabase|handleSelectLLMProvider|handleSelectFirecrawl/)
  })
})
