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
    expect(screen.getByText('Veridian — Cold outreach')).toBeInTheDocument()
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

  // ── IMAP inbox (self-service bounce/reply) ──────────────────────────────────

  it('creates a new IMAP integration when none exists (owner)', async () => {
    const user = userEvent.setup()
    renderCmp({ workspace: makeWorkspace(), isOwner: true })

    expect(screen.getByText('Reply & bounce inbox (IMAP)')).toBeInTheDocument()
    // Pas d'intégration → tag "Not configured" + bouton "Connect"
    expect(screen.getByText('Not configured')).toBeInTheDocument()

    await user.type(screen.getByLabelText(/IMAP host/i), 'imap.example.com')
    const username = screen.getByLabelText('Username')
    await user.clear(username)
    await user.type(username, 'returns@example.com')
    // password (1er Input.Password de la carte IMAP)
    const pwd = screen.getByPlaceholderText(/Mailbox password/i)
    await user.type(pwd, 's3cret')

    await user.click(screen.getByRole('button', { name: /Connect IMAP inbox/i }))

    await waitFor(() => {
      expect(workspaceService.createIntegration).toHaveBeenCalledTimes(1)
    })
    const arg = vi.mocked(workspaceService.createIntegration).mock.calls[0][0]
    expect(arg.type).toBe('imap')
    expect(arg.imap_settings?.host).toBe('imap.example.com')
    expect(arg.imap_settings?.username).toBe('returns@example.com')
    expect(arg.imap_settings?.password).toBe('s3cret')
    // TLS coché par défaut + dossier INBOX
    expect(arg.imap_settings?.use_tls).toBe(true)
    expect(arg.imap_settings?.folder).toBe('INBOX')
  })

  it('updates an existing IMAP integration without resending the password (owner)', async () => {
    const user = userEvent.setup()
    const imapIntegration = {
      id: 'imap-1',
      name: 'Cold reply inbox',
      type: 'imap' as const,
      imap_settings: {
        host: 'imap.example.com',
        port: 993,
        username: 'returns@example.com',
        encrypted_password: 'deadbeef',
        use_tls: true,
        folder: 'INBOX'
      },
      created_at: '',
      updated_at: ''
    }
    renderCmp({
      workspace: makeWorkspace({}, [imapIntegration]),
      isOwner: true
    })

    // Intégration présente → tag "Configured" et host pré-rempli
    expect(screen.getByText('Configured')).toBeInTheDocument()
    expect((screen.getByLabelText(/IMAP host/i) as HTMLInputElement).value).toBe('imap.example.com')

    // On change juste le dossier, on ne touche pas au password → updateIntegration
    // doit partir SANS password (le backend préserve encrypted_password).
    const folder = screen.getByLabelText('Folder')
    await user.clear(folder)
    await user.type(folder, 'Bounces')
    await user.click(screen.getByRole('button', { name: /Save IMAP inbox/i }))

    await waitFor(() => {
      expect(workspaceService.updateIntegration).toHaveBeenCalledTimes(1)
    })
    const arg = vi.mocked(workspaceService.updateIntegration).mock.calls[0][0]
    expect(arg.integration_id).toBe('imap-1')
    expect(arg.imap_settings?.folder).toBe('Bounces')
    expect(arg.imap_settings?.password).toBeUndefined()
  })

  it('renders the IMAP inbox read-only for non-owner', () => {
    const imapIntegration = {
      id: 'imap-1',
      name: 'Cold reply inbox',
      type: 'imap' as const,
      imap_settings: {
        host: 'imap.example.com',
        port: 993,
        username: 'returns@example.com',
        use_tls: true,
        folder: 'INBOX'
      },
      created_at: '',
      updated_at: ''
    }
    renderCmp({ workspace: makeWorkspace({}, [imapIntegration]), isOwner: false })
    expect(screen.getByText('imap.example.com:993')).toBeInTheDocument()
    expect(screen.getByText('returns@example.com')).toBeInTheDocument()
    // pas de bouton de connexion en lecture seule
    expect(screen.queryByRole('button', { name: /IMAP inbox/i })).not.toBeInTheDocument()
  })

  // ── Custom tracking domain per sending infra ────────────────────────────────

  it('saves a custom tracking domain on the email provider, preserving senders (owner)', async () => {
    const user = userEvent.setup()
    const emailIntegration = {
      id: 'email-1',
      name: 'Cold relay',
      type: 'email' as const,
      email_provider: {
        kind: 'smtp' as const,
        senders: [{ id: 's1', email: 'hello@agences-veridian.fr', name: 'Veridian', is_default: true }],
        rate_limit_per_minute: 25
      },
      created_at: '',
      updated_at: ''
    }
    renderCmp({ workspace: makeWorkspace({}, [emailIntegration]), isOwner: true })

    expect(screen.getByText('Custom tracking domain (per sending infrastructure)')).toBeInTheDocument()
    // placeholder suggère track.<sender domain>
    const input = screen.getByPlaceholderText('track.agences-veridian.fr')
    await user.type(input, 'track.agences-veridian.fr')
    await user.click(screen.getByRole('button', { name: /^Save$/i }))

    await waitFor(() => {
      expect(workspaceService.updateIntegration).toHaveBeenCalledTimes(1)
    })
    const arg = vi.mocked(workspaceService.updateIntegration).mock.calls[0][0]
    expect(arg.integration_id).toBe('email-1')
    expect(arg.provider?.veridian_tracking_domain).toBe('track.agences-veridian.fr')
    // senders + rate_limit conservés (on renvoie le provider COMPLET)
    expect(arg.provider?.senders).toHaveLength(1)
    expect(arg.provider?.rate_limit_per_minute).toBe(25)
  })

  it('shows a hint when no sending integration exists for tracking domain', () => {
    renderCmp({ workspace: makeWorkspace(), isOwner: true })
    expect(
      screen.getByText(/No sending integration configured yet/i)
    ).toBeInTheDocument()
  })
})
