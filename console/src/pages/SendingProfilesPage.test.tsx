import { beforeEach, describe, expect, it, vi } from 'vitest'
import { render, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { i18n } from '@lingui/core'
import { I18nProvider } from '@lingui/react'
import { App as AntApp } from 'antd'

import type { Workspace } from '../services/api/types'
import {
  overviewOf,
  planClass,
  profile
} from '../components/sending_profiles/veridian_profile_test_fixtures'

i18n.loadAndActivate({ locale: 'en', messages: {} })

const user = userEvent.setup({ delay: null })
vi.setConfig({ testTimeout: 30000 })

vi.mock('@tanstack/react-router', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@tanstack/react-router')>()
  return { ...actual, useParams: () => ({ workspaceId: 'ws-1' }) }
})
vi.mock('../contexts/AuthContext', () => ({
  useAuth: () => ({ user: { id: 'u-owner', email: 'robert.brunon@veridian.site' } })
}))
vi.mock('../services/api/veridian_email_profiles', async () => {
  const actual = await vi.importActual<typeof import('../services/api/veridian_email_profiles')>(
    '../services/api/veridian_email_profiles'
  )
  return {
    ...actual,
    emailProfilesOverviewService: { get: vi.fn() },
    emailProfilesCreateService: { create: vi.fn() }
  }
})
vi.mock('../services/api/workspace', async () => {
  const actual = await vi.importActual<typeof import('../services/api/workspace')>('../services/api/workspace')
  return {
    ...actual,
    workspaceService: {
      get: vi.fn(),
      getMembers: vi.fn(),
      update: vi.fn(),
      updateIntegration: vi.fn(),
      createIntegration: vi.fn(),
      deleteIntegration: vi.fn()
    }
  }
})
vi.mock('../components/sending_profiles/veridian_profile_ops', async () => {
  const actual = await vi.importActual<typeof import('../components/sending_profiles/veridian_profile_ops')>(
    '../components/sending_profiles/veridian_profile_ops'
  )
  return {
    ...actual,
    setPaused: vi.fn().mockResolvedValue(undefined),
    setRotation: vi.fn().mockResolvedValue(undefined),
    deleteProfile: vi.fn().mockResolvedValue(undefined),
    testProfile: vi.fn().mockResolvedValue({ success: true })
  }
})

import { emailProfilesOverviewService } from '../services/api/veridian_email_profiles'
import { workspaceService } from '../services/api/workspace'
import { deleteProfile, setPaused, setRotation, testProfile } from '../components/sending_profiles/veridian_profile_ops'
import { SendingProfilesPage } from './SendingProfilesPage'

const workspace = {
  id: 'ws-1',
  name: 'Robert Brunon',
  created_at: '',
  updated_at: '',
  settings: { timezone: 'UTC', email_tracking_enabled: false, default_language: 'fr', languages: ['fr'] },
  integrations: []
} as unknown as Workspace

const overview = overviewOf(
  [
    profile('nord', { name: 'nord-propre-1' }, { sent_today: 149, reserved_today: 149, daily_cap_today: 300 }),
    profile('relais', { name: 'relais-agence-2' }, {
      sent_today: 138,
      reserved_today: 138,
      daily_cap_today: 200,
      limiting_gate: 'profile_cap',
      warmup: { active: false },
      classes: [planClass('security_gateway', { slowdown_factor: 2, slowdown_reason: 'hard_bounce_rate', slowdown_rate: 0.099 })]
    }),
    profile('transac', { name: 'transactionnel-asd', usage: 'transactional', in_rotation: false }, {
      applicable: false,
      mode: 'transactional',
      sent_today: 4,
      daily_cap_today: null
    })
  ],
  {
    global_inboxes: [
      { integration_id: 'g1', name: 'Return inbox', host: 'imap.larksuite.com', address: 'robert.brunon@veridian.site', folder: 'INBOX', linked_profiles: [] }
    ],
    totals: {
      commercial_sent_today: 287,
      transactional_sent_today: 4,
      commercial_capacity_today: 500,
      active_commercial_profiles: 2,
      paused_profiles: 1
    }
  }
)

function renderPage() {
  return render(
    <I18nProvider i18n={i18n}>
      <AntApp>
        <SendingProfilesPage />
      </AntApp>
    </I18nProvider>
  )
}

describe('page Profils d\'envoi', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    vi.mocked(emailProfilesOverviewService.get).mockResolvedValue(overview)
    vi.mocked(workspaceService.get).mockResolvedValue({ workspace } as never)
    vi.mocked(workspaceService.getMembers).mockResolvedValue({
      members: [{ user_id: 'u-owner', role: 'owner' }]
    } as never)
  })

  it('lit emailProfiles.overview et affiche exactement ses chiffres', async () => {
    renderPage()
    expect(await screen.findByText('nord-propre-1')).toBeInTheDocument()
    expect(emailProfilesOverviewService.get).toHaveBeenCalledWith('ws-1')

    const totals = screen.getByTestId('totals')
    expect(totals.textContent).toContain('287')
    expect(totals.textContent).toContain('500')
    expect(totals.textContent).toContain('Paused profiles1')

    const nord = screen.getByTestId('profile-card-nord')
    expect(within(nord).getByText('149 / 300 sent')).toBeInTheDocument()
    const relais = screen.getByTestId('profile-card-relais')
    expect(within(relais).getByText('138 / 200 sent')).toBeInTheDocument()
    expect(within(relais).getByText('Anti-spam gateways: rate ÷2, 9.9% bounces')).toBeInTheDocument()
  })

  it('sépare Commercial et Transactionnel, et met les boîtes globales dans un encart', async () => {
    renderPage()
    await screen.findByText('nord-propre-1')
    const commercial = screen.getByTestId('section-commercial')
    const transactional = screen.getByTestId('section-transactional')
    expect(within(commercial).getByText('nord-propre-1')).toBeInTheDocument()
    expect(within(commercial).queryByText('transactionnel-asd')).not.toBeInTheDocument()
    expect(within(transactional).getByText('transactionnel-asd')).toBeInTheDocument()
    expect(within(transactional).queryByText('nord-propre-1')).not.toBeInTheDocument()

    const inboxes = screen.getByTestId('global-inboxes')
    expect(within(inboxes).getByText('robert.brunon@veridian.site')).toBeInTheDocument()
    expect(inboxes.textContent).toContain('imap.larksuite.com')
  })

  it('signale une violation d\'exclusivité', async () => {
    vi.mocked(emailProfilesOverviewService.get).mockResolvedValue({ ...overview, usage_conflicts: ['relais'] })
    renderPage()
    expect(await screen.findByText('Some profiles are both in the commercial rotation and transactional')).toBeInTheDocument()
  })

  it('Mettre en pause appelle l\'opération puis relit l\'overview', async () => {
    renderPage()
    const card = await screen.findByTestId('profile-card-nord')
    await user.click(within(card).getByRole('button', { name: 'Pause' }))
    const confirm = await screen.findAllByRole('button', { name: 'Pause' })
    await user.click(confirm[confirm.length - 1])
    await waitFor(() => expect(setPaused).toHaveBeenCalledWith('ws-1', 'nord', true))
    await waitFor(() => expect(emailProfilesOverviewService.get).toHaveBeenCalledTimes(2))
  })

  it('Tester envoie vers l\'adresse du propriétaire', async () => {
    renderPage()
    const card = await screen.findByTestId('profile-card-relais')
    await user.click(within(card).getByRole('button', { name: 'Test' }))
    await waitFor(() => expect(testProfile).toHaveBeenCalledWith('ws-1', 'relais', 'robert.brunon@veridian.site'))
  })

  it('Retirer de la rotation et Supprimer passent par les opérations', async () => {
    renderPage()
    const card = await screen.findByTestId('profile-card-relais')
    await user.click(within(card).getByRole('button', { name: 'Remove from rotation' }))
    await waitFor(() => expect(setRotation).toHaveBeenCalledWith('ws-1', 'relais', false))

    await user.click(within(card).getByRole('button', { name: 'Delete' }))
    const confirm = await screen.findAllByRole('button', { name: 'Delete' })
    await user.click(confirm[confirm.length - 1])
    await waitFor(() => expect(deleteProfile).toHaveBeenCalledWith('ws-1', 'relais'))
  })

  it('le bouton Ajouter un profil ouvre l\'assistant', async () => {
    renderPage()
    await screen.findByText('nord-propre-1')
    await user.click(screen.getAllByRole('button', { name: /Add a profile/ })[0])
    expect(await screen.findByText('Add a sending profile')).toBeInTheDocument()
    expect(screen.getByTestId('choice-gmail-app-password')).toBeInTheDocument()
  })

  it('un membre qui n\'est pas propriétaire ne peut rien modifier', async () => {
    vi.mocked(workspaceService.getMembers).mockResolvedValue({
      members: [{ user_id: 'u-owner', role: 'member' }]
    } as never)
    renderPage()
    await screen.findByText('nord-propre-1')
    await waitFor(() => expect(screen.queryByRole('button', { name: /Add a profile/ })).not.toBeInTheDocument())
    expect(screen.queryByRole('button', { name: 'Edit' })).not.toBeInTheDocument()
  })

  it('workspace sans profil: invite à en ajouter un', async () => {
    vi.mocked(emailProfilesOverviewService.get).mockResolvedValue(
      overviewOf([], { totals: { commercial_sent_today: 0, transactional_sent_today: 0, commercial_capacity_today: null, active_commercial_profiles: 0, paused_profiles: 0 } })
    )
    renderPage()
    expect(await screen.findByText('No sending profile yet')).toBeInTheDocument()
  })

  it('erreur de chargement: message, pas de chiffre inventé', async () => {
    vi.mocked(emailProfilesOverviewService.get).mockRejectedValue(new Error('boom'))
    renderPage()
    expect(await screen.findByText('boom')).toBeInTheDocument()
    expect(screen.queryByTestId('totals')).not.toBeInTheDocument()
  })
})
