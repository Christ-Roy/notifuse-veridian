import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { render, screen, waitFor, fireEvent } from '@testing-library/react'
import { i18n } from '@lingui/core'
import { I18nProvider } from '@lingui/react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { VeridianConnectAI } from './veridian_connect_ai'
import { fetchInstallLinks, sanitizeInstallLinks, VERIDIAN_MCP_FALLBACK_URL } from '../lib/veridian_connect_ai'

i18n.loadAndActivate({ locale: 'en', messages: {} })

vi.mock('../services/api/veridian', () => ({ veridianApi: { getMode: vi.fn() } }))
import { veridianApi } from '../services/api/veridian'

const HUB_RESPONSE = {
  url: 'https://app.veridian.site/api/mcp',
  assistants: [
    { cle: 'claude', libelle: 'Ajouter à Claude', installation: 'https://claude.ai/customize/connectors?modal=add-custom-connector', etape: null },
    { cle: 'chatgpt', libelle: 'Ouvrir ChatGPT', installation: 'https://chatgpt.com/#settings/Connectors', etape: 'Activez le mode développeur.' },
    { cle: 'cursor', libelle: 'Ajouter à Cursor', installation: 'cursor://anysphere.cursor-deeplink/mcp/install?name=veridian', etape: null },
    { cle: 'evil', libelle: 'Piège', installation: 'javascript:alert(1)', etape: null }
  ],
  claudeCode: 'claude mcp add --transport http veridian https://app.veridian.site/api/mcp',
  app: { cle: 'notifuse', nom: 'Mail', phrase: 'Une seule connexion Veridian.' }
}

const renderIt = () => {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  return render(
    <QueryClientProvider client={client}>
      <I18nProvider i18n={i18n}>
        <VeridianConnectAI />
      </I18nProvider>
    </QueryClientProvider>
  )
}

describe('sanitizeInstallLinks', () => {
  it('écarte les schémas dangereux et les entrées incomplètes', () => {
    const out = sanitizeInstallLinks(HUB_RESPONSE)
    expect(out?.assistants.map((a) => a.cle)).toEqual(['claude', 'chatgpt', 'cursor'])
    expect(out?.phrase).toBe('Une seule connexion Veridian.')
    expect(out?.claudeCode).toContain('claude mcp add')
  })

  it('rend null quand rien n’est exploitable', () => {
    expect(sanitizeInstallLinks(null)).toBeNull()
    expect(sanitizeInstallLinks('x')).toBeNull()
    expect(sanitizeInstallLinks({ url: 'javascript:alert(1)' })).toBeNull()
    expect(sanitizeInstallLinks({ url: 'pas une url' })).toBeNull()
  })
})

describe('fetchInstallLinks', () => {
  it('lit la route du Hub', async () => {
    const f = vi.fn().mockResolvedValue({ ok: true, json: async () => HUB_RESPONSE })
    const out = await fetchInstallLinks(f as unknown as typeof fetch)
    expect(out?.assistants).toHaveLength(3)
    expect(f.mock.calls[0][0]).toContain('/api/mcp/liens-installation?app=notifuse')
  })

  it('rend null sur erreur réseau, statut non 2xx ou délai dépassé', async () => {
    expect(await fetchInstallLinks(vi.fn().mockRejectedValue(new Error('réseau')) as unknown as typeof fetch)).toBeNull()
    expect(await fetchInstallLinks(vi.fn().mockResolvedValue({ ok: false }) as unknown as typeof fetch)).toBeNull()
    const hang = vi.fn((_u: string, init: RequestInit) =>
      new Promise((_res, rej) => init.signal?.addEventListener('abort', () => rej(new Error('abort'))))
    )
    expect(await fetchInstallLinks(hang as unknown as typeof fetch, 20)).toBeNull()
  })
})

describe('VeridianConnectAI', () => {
  beforeEach(() => vi.clearAllMocks())
  afterEach(() => vi.unstubAllGlobals())

  it("n'affiche rien hors mode veridian-managed", async () => {
    vi.mocked(veridianApi.getMode).mockResolvedValue({ mode: 'self-hosted', signin_url: '/console/signin' })
    const { container } = renderIt()
    await waitFor(() => expect(veridianApi.getMode).toHaveBeenCalled())
    expect(container.querySelector('[data-testid="veridian-connect-ai"]')).toBeNull()
  })

  it('ouvre le modal et rend les boutons du Hub (étape ChatGPT sous le bouton)', async () => {
    vi.mocked(veridianApi.getMode).mockResolvedValue({ mode: 'veridian-managed', signin_url: '/x', hub_url: 'https://app.veridian.site' })
    vi.stubGlobal('fetch', vi.fn().mockResolvedValue({ ok: true, json: async () => HUB_RESPONSE }))
    renderIt()
    fireEvent.click(await screen.findByTestId('veridian-connect-ai'))
    const claude = await screen.findByText('Ajouter à Claude')
    expect(claude.closest('a')?.getAttribute('href')).toContain('claude.ai/customize/connectors')
    expect(screen.getByText('Activez le mode développeur.')).toBeTruthy()
    expect(screen.queryByText('Piège')).toBeNull()
    expect(screen.queryByTestId('veridian-connect-ai-fallback')).toBeNull()
  })

  it("si le Hub ne répond pas : repli propre avec l'adresse à copier, aucun bouton mort", async () => {
    vi.mocked(veridianApi.getMode).mockResolvedValue({ mode: 'veridian-managed', signin_url: '/x', hub_url: 'https://app.veridian.site' })
    vi.stubGlobal('fetch', vi.fn().mockRejectedValue(new Error('hub down')))
    renderIt()
    fireEvent.click(await screen.findByTestId('veridian-connect-ai'))
    expect(await screen.findByTestId('veridian-connect-ai-fallback')).toBeTruthy()
    expect(screen.getByText(VERIDIAN_MCP_FALLBACK_URL)).toBeTruthy()
    expect(screen.queryByText('Ajouter à Claude')).toBeNull()
  })
})
