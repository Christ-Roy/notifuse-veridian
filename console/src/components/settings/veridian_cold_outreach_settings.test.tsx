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

vi.mock('../../services/api/workspace', async () => {
  const actual = await vi.importActual<typeof import('../../services/api/workspace')>(
    '../../services/api/workspace'
  )
  return {
    ...actual,
    workspaceService: {
      update: vi.fn().mockResolvedValue({}),
      get: vi.fn()
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

function makeWorkspace(overrides?: Partial<Workspace['settings']>): Workspace {
  return {
    id: 'ws-1',
    name: 'WS',
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

  it('shows the section header and all 5 provider classes', () => {
    renderCmp({ workspace: makeWorkspace(), isOwner: true })
    expect(screen.getByText('Veridian — Cold outreach')).toBeInTheDocument()
    expect(screen.getByText(/Google \(Gmail/)).toBeInTheDocument()
    expect(screen.getByText(/Microsoft \(Outlook/)).toBeInTheDocument()
    expect(screen.getByText(/Yahoo \/ AOL/)).toBeInTheDocument()
    expect(screen.getByText(/French ISPs/)).toBeInTheDocument()
    expect(screen.getByText(/Corporate/)).toBeInTheDocument()
  })

  it('tags Google and Microsoft as pixel OFF by default (owner view)', () => {
    renderCmp({ workspace: makeWorkspace(), isOwner: true })
    // 2 tags "pixel OFF by default" (google + microsoft)
    expect(screen.getAllByText(/pixel OFF by default/i)).toHaveLength(2)
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

    // Le rate google est pré-rempli à 1 ; on bascule un toggle pixel pour toucher le form.
    const switches = screen.getAllByRole('switch')
    await user.click(switches[0]) // toggle pixel google

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
      expect(screen.getByText(/1240 contacts/)).toBeInTheDocument()
      expect(screen.getByText(/1502 contacts/)).toBeInTheDocument()
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
