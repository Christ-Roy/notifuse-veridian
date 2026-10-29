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
    // Tracking domain + infra limits cards affichent toutes deux ce hint.
    expect(
      screen.getAllByText(/No sending integration configured yet/i).length
    ).toBeGreaterThan(0)
  })

  // ── Sending window (business hours) ─────────────────────────────────────────

  it('renders the sending window card disabled (24/7) when none is configured', () => {
    renderCmp({ workspace: makeWorkspace(), isOwner: true })
    expect(screen.getByText('Sending window (business hours)')).toBeInTheDocument()
    // Pas de fenêtre → tag "24/7 (no window)"
    expect(screen.getByText('24/7 (no window)')).toBeInTheDocument()
  })

  it('persists a sending window with the Go-shaped payload when enabled (owner)', async () => {
    const user = userEvent.setup()
    renderCmp({ workspace: makeWorkspace(), isOwner: true })

    // Le switch enable de la carte, ciblé par son aria-label dédié (la carte
    // IMAP a aussi un switch TLS — on ne cible donc PAS par index).
    const enableSwitch = screen.getByLabelText('Enable sending window')
    await user.click(enableSwitch)

    // Une fois activé, les contrôles de plage horaire apparaissent. On attend
    // "Opening time" (label unique, non dupliqué par un aria-label de Select).
    await waitFor(() => expect(screen.getByText('Opening time')).toBeInTheDocument())

    // Re-query le bouton APRÈS le re-render (la carte s'est étendue avec les
    // contrôles, l'ancienne ref pourrait être détachée).
    await user.click(screen.getByRole('button', { name: /Save sending window/i }))

    await waitFor(() => expect(workspaceService.update).toHaveBeenCalledTimes(1))
    const arg = vi.mocked(workspaceService.update).mock.calls[0][0]
    const win = arg.settings?.veridian_sending_window
    expect(win).toBeDefined()
    // Défauts : lun-ven (1-5), 9h-18h.
    expect(win?.days).toEqual([1, 2, 3, 4, 5])
    expect(win?.start_hour).toBe(9)
    expect(win?.end_hour).toBe(18)
    expect(win?.timezone).toBeTruthy()
  })

  it('removes the sending window (24/7) when disabled and saved (owner)', async () => {
    const user = userEvent.setup()
    renderCmp({
      workspace: makeWorkspace({
        veridian_sending_window: {
          days: [1, 2, 3, 4, 5],
          start_hour: 9,
          end_hour: 18,
          timezone: 'Europe/Paris'
        }
      }),
      isOwner: true
    })

    // Fenêtre active → tag "Active" + toggle "On". On désactive.
    expect(screen.getByText('Active')).toBeInTheDocument()
    const enableSwitch = screen.getByLabelText('Enable sending window')
    await user.click(enableSwitch)

    await user.click(screen.getByRole('button', { name: /Save sending window/i }))

    await waitFor(() => expect(workspaceService.update).toHaveBeenCalledTimes(1))
    const arg = vi.mocked(workspaceService.update).mock.calls[0][0]
    // Désactivé → undefined (pas de fenêtre = 24/7 côté backend).
    expect(arg.settings?.veridian_sending_window).toBeUndefined()
  })
})
