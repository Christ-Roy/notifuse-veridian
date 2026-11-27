import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, waitFor, within, fireEvent } from '@testing-library/react'
import { i18n } from '@lingui/core'
import { I18nProvider } from '@lingui/react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { VeridianMonIA } from './veridian_mon_ia'
import { affichageMonIA, urlCadreMonIA, urlRelierMonIA } from '../lib/veridian_mon_ia'

i18n.loadAndActivate({ locale: 'en', messages: {} })

vi.mock('../services/api/client', () => ({ api: { get: vi.fn() } }))
import { api } from '../services/api/client'

const HUB = 'https://app.veridian.site'
const TENANT = '80945a9e-a5c7-499f-bd9c-87920ad19c8d'
const ESPACE = 'e2esso5yd8jj8ljzwxtr'

const monter = () => {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  return render(
    <QueryClientProvider client={client}>
      <I18nProvider i18n={i18n}>
        <VeridianMonIA workspaceId={ESPACE} />
      </I18nProvider>
    </QueryClientProvider>
  )
}

const hubDit = (corps: unknown) => vi.mocked(api.get).mockResolvedValue(corps)

describe('règle d’affichage (lib)', () => {
  it('seul `prete` ouvre le cadre ; `sans_moteur` la carte ; le reste, rien', () => {
    expect(affichageMonIA('prete')).toBe('cadre')
    expect(affichageMonIA('sans_moteur')).toBe('carte')
    for (const e of ['eteinte', 'indisponible', '', undefined, null, 'autre']) {
      expect(affichageMonIA(e)).toBe('rien')
    }
  })

  it('le cadre pointe {HUB}/ia/chat avec l’organisation du Hub ; le renvoi {HUB}/dashboard/ia', () => {
    expect(urlCadreMonIA(HUB, TENANT)).toBe(`${HUB}/ia/chat?app=notifuse&tenant=${TENANT}`)
    expect(urlRelierMonIA(HUB)).toBe(`${HUB}/dashboard/ia`)
  })

  it('refuse une adresse de Hub douteuse et un tenant qui n’est pas un UUID (identifiant d’espace)', () => {
    expect(urlCadreMonIA(HUB, ESPACE)).toBeNull()
    expect(urlCadreMonIA('http://app.veridian.site', TENANT)).toBeNull()
    expect(urlCadreMonIA('https://u:p@app.veridian.site', TENANT)).toBeNull()
    expect(urlCadreMonIA('javascript:alert(1)', TENANT)).toBeNull()
    expect(urlRelierMonIA('')).toBeNull()
  })
})

describe('VeridianMonIA, marques communes (CONTRAT-HUB §5.24.1)', () => {
  beforeEach(() => vi.clearAllMocks())

  it('prete : bouton « Mon IA », et le cadre n’est inséré qu’au clic, dans un dialogue « Mon IA »', async () => {
    hubDit({ etat: 'prete', hub_url: HUB, tenant_id: TENANT })
    const { container } = monter()
    await waitFor(() => expect(container.querySelector('[data-mon-ia="prete"]')).not.toBeNull())
    expect(api.get).toHaveBeenCalledWith(`/api/veridian/ia/etat?workspace_id=${ESPACE}`)
    expect(container.querySelector('[data-mon-ia="sans_moteur"]')).toBeNull()
    // Avant le clic : aucun cadre, nulle part dans la page.
    expect(document.querySelector('iframe')).toBeNull()
    fireEvent.click(screen.getByRole('button', { name: 'Mon IA' }))
    const dialogue = await screen.findByRole('dialog', { name: 'Mon IA' })
    const cadre = within(dialogue).getByTestId('veridian-mon-ia-cadre')
    expect(cadre.getAttribute('src')).toBe(`${HUB}/ia/chat?app=notifuse&tenant=${TENANT}`)
    expect(cadre.getAttribute('src')).not.toContain(ESPACE)
  })

  it('sans_moteur : bouton « Relier mon IA », carte au clic avec le lien en nouvel onglet, AUCUN cadre ni avant ni après', async () => {
    hubDit({ etat: 'sans_moteur', hub_url: HUB, tenant_id: TENANT })
    const { container } = monter()
    await waitFor(() => expect(container.querySelector('[data-mon-ia="sans_moteur"]')).not.toBeNull())
    expect(container.querySelector('[data-mon-ia="prete"]')).toBeNull()
    expect(screen.queryByRole('button', { name: 'Mon IA' })).toBeNull()
    expect(document.querySelector('iframe')).toBeNull()
    expect(document.querySelector('[data-carte="relier-mon-ia"]')).toBeNull()
    fireEvent.click(screen.getByRole('button', { name: 'Relier mon IA' }))
    const carte = await waitFor(() => {
      const c = document.querySelector('[data-carte="relier-mon-ia"]')
      expect(c).not.toBeNull()
      return c as HTMLElement
    })
    const lien = within(carte).getByRole('link', { name: 'Relier mon IA' })
    expect(lien.getAttribute('href')).toBe(`${HUB}/dashboard/ia`)
    expect(lien.getAttribute('target')).toBe('_blank')
    expect(lien.getAttribute('rel')).toContain('noopener')
    expect(carte.textContent).toContain(
      'Votre IA travaille dans vos apps Veridian depuis son propre écran (Claude, ChatGPT, Mistral…) : reliez-la une fois, sans clé.'
    )
    // Jamais un champ à remplir.
    expect(carte.querySelector('input, textarea, select')).toBeNull()
    expect(document.querySelector('iframe')).toBeNull()
  })

  it.each([
    ['éteinte', { etat: 'eteinte' }],
    ['indisponible (404, Hub muet, 5xx : le serveur le dit ainsi)', { etat: 'indisponible' }],
    ['état inconnu', { etat: 'autre', hub_url: HUB, tenant_id: TENANT }],
    ['réponse vide', {}],
    ['prete sans organisation du Hub', { etat: 'prete', hub_url: HUB }],
    ['prete avec un identifiant d’espace au lieu de l’organisation', { etat: 'prete', hub_url: HUB, tenant_id: ESPACE }]
  ])('%s : aucune marque, aucun cadre', async (_nom, corps) => {
    hubDit(corps)
    const { container } = monter()
    await waitFor(() => expect(api.get).toHaveBeenCalled())
    await new Promise((r) => setTimeout(r, 30))
    expect(container.querySelector('[data-mon-ia]')).toBeNull()
    expect(container.querySelector('button')).toBeNull()
    expect(document.querySelector('iframe')).toBeNull()
  })

  it('la route de la console en échec (4xx, 5xx, réseau) : on se tait', async () => {
    vi.mocked(api.get).mockRejectedValue(new Error('500'))
    const { container } = monter()
    await waitFor(() => expect(api.get).toHaveBeenCalled())
    await new Promise((r) => setTimeout(r, 30))
    expect(container.querySelector('[data-mon-ia]')).toBeNull()
  })

  it('dans un cadre (console ouverte par une autre app) : rien n’est imbriqué', async () => {
    hubDit({ etat: 'prete', hub_url: HUB, tenant_id: TENANT })
    const top = Object.getOwnPropertyDescriptor(window, 'top')
    Object.defineProperty(window, 'top', { value: {}, configurable: true })
    try {
      const { container } = monter()
      await waitFor(() => expect(api.get).toHaveBeenCalled())
      await new Promise((r) => setTimeout(r, 30))
      expect(container.querySelector('[data-mon-ia]')).toBeNull()
    } finally {
      if (top) Object.defineProperty(window, 'top', top)
    }
  })
})
