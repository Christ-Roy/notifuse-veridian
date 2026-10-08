import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, waitFor } from '@testing-library/react'
import { i18n } from '@lingui/core'
import { I18nProvider } from '@lingui/react'
import { App } from 'antd'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { AnalyticsDashboard } from './AnalyticsDashboard'
import { analyticsService } from '../../services/api/analytics'
import { emailProfilesOverviewService } from '../../services/api/veridian_email_profiles'
import type { Workspace } from '../../services/api/types'

i18n.loadAndActivate({ locale: 'en', messages: {} })

vi.mock('@tanstack/react-router', () => ({ useNavigate: () => vi.fn() }))
vi.mock('./EmailMetricsChart', () => ({
  EmailMetricsChart: (props: { messageType?: string }) => (
    <div data-testid="email-chart" data-type={props.messageType} />
  )
}))
vi.mock('./veridian_engagement_by_class', () => ({
  VeridianEngagementByClass: () => <div data-testid="engagement-by-class" />
}))
vi.mock('./FailedMessagesTable', () => ({ FailedMessagesTable: () => <div data-testid="failed-table" /> }))
vi.mock('./NewContactsTable', () => ({ NewContactsTable: () => <div data-testid="new-contacts-table" /> }))
vi.mock('../../services/api/analytics', () => ({ analyticsService: { query: vi.fn() } }))
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
  ]
}

const renderDash = (messageType?: 'commercial' | 'transactional') => {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  return render(
    <QueryClientProvider client={client}>
      <I18nProvider i18n={i18n}>
        <App>
          <AnalyticsDashboard
            workspace={workspace}
            timeRange={['2024-01-01', '2024-12-31']}
            messageType={messageType}
          />
        </App>
      </I18nProvider>
    </QueryClientProvider>
  )
}

describe('AnalyticsDashboard : vue Commercial | Transactionnel', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    ;(analyticsService.query as ReturnType<typeof vi.fn>).mockResolvedValue({
      data: [{ count: 4321 }],
      meta: { total: 1, query: '', params: [] }
    })
    ;(emailProfilesOverviewService.get as ReturnType<typeof vi.fn>).mockResolvedValue(overview)
  })

  it('vue commerciale par defaut : stock, nouveaux, profils en rotation, envoyes / capacite', async () => {
    renderDash()
    expect(screen.getByTestId('cards-commercial')).toBeInTheDocument()
    expect(screen.getByText('Imported contacts (stock)')).toBeInTheDocument()
    expect(screen.getByText('New imported contacts')).toBeInTheDocument()
    expect(screen.getByText('Profiles in rotation')).toBeInTheDocument()
    await waitFor(() => expect(screen.getByText('120 / 500')).toBeInTheDocument())
    expect(screen.getByText('3')).toBeInTheDocument()
    expect(screen.getByTestId('email-chart')).toHaveAttribute('data-type', 'commercial')
    expect(screen.getByTestId('engagement-by-class')).toBeInTheDocument()
    expect(screen.getByTestId('failed-table')).toBeInTheDocument()
    expect(screen.queryByTestId('cards-transactional')).not.toBeInTheDocument()
  })

  it('vue transactionnelle : profil, envoyes aujourd hui, jamais de plafond ni de contacts', async () => {
    renderDash('transactional')
    expect(screen.getByTestId('cards-transactional')).toBeInTheDocument()
    await waitFor(() => expect(screen.getByText('asd-transactionnel')).toBeInTheDocument())
    expect(screen.getByText('notifications@asd.example')).toBeInTheDocument()
    expect(screen.getByText('37')).toBeInTheDocument()
    expect(screen.getByTestId('transactional-no-cap').textContent).toContain('no cap')
    expect(screen.getByTestId('email-chart')).toHaveAttribute('data-type', 'transactional')
    // rien de commercial
    expect(screen.queryByText('Imported contacts (stock)')).not.toBeInTheDocument()
    expect(screen.queryByText('Profiles in rotation')).not.toBeInTheDocument()
    expect(screen.queryByText(/capacity/i)).not.toBeInTheDocument()
    expect(screen.queryByTestId('engagement-by-class')).not.toBeInTheDocument()
    expect(screen.queryByTestId('failed-table')).not.toBeInTheDocument()
    expect(screen.queryByTestId('new-contacts-table')).not.toBeInTheDocument()
    expect(screen.queryByText('120 / 500')).not.toBeInTheDocument()
  })

  it('vue transactionnelle sans profil transactionnel : Non configure', async () => {
    ;(emailProfilesOverviewService.get as ReturnType<typeof vi.fn>).mockResolvedValue({
      ...overview,
      profiles: [overview.profiles[1]]
    })
    renderDash('transactional')
    await waitFor(() => expect(screen.getByText('Not configured')).toBeInTheDocument())
  })
})
