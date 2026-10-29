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

  // ── Per-infrastructure limits (R2) ──────────────────────────────────────────

  it('saves per-infra rates and caps on the email provider, preserving senders/rate_limit (owner)', async () => {
    const user = userEvent.setup()
    const emailIntegration = {
      id: 'email-1',
      name: 'Cold relay',
      type: 'email' as const,
      email_provider: {
        kind: 'smtp' as const,
        senders: [{ id: 's1', email: 'hello@agences-veridian.fr', name: 'Veridian', is_default: true }],
        rate_limit_per_minute: 25,
        veridian_tracking_domain: 'track.agences-veridian.fr'
      },
      created_at: '',
      updated_at: ''
    }
    renderCmp({ workspace: makeWorkspace({}, [emailIntegration]), isOwner: true })

    expect(screen.getByText('Per-infrastructure limits (warm-up)')).toBeInTheDocument()

    // Pose un rate Google = 1/min sur cette infra (warm-up).
    const googleRate = screen.getByLabelText('rate google Cold relay')
    await user.type(googleRate, '1')
    // Pose un cap journalier Microsoft = 50/jour.
    const msCap = screen.getByLabelText('cap microsoft Cold relay')
    await user.type(msCap, '50')

    await user.click(screen.getByRole('button', { name: /Save Cold relay limits/i }))

    await waitFor(() => expect(workspaceService.updateIntegration).toHaveBeenCalledTimes(1))
    const arg = vi.mocked(workspaceService.updateIntegration).mock.calls[0][0]
    expect(arg.integration_id).toBe('email-1')
    // limites posées par classe
    expect(arg.provider?.veridian_provider_class_rates?.google).toBe(1)
    expect(arg.provider?.veridian_provider_class_daily_cap?.microsoft).toBe(50)
    // provider COMPLET conservé : senders + rate_limit + tracking_domain
    expect(arg.provider?.senders).toHaveLength(1)
    expect(arg.provider?.rate_limit_per_minute).toBe(25)
    expect(arg.provider?.veridian_tracking_domain).toBe('track.agences-veridian.fr')
  })

  it('renders existing per-infra limits and is read-only for non-owner', () => {
    const emailIntegration = {
      id: 'email-1',
      name: 'Cold relay',
      type: 'email' as const,
      email_provider: {
        kind: 'smtp' as const,
        senders: [{ id: 's1', email: 'hello@agences-veridian.fr', name: 'Veridian', is_default: true }],
        rate_limit_per_minute: 25,
        veridian_provider_class_rates: { google: 2 } as never,
        veridian_per_recipient_daily_cap: 3
      },
      created_at: '',
      updated_at: ''
    }
    renderCmp({ workspace: makeWorkspace({}, [emailIntegration]), isOwner: false })
    expect(screen.getByText('Per-infrastructure limits (warm-up)')).toBeInTheDocument()
    // valeur persistée affichée (rate google = 2). InputNumber avec step=0.5
    // formate "2" → "2.0" : on tolère les deux représentations.
    expect((screen.getByLabelText('rate google Cold relay') as HTMLInputElement).value).toMatch(
      /^2(\.0)?$/
    )
    // pas de bouton de save en lecture seule
    expect(screen.queryByRole('button', { name: /Cold relay limits/i })).not.toBeInTheDocument()
  })

  it('shows a hint when no sending integration exists for per-infra limits', () => {
    renderCmp({ workspace: makeWorkspace(), isOwner: true })
    // 2 cartes (tracking domain + infra limits) affichent le même hint → présent.
    expect(screen.getAllByText(/No sending integration configured yet/i).length).toBeGreaterThan(0)
  })

  it('renders the sending window read-only for non-owner with a summary', () => {
    renderCmp({
      workspace: makeWorkspace({
        veridian_sending_window: {
          days: [1, 2, 3, 4, 5],
          start_hour: 9,
          end_hour: 18,
          timezone: 'Europe/Paris'
        }
      }),
      isOwner: false
    })
    expect(screen.getByText('Sending window (business hours)')).toBeInTheDocument()
    // Résumé littéral : jours + plage + timezone (libellés littéraux, pas de t``)
    expect(screen.getByText(/Monday.*09:00.*18:00.*Europe\/Paris/)).toBeInTheDocument()
    // pas de bouton de save en lecture seule
    expect(screen.queryByRole('button', { name: /Save sending window/i })).not.toBeInTheDocument()
  })

  // ── Exclusion de classes de providers (workspace) ───────────────────────────

  it('renders the excluded-classes card with no exclusion by default (owner)', () => {
    renderCmp({ workspace: makeWorkspace(), isOwner: true })
    expect(screen.getByText('Excluded provider classes (do not contact)')).toBeInTheDocument()
    expect(screen.getByText('None excluded')).toBeInTheDocument()
  })

  it('round-trips a persisted workspace exclusion on save (owner)', async () => {
    const user = userEvent.setup()
    renderCmp({
      workspace: makeWorkspace({ veridian_excluded_provider_classes: ['microsoft'] }),
      isOwner: true
    })
    // Tag count visible
    expect(screen.getByText('1 excluded')).toBeInTheDocument()
    // Sauver sans modifier → la liste persistée est renvoyée telle quelle.
    await user.click(screen.getByRole('button', { name: /Save excluded classes/i }))
    await waitFor(() => expect(workspaceService.update).toHaveBeenCalledTimes(1))
    const arg = vi.mocked(workspaceService.update).mock.calls[0][0]
    expect(arg.settings?.veridian_excluded_provider_classes).toEqual(['microsoft'])
  })

  it('omits the exclusion key when the list is cleared and saved (owner)', async () => {
    const user = userEvent.setup()
    renderCmp({
      workspace: makeWorkspace({ veridian_excluded_provider_classes: ['microsoft'] }),
      isOwner: true
    })
    // Scope sur la carte d'exclusion (titre unique) puis vide la sélection via le
    // bouton clear du Select multiple. On ne passe PAS par getByLabelText : antd
    // pose le même aria-label sur la combobox ET l'input de recherche interne.
    const card = screen
      .getByText('Excluded provider classes (do not contact)')
      .closest('.ant-card') as HTMLElement
    const clearBtn = card.querySelector('.ant-select-clear') as HTMLElement
    await user.click(clearBtn)
    await user.click(screen.getByRole('button', { name: /Save excluded classes/i }))
    await waitFor(() => expect(workspaceService.update).toHaveBeenCalledTimes(1))
    const arg = vi.mocked(workspaceService.update).mock.calls[0][0]
    // Liste vide → undefined (pas de clé = aucune exclusion, non-régression).
    expect(arg.settings?.veridian_excluded_provider_classes).toBeUndefined()
  })

  it('warns how many contacts will be skipped for an excluded class (owner)', async () => {
    renderCmp({
      workspace: makeWorkspace({ veridian_excluded_provider_classes: ['microsoft'] }),
      isOwner: true
    })
    // Le breakdown mocké renvoie microsoft: 830 → l'alerte d'impact l'affiche.
    await waitFor(() =>
      expect(screen.getByText(/830 contacts will be skipped/)).toBeInTheDocument()
    )
  })

  it('renders excluded classes read-only for non-owner', () => {
    renderCmp({
      workspace: makeWorkspace({ veridian_excluded_provider_classes: ['microsoft'] }),
      isOwner: false
    })
    expect(screen.getByText('Excluded provider classes (do not contact)')).toBeInTheDocument()
    // Le label Microsoft apparaît dans la carte d'exclusion ET dans la liste
    // read-only des classes plus bas → on tolère plusieurs occurrences, mais au
    // moins une (le tag rouge d'exclusion).
    expect(screen.getAllByText('Microsoft (Outlook / Microsoft 365)').length).toBeGreaterThan(0)
    // pas de bouton de save en lecture seule
    expect(
      screen.queryByRole('button', { name: /Save excluded classes/i })
    ).not.toBeInTheDocument()
  })

  it('saves per-infra excluded classes on the email provider, preserving senders (owner)', async () => {
    const user = userEvent.setup()
    const emailIntegration = {
      id: 'email-1',
      name: 'Cold relay',
      type: 'email' as const,
      email_provider: {
        kind: 'smtp' as const,
        senders: [{ id: 's1', email: 'hello@agences-veridian.fr', name: 'Veridian', is_default: true }],
        rate_limit_per_minute: 25,
        veridian_excluded_provider_classes: ['microsoft'] as never
      },
      created_at: '',
      updated_at: ''
    }
    renderCmp({ workspace: makeWorkspace({}, [emailIntegration]), isOwner: true })
    // Sauver l'infra sans modifier → l'exclusion persistée est renvoyée + senders conservés.
    await user.click(screen.getByRole('button', { name: /Save Cold relay limits/i }))
    await waitFor(() => expect(workspaceService.updateIntegration).toHaveBeenCalledTimes(1))
    const arg = vi.mocked(workspaceService.updateIntegration).mock.calls[0][0]
    expect(arg.provider?.veridian_excluded_provider_classes).toEqual(['microsoft'])
    expect(arg.provider?.senders).toHaveLength(1)
    expect(arg.provider?.rate_limit_per_minute).toBe(25)
  })
})
