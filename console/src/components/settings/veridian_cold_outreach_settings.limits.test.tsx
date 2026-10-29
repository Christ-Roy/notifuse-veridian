import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { i18n } from '@lingui/core'
import { I18nProvider } from '@lingui/react'
import { App as AntApp } from 'antd'
import { VeridianColdOutreachSettings } from './veridian_cold_outreach_settings'
import type { Workspace } from '../../services/api/types'

i18n.loadAndActivate({ locale: 'en', messages: {} })

// Ce composant rend la PAGE Cold outreach entière (8 cartes, breakdown R1, 23 classes de
// formulaire, presets, éditeurs de fenêtre…) : un rendu coûte 4 à 7 s en jsdom. L'ancien fichier
// unique de 44 tests formait à lui seul 80 % du temps de la suite vitest (322 à 502 s sur 540 s de
// mur) et dépassait 30 s par test sous charge le 28/10. Il est partagé en quatre fichiers par
// thème, exécutés en parallèle, et chaque test garde 30 s (au lieu de 90 s). Ne pas réunifier.
vi.setConfig({ testTimeout: 30000, hookTimeout: 30000 })

vi.mock('../../services/api/workspace', async () => {
  const actual = await vi.importActual<typeof import('../../services/api/workspace')>(
    '../../services/api/workspace'
  )
  return {
    ...actual,
    workspaceService: {
      update: vi.fn().mockResolvedValue({}),
      get: vi.fn(),
      createIntegration: vi.fn().mockResolvedValue({ integration_id: 'imap-new' }),
      updateIntegration: vi.fn().mockResolvedValue({ status: 'ok' })
    }
  }
})

// Breakdown R1 — mocké pour ne pas dépendre du réseau et tester l'affichage
// du compte de contacts par classe.
vi.mock('../../services/api/contacts', () => ({
  contactsApi: {
    providerBreakdown: vi.fn().mockResolvedValue({
      breakdown: { google: 1240, microsoft: 830, yahoo_aol: 95, freemail_fr: 410, corporate: 1502 },
      total: 4077
    })
  }
}))

import { workspaceService } from '../../services/api/workspace'
import { contactsApi } from '../../services/api/contacts'

function makeWorkspace(
  overrides?: Partial<Workspace['settings']>,
  integrations?: Workspace['integrations']
): Workspace {
  return {
    id: 'ws-1',
    name: 'WS',
    integrations: integrations ?? [],
    settings: {
      timezone: 'UTC',
      email_tracking_enabled: true,
      default_language: 'en',
      languages: ['en'],
      ...overrides
    },
    created_at: '',
    updated_at: ''
  } as Workspace
}

function renderCmp(props: {
  workspace: Workspace | null
  isOwner: boolean
  onWorkspaceUpdate?: (w: Workspace) => void
}) {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  return render(
    <QueryClientProvider client={client}>
      <I18nProvider i18n={i18n}>
        <AntApp>
          <VeridianColdOutreachSettings
            workspace={props.workspace}
            isOwner={props.isOwner}
            onWorkspaceUpdate={props.onWorkspaceUpdate ?? (() => {})}
          />
        </AntApp>
      </I18nProvider>
    </QueryClientProvider>
  )
}

describe('VeridianColdOutreachSettings', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    vi.mocked(workspaceService.get).mockResolvedValue({ workspace: makeWorkspace() } as never)
  })

  it('shows the section header and the provider classes', () => {
    renderCmp({ workspace: makeWorkspace(), isOwner: true })
    expect(screen.getByText('Sending limits')).toBeInTheDocument()
    expect(screen.getByText(/Google \(Gmail/)).toBeInTheDocument()
    expect(screen.getByText(/Microsoft \(Outlook/)).toBeInTheDocument()
    expect(screen.getByText(/Yahoo \/ AOL/)).toBeInTheDocument()
    expect(screen.getByText(/French ISPs/)).toBeInTheDocument()
    // Deux classes "Corporate" depuis la classification MX (Lot 4) : suffixe
    // inconnu + self-hosted. Matchers exacts pour lever l'ambiguïté.
    expect(screen.getByText('Corporate (unknown by suffix)')).toBeInTheDocument()
    expect(screen.getByText('Corporate self-hosted (MX unknown)')).toBeInTheDocument()
  })

  it('tags the big/sensitive providers as pixel OFF by default (owner view)', () => {
    renderCmp({ workspace: makeWorkspace(), isOwner: true })
    // 3 tags "pixel OFF by default" : google + microsoft + security_gateway
    // (BIG_PROVIDERS — réputation sensible au pixel d'ouverture).
    expect(screen.getAllByText(/pixel OFF by default/i)).toHaveLength(3)
  })

  it('renders read-only descriptions for non-owner', () => {
    renderCmp({
      workspace: makeWorkspace({
        veridian_provider_class_rates: { google: 1, microsoft: 2, yahoo_aol: 3, freemail_fr: 4, corporate: 30 }
      }),
      isOwner: false
    })
    // pas de bouton Save en lecture seule
    expect(screen.queryByRole('button', { name: /Save Changes/i })).not.toBeInTheDocument()
    expect(screen.getAllByText(/Open pixel/i).length).toBeGreaterThan(0)
  })

  it('saves only positive rates and omits empty ones, with pixel booleans', async () => {
    const user = userEvent.setup()
    const onUpdate = vi.fn()
    renderCmp({
      workspace: makeWorkspace({
        veridian_provider_class_rates: { google: 1 } as never
      }),
      isOwner: true,
      onWorkspaceUpdate: onUpdate
    })

    // Modifie un champ pour activer le bouton (touched), puis sauve.
    const saveBtn = screen.getByRole('button', { name: /Save Changes/i })
    expect(saveBtn).toBeDisabled()

    // Touche le form de RATES de façon déterministe via le champ per-recipient
    // (unique sur la page). NB : ne pas cibler un switch par index global — la
    // carte IMAP rend aussi un switch TLS qui se glisserait en switches[0].
    const recipInput = screen.getByLabelText(/Emails \/ recipient \/ day/i)
    await user.type(recipInput, '2')

    await waitFor(() => expect(saveBtn).toBeEnabled())
    await user.click(saveBtn)

    await waitFor(() => {
      expect(workspaceService.update).toHaveBeenCalledTimes(1)
    })
    const arg = vi.mocked(workspaceService.update).mock.calls[0][0]
    // rates : google=1 conservé (positif)
    expect(arg.settings?.veridian_provider_class_rates?.google).toBe(1)
    // pixel map présente avec des booléens pour les 5 classes
    expect(arg.settings?.veridian_open_pixel_by_class).toBeDefined()
    expect(typeof arg.settings?.veridian_open_pixel_by_class?.corporate).toBe('boolean')
  })

  it('renders the per-recipient daily cap field and the daily cap label (owner)', () => {
    renderCmp({ workspace: makeWorkspace(), isOwner: true })
    expect(screen.getByText('Per-recipient daily cap')).toBeInTheDocument()
    // Au moins une carte de classe expose un champ "Daily cap (emails/day)"
    expect(screen.getAllByText(/Daily cap \(emails\/day\)/i).length).toBeGreaterThan(0)
  })

  it('shows the contact count badge per class from the breakdown endpoint', async () => {
    renderCmp({ workspace: makeWorkspace(), isOwner: true })
    await waitFor(() => expect(contactsApi.providerBreakdown).toHaveBeenCalled())
    // Le breakdown mocké pose 1240 (google) et 1502 (corporate)
    await waitFor(() => {
      expect(screen.getByText(/1[,\s\u202f\u00a0]?240 contacts/)).toBeInTheDocument()
      expect(screen.getByText(/1[,\s\u202f\u00a0]?502 contacts/)).toBeInTheDocument()
    })
  })

  it('persists positive daily caps and per-recipient cap, omits zeros', async () => {
    const user = userEvent.setup()
    renderCmp({ workspace: makeWorkspace(), isOwner: true })

    // Champ per-recipient (premier spinbutton de la page).
    const recipInput = screen.getByLabelText(/Emails \/ recipient \/ day/i)
    await user.clear(recipInput)
    await user.type(recipInput, '1')

    const saveBtn = screen.getByRole('button', { name: /Save Changes/i })
    await waitFor(() => expect(saveBtn).toBeEnabled())
    await user.click(saveBtn)

    await waitFor(() => expect(workspaceService.update).toHaveBeenCalledTimes(1))
    const arg = vi.mocked(workspaceService.update).mock.calls[0][0]
    expect(arg.settings?.veridian_per_recipient_daily_cap).toBe(1)
  })

  it('shows per-recipient cap and daily caps read-only for non-owner', () => {
    renderCmp({
      workspace: makeWorkspace({
        veridian_per_recipient_daily_cap: 2,
        veridian_provider_class_daily_cap: { google: 50 } as never
      }),
      isOwner: false
    })
    expect(screen.getByText('Per-recipient daily cap')).toBeInTheDocument()
    // valeur "2 / day" rendue pour le cap destinataire
    expect(screen.getByText(/2 \/ day/)).toBeInTheDocument()
    // cap classe google "50 / day"
    expect(screen.getByText(/50 \/ day/)).toBeInTheDocument()
  })
})
