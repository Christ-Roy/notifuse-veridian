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

  // ── Preset « Mode warmup » ────────────────────────────────────────────────

  it('shows the warmup preset Apply button for owner only', () => {
    renderCmp({ workspace: makeWorkspace(), isOwner: true })
    expect(
      screen.getByRole('button', { name: /Apply Warm-up \(cautious\) preset/i })
    ).toBeInTheDocument()
  })

  it('does not show the warmup preset button for non-owner', () => {
    renderCmp({ workspace: makeWorkspace(), isOwner: false })
    expect(
      screen.queryByRole('button', { name: /Apply Warm-up \(cautious\) preset/i })
    ).not.toBeInTheDocument()
  })

  it('applying the warmup preset fills caps/per-recipient WITHOUT touching per-sender (doctrine 2026-06-18), then Save persists', async () => {
    const user = userEvent.setup()
    renderCmp({ workspace: makeWorkspace(), isOwner: true })

    // 1) Clic preset Warm-up → Popconfirm → Apply. Rien n'est persisté à ce stade.
    await user.click(screen.getByRole('button', { name: /Apply Warm-up \(cautious\) preset/i }))
    await user.click(await screen.findByRole('button', { name: /^Apply$/i }))
    expect(workspaceService.update).not.toHaveBeenCalled()

    // per-recipient = 1 est pré-rempli. Le cap par sender N'EST PLUS posé par le
    // preset warmup (faux modèle réputationnel : le cap-classe par infra émettrice
    // couvre la réputation par domaine d'envoi). Le champ reste donc vide.
    const recipInput = screen.getByLabelText(/Emails \/ recipient \/ day/i) as HTMLInputElement
    const senderInput = screen.getByLabelText(/Emails \/ sender \/ day/i) as HTMLInputElement
    await waitFor(() => expect(recipInput.value).toBe('1'))
    expect(senderInput.value).toBe('')

    // 2) Save → persiste les valeurs warmup (caps=1 par classe, per-recipient=1).
    //    Pas de per-sender posé par le preset.
    const saveBtn = screen.getByRole('button', { name: /Save Changes/i })
    await waitFor(() => expect(saveBtn).toBeEnabled())
    await user.click(saveBtn)

    await waitFor(() => expect(workspaceService.update).toHaveBeenCalledTimes(1))
    const arg = vi.mocked(workspaceService.update).mock.calls[0][0]
    expect(arg.settings?.veridian_per_recipient_daily_cap).toBe(1)
    // cap journalier = 1 pour les classes principales (keyé par infra émettrice côté backend).
    expect(arg.settings?.veridian_provider_class_daily_cap?.google).toBe(1)
    expect(arg.settings?.veridian_provider_class_daily_cap?.microsoft).toBe(1)
    // rate bas = 0.5 par classe.
    expect(arg.settings?.veridian_provider_class_rates?.google).toBe(0.5)
  })

  it('warns when a sending integration has fewer than 2 senders (round-robin off)', () => {
    const oneSenderIntegration = {
      id: 'int-1',
      name: 'Cold relay',
      type: 'email',
      email_provider: {
        kind: 'smtp',
        senders: [{ id: 's1', email: 'bot@send.fr', name: 'Bot', is_default: true }],
        rate_limit_per_minute: 60
      },
      created_at: '',
      updated_at: ''
    }
    renderCmp({
      workspace: makeWorkspace({}, [oneSenderIntegration] as never),
      isOwner: true
    })
    expect(screen.getByText(/Round-robin needs at least 2 sending addresses/i)).toBeInTheDocument()
  })

  it('renders the per-sender daily cap field (owner) and persists it', async () => {
    const user = userEvent.setup()
    renderCmp({ workspace: makeWorkspace(), isOwner: true })

    const senderInput = screen.getByLabelText(/Emails \/ sender \/ day/i)
    await user.clear(senderInput)
    await user.type(senderInput, '50')

    const saveBtn = screen.getByRole('button', { name: /Save Changes/i })
    await waitFor(() => expect(saveBtn).toBeEnabled())
    await user.click(saveBtn)

    await waitFor(() => expect(workspaceService.update).toHaveBeenCalledTimes(1))
    const arg = vi.mocked(workspaceService.update).mock.calls[0][0]
    expect(arg.settings?.veridian_per_sender_daily_cap).toBe(50)
  })

  // ── Presets (warm-up / cruise / microsoft prudence) ─────────────────────────

  it('renders the three sending policy presets for owner', () => {
    renderCmp({ workspace: makeWorkspace(), isOwner: true })
    expect(screen.getByText('Sending policy presets')).toBeInTheDocument()
    expect(screen.getByText('Warm-up (cautious)')).toBeInTheDocument()
    expect(screen.getByText('Cruise')).toBeInTheDocument()
    expect(screen.getByText('Microsoft prudence')).toBeInTheDocument()
  })

  it('does not render presets for non-owner', () => {
    renderCmp({ workspace: makeWorkspace(), isOwner: false })
    expect(screen.queryByText('Sending policy presets')).not.toBeInTheDocument()
  })

  it('applying the Cruise preset fills caps/rates without auto-saving, then Save persists', async () => {
    const user = userEvent.setup()
    renderCmp({ workspace: makeWorkspace(), isOwner: true })

    // Clic « Apply » sur la carte Cruise → Popconfirm → Apply.
    await user.click(screen.getByRole('button', { name: /Apply Cruise preset/i }))
    await user.click(await screen.findByRole('button', { name: /^Apply$/i }))
    expect(workspaceService.update).not.toHaveBeenCalled()

    const saveBtn = screen.getByRole('button', { name: /Save Changes/i })
    await waitFor(() => expect(saveBtn).toBeEnabled())
    await user.click(saveBtn)

    await waitFor(() => expect(workspaceService.update).toHaveBeenCalledTimes(1))
    const arg = vi.mocked(workspaceService.update).mock.calls[0][0]
    // Cruise : Microsoft réactivé à débit modéré (2/min), corporate 5/min.
    expect(arg.settings?.veridian_provider_class_rates?.microsoft).toBe(2)
    expect(arg.settings?.veridian_provider_class_rates?.corporate).toBe(5)
    expect(arg.settings?.veridian_provider_class_daily_cap?.microsoft).toBe(200)
  })

  // ── Jitter & anti-hash (workspace) ──────────────────────────────────────────

  it('renders the anti-detection card and saves an explicit jitter override (owner)', async () => {
    const user = userEvent.setup()
    renderCmp({ workspace: makeWorkspace(), isOwner: true })

    expect(screen.getByText('Anti-detection (jitter & content dedup)')).toBeInTheDocument()

    // Active l'override jitter (switch) → amplitude par défaut 0.30 posée.
    const jitterSwitch = screen.getByLabelText('Override jitter workspace')
    await user.click(jitterSwitch)
    await user.click(screen.getByRole('button', { name: /Save anti-detection settings/i }))

    await waitFor(() => expect(workspaceService.update).toHaveBeenCalledTimes(1))
    const arg = vi.mocked(workspaceService.update).mock.calls[0][0]
    expect(arg.settings?.veridian_jitter_pct).toBe(0.3)
  })

  it('saves anti-hash forced OFF as an explicit false (not omitted) (owner)', async () => {
    const user = userEvent.setup()
    renderCmp({ workspace: makeWorkspace(), isOwner: true })

    // Le Select anti-hash workspace → "Forced OFF". antd s'ouvre au mousedown ;
    // l'option du popup porte la classe ant-select-item-option-content. On ouvre
    // via la combobox (input) puis on clique l'option du popup.
    const select = screen.getByRole('combobox', { name: 'Anti-hash mode workspace' })
    await user.click(select)
    await waitFor(() => {
      const opts = document.querySelectorAll('.ant-select-item-option-content')
      expect(Array.from(opts).some((o) => o.textContent === 'Forced OFF')).toBe(true)
    })
    const offOption = Array.from(
      document.querySelectorAll('.ant-select-item-option-content')
    ).find((o) => o.textContent === 'Forced OFF') as HTMLElement
    await user.click(offOption)
    await user.click(screen.getByRole('button', { name: /Save anti-detection settings/i }))

    await waitFor(() => expect(workspaceService.update).toHaveBeenCalledTimes(1))
    const arg = vi.mocked(workspaceService.update).mock.calls[0][0]
    // OFF explicite = false ENVOYÉ (≠ undefined qui retomberait sur le défaut cold ON).
    expect(arg.settings?.veridian_anti_hash_enabled).toBe(false)
  })

  it('omits jitter/anti-hash when left on inherit/default (owner)', async () => {
    const user = userEvent.setup()
    renderCmp({ workspace: makeWorkspace(), isOwner: true })
    // Sauver sans toucher aux contrôles → tri-état "non configuré" = undefined.
    await user.click(screen.getByRole('button', { name: /Save anti-detection settings/i }))
    await waitFor(() => expect(workspaceService.update).toHaveBeenCalledTimes(1))
    const arg = vi.mocked(workspaceService.update).mock.calls[0][0]
    expect(arg.settings?.veridian_jitter_pct).toBeUndefined()
    expect(arg.settings?.veridian_anti_hash_enabled).toBeUndefined()
  })

  it('renders anti-detection read-only with persisted values for non-owner', () => {
    renderCmp({
      workspace: makeWorkspace({
        veridian_jitter_pct: 0.5,
        veridian_anti_hash_enabled: false
      }),
      isOwner: false
    })
    expect(screen.getByText('Anti-detection (jitter & content dedup)')).toBeInTheDocument()
    // jitter 0.5 → "±50%"
    expect(screen.getByText(/±50%/)).toBeInTheDocument()
  })

  // ── Sending window PER INFRA + round-robin badge ────────────────────────────

  it('shows a round-robin badge per infra based on sender count', () => {
    const twoSenders = {
      id: 'email-2',
      name: 'Multi relay',
      type: 'email' as const,
      email_provider: {
        kind: 'smtp' as const,
        senders: [
          { id: 's1', email: 'a@send.fr', name: 'A', is_default: true },
          { id: 's2', email: 'b@send.fr', name: 'B', is_default: false }
        ],
        rate_limit_per_minute: 30
      },
      created_at: '',
      updated_at: ''
    }
    renderCmp({ workspace: makeWorkspace({}, [twoSenders]), isOwner: true })
    expect(screen.getByText(/2 senders · round-robin per class/i)).toBeInTheDocument()
  })

  it('saves a per-infra sending window on the email provider, preserving senders (owner)', async () => {
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

    // Active la fenêtre d'envoi DE L'INFRA. Il y a deux switches "Enable sending
    // window" (la carte workspace + l'éditeur infra). On SCOPE sur la carte
    // infra (titre unique) pour cibler le bon switch sans dépendre de l'ordre DOM.
    const infraCard = screen
      .getByText('Per-infrastructure limits (warm-up)')
      .closest('.ant-card') as HTMLElement
    const infraSwitch = infraCard.querySelector(
      'button[aria-label="Enable sending window"]'
    ) as HTMLElement
    await user.click(infraSwitch)

    await user.click(screen.getByRole('button', { name: /Save Cold relay limits/i }))

    await waitFor(() => expect(workspaceService.updateIntegration).toHaveBeenCalledTimes(1))
    const arg = vi.mocked(workspaceService.updateIntegration).mock.calls[0][0]
    expect(arg.provider?.veridian_sending_window).toBeDefined()
    expect(arg.provider?.veridian_sending_window?.start_hour).toBe(9)
    expect(arg.provider?.veridian_sending_window?.end_hour).toBe(18)
    // provider COMPLET conservé
    expect(arg.provider?.senders).toHaveLength(1)
    expect(arg.provider?.rate_limit_per_minute).toBe(25)
  })

  it('saves a per-infra jitter override on the email provider (owner)', async () => {
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

    // Le contrôle jitter de l'infra est suffixé par le nom de l'intégration.
    await user.click(screen.getByLabelText('Override jitter Cold relay'))
    await user.click(screen.getByRole('button', { name: /Save Cold relay limits/i }))

    await waitFor(() => expect(workspaceService.updateIntegration).toHaveBeenCalledTimes(1))
    const arg = vi.mocked(workspaceService.updateIntegration).mock.calls[0][0]
    expect(arg.provider?.veridian_jitter_pct).toBe(0.3)
    expect(arg.provider?.senders).toHaveLength(1)
  })

  // ── Pixel d'ouverture PAR INFRA (override tri-état, 2026-06-17) ──────────────

  it('saves a per-infra open-pixel override on the email provider, preserving senders (owner)', async () => {
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

    // Force le pixel OFF sur Google pour CETTE infra (tri-état Select). On ouvre
    // la combobox par son aria-label puis on clique l'option "Off" du popup.
    const select = screen.getByRole('combobox', { name: 'pixel google Cold relay' })
    await user.click(select)
    await waitFor(() => {
      const opts = document.querySelectorAll('.ant-select-item-option-content')
      expect(Array.from(opts).some((o) => o.textContent === 'Off')).toBe(true)
    })
    const offOption = Array.from(
      document.querySelectorAll('.ant-select-item-option-content')
    ).find((o) => o.textContent === 'Off') as HTMLElement
    await user.click(offOption)

    await user.click(screen.getByRole('button', { name: /Save Cold relay limits/i }))

    await waitFor(() => expect(workspaceService.updateIntegration).toHaveBeenCalledTimes(1))
    const arg = vi.mocked(workspaceService.updateIntegration).mock.calls[0][0]
    // Override infra explicite : google=false POSÉ sur l'EmailProvider.
    expect(arg.provider?.veridian_open_pixel_by_class?.google).toBe(false)
    // Les classes laissées en "Inherit" ne posent AUCUNE clé (cascade workspace).
    expect('microsoft' in (arg.provider?.veridian_open_pixel_by_class ?? {})).toBe(false)
    // provider COMPLET conservé : senders + rate_limit
    expect(arg.provider?.senders).toHaveLength(1)
    expect(arg.provider?.rate_limit_per_minute).toBe(25)
  })

  it('renders a persisted per-infra pixel override and omits no key when left on inherit (owner)', async () => {
    const user = userEvent.setup()
    const emailIntegration = {
      id: 'email-1',
      name: 'Cold relay',
      type: 'email' as const,
      email_provider: {
        kind: 'smtp' as const,
        senders: [{ id: 's1', email: 'hello@agences-veridian.fr', name: 'Veridian', is_default: true }],
        rate_limit_per_minute: 25,
        // Pixel infra déjà posé : google forcé ON (override de l'infra).
        veridian_open_pixel_by_class: { google: true } as never
      },
      created_at: '',
      updated_at: ''
    }
    renderCmp({ workspace: makeWorkspace({}, [emailIntegration]), isOwner: true })

    // La valeur persistée s'affiche dans le Select (On pour google).
    const select = screen.getByRole('combobox', { name: 'pixel google Cold relay' })
    const selectBox = select.closest('.ant-select') as HTMLElement
    expect(selectBox.textContent).toContain('On')

    // Save sans rien toucher → la clé persistée google=true est ré-émise telle quelle.
    await user.click(screen.getByRole('button', { name: /Save Cold relay limits/i }))
    await waitFor(() => expect(workspaceService.updateIntegration).toHaveBeenCalledTimes(1))
    const arg = vi.mocked(workspaceService.updateIntegration).mock.calls[0][0]
    expect(arg.provider?.veridian_open_pixel_by_class?.google).toBe(true)
  })

  // ── Structure ergonomique : scopes + groupes thématiques ────────────────────

  it('renders the two scope banners and the thematic group sections (owner)', () => {
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
    // Les deux niveaux de scope sont visibles.
    expect(screen.getByText('Workspace defaults')).toBeInTheDocument()
    expect(screen.getByText('Per-infrastructure overrides')).toBeInTheDocument()
    // Les panneaux thématiques (Collapse) sont rendus (forceRender → DOM-present).
    expect(screen.getByText('Rate & volume')).toBeInTheDocument()
    expect(screen.getByText('Reputation')).toBeInTheDocument()
    expect(screen.getByText('Schedule')).toBeInTheDocument()
    expect(screen.getByText('Sending infrastructures')).toBeInTheDocument()
  })
})
