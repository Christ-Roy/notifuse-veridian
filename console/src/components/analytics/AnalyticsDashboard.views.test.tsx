import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, waitFor, fireEvent } from '@testing-library/react'
import { i18n } from '@lingui/core'
import { I18nProvider } from '@lingui/react'
import { App } from 'antd'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { AnalyticsDashboard } from './AnalyticsDashboard'
import { emailProfilesOverviewService } from '../../services/api/veridian_email_profiles'
import type { Workspace } from '../../services/api/types'

i18n.loadAndActivate({ locale: 'en', messages: {} })

vi.mock('@tanstack/react-router', () => ({ useNavigate: () => vi.fn() }))
vi.mock('./EmailMetricsChart', () => ({
  EmailMetricsChart: (props: { messageType?: string }) => (
    <div data-testid="email-chart" data-type={props.messageType} />
  )
}))
vi.mock('../prospection/veridian_prospection_dashboard', () => ({
  VeridianProspectionDashboard: (props: { onOpenTransactional?: () => void }) => (
    <div data-testid="prospection-dashboard">
      <button onClick={props.onOpenTransactional}>open-transactional</button>
    </div>
  )
}))
vi.mock('./FailedMessagesTable', () => ({ FailedMessagesTable: () => <div data-testid="failed-table" /> }))
vi.mock('./NewContactsTable', () => ({ NewContactsTable: () => <div data-testid="new-contacts-table" /> }))
vi.mock('../../services/api/veridian_email_profiles', () => ({
  emailProfilesOverviewService: { get: vi.fn() }
}))

const workspace = { id: 'ws-test', name: 'Test', settings: {}, integrations: [] } as unknown as Workspace

const overview = {
  totals: {
    commercial_sent_today: 120,
    transactional_sent_today: 37,
    commercial_capacity_today: 500,
    active_commercial_profiles: 3,
    paused_profiles: 0
  },
  profiles: [
    {
      integration_id: 't1',
      name: 'asd-transactionnel',
      usage: 'transactional',
      senders: [{ email: 'notifications@asd.example', name: 'ASD', is_default: true }]
    },
    { integration_id: 'c1', name: 'commercial-1', usage: 'commercial', senders: [] }
  ],
  transactional_watch: {
    profile_id: 't1',
    profile_name: 'asd-transactionnel',
    level: 'alert',
    blocking: false,
    sent_today: 900,
    baseline_per_day: 10,
    sent_7d: 970,
    hard_bounce_rate_7d: 0.06,
    complaint_rate_7d: 0,
    policy_refusal_rate_7d: 0,
    alerts: [
      { code: 'volume_spike', level: 'alert', value: 900, watch_threshold: 100, alert_threshold: 200, message: 'x' },
      { code: 'hard_bounce_rate', level: 'alert', value: 0.06, watch_threshold: 0.02, alert_threshold: 0.05, message: 'y' }
    ]
  }
}

const renderDash = (messageType?: 'commercial' | 'transactional', onOpenTransactional?: () => void) => {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  return render(
    <QueryClientProvider client={client}>
      <I18nProvider i18n={i18n}>
        <App>
          <AnalyticsDashboard
            workspace={workspace}
            timeRange={['2024-01-01', '2024-12-31']}
            messageType={messageType}
            onOpenTransactional={onOpenTransactional}
          />
        </App>
      </I18nProvider>
    </QueryClientProvider>
  )
}

describe('AnalyticsDashboard : vue Commercial | Transactionnel', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    ;(emailProfilesOverviewService.get as ReturnType<typeof vi.fn>).mockResolvedValue(overview)
  })

  it('vue commerciale par defaut : le tableau de bord de prospection, puis contacts importes et echecs', () => {
    renderDash()
    expect(screen.getByTestId('prospection-dashboard')).toBeInTheDocument()
    expect(screen.getByTestId('new-contacts-table')).toBeInTheDocument()
    expect(screen.getByTestId('failed-table')).toBeInTheDocument()
    expect(screen.queryByTestId('cards-transactional')).not.toBeInTheDocument()
    // la vue commerciale ne lit pas l'overview elle-meme : le tableau de bord de prospection a la sienne
    expect(emailProfilesOverviewService.get).not.toHaveBeenCalled()
  })

  it('le lien du bandeau ouvre la vue transactionnelle', () => {
    const open = vi.fn()
    renderDash('commercial', open)
    fireEvent.click(screen.getByText('open-transactional'))
    expect(open).toHaveBeenCalledTimes(1)
  })

  it('vue transactionnelle : profil, envoyes aujourd hui, surveillance, jamais de plafond ni de contacts', async () => {
    renderDash('transactional')
    expect(screen.getByTestId('cards-transactional')).toBeInTheDocument()
    await waitFor(() => expect(screen.getAllByText('asd-transactionnel').length).toBeGreaterThan(0))
    expect(screen.getByText('notifications@asd.example')).toBeInTheDocument()
    expect(screen.getByText('37')).toBeInTheDocument()
    expect(screen.getByTestId('transactional-no-cap').textContent).toContain('no cap')
    expect(screen.getByTestId('email-chart')).toHaveAttribute('data-type', 'transactional')
    // surveillance du volume et de la reputation : mesuree et jamais bloquante
    const card = await screen.findByTestId('transactional-watch-card')
    expect(card).toHaveTextContent('Alert')
    expect(card).toHaveTextContent('Unusual volume: 900 mails today, 10 per day on average over the previous 7 days')
    expect(card).toHaveTextContent('Hard bounces: 6% over 7 days')
    expect(card).toHaveTextContent('never held back, delayed or refused')
    // rien de commercial
    expect(screen.queryByTestId('prospection-dashboard')).not.toBeInTheDocument()
    expect(screen.queryByTestId('failed-table')).not.toBeInTheDocument()
    expect(screen.queryByTestId('new-contacts-table')).not.toBeInTheDocument()
    expect(screen.queryByText(/capacity/i)).not.toBeInTheDocument()
  })

  it('vue transactionnelle sans profil transactionnel : Non configure, pas de carte de surveillance', async () => {
    ;(emailProfilesOverviewService.get as ReturnType<typeof vi.fn>).mockResolvedValue({
      ...overview,
      profiles: [overview.profiles[1]],
      transactional_watch: null
    })
    renderDash('transactional')
    await waitFor(() => expect(screen.getByText('Not configured')).toBeInTheDocument())
    expect(screen.queryByTestId('transactional-watch-card')).not.toBeInTheDocument()
  })
})
