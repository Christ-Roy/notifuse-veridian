import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, waitFor } from '@testing-library/react'
import { i18n } from '@lingui/core'
import { I18nProvider } from '@lingui/react'
import { App } from 'antd'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { AnalyticsDashboard } from './AnalyticsDashboard'
import { analyticsService } from '../../services/api/analytics'
import type { Workspace } from '../../services/api/types'

i18n.loadAndActivate({ locale: 'en', messages: {} })

vi.mock('@tanstack/react-router', () => ({ useNavigate: () => vi.fn() }))
vi.mock('./EmailMetricsChart', () => ({ EmailMetricsChart: () => <div data-testid="email-chart" /> }))
vi.mock('./veridian_engagement_by_class', () => ({ VeridianEngagementByClass: () => <div /> }))
vi.mock('./FailedMessagesTable', () => ({ FailedMessagesTable: () => <div /> }))
vi.mock('./NewContactsTable', () => ({ NewContactsTable: () => <div /> }))
vi.mock('../../services/api/analytics', () => ({ analyticsService: { query: vi.fn() } }))

const workspace = { id: 'ws-test', name: 'Test', settings: {}, integrations: [] } as unknown as Workspace

const renderDash = () => {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  return render(
    <QueryClientProvider client={client}>
      <I18nProvider i18n={i18n}>
        <App>
          <AnalyticsDashboard workspace={workspace} timeRange={['2024-01-01', '2024-12-31']} />
        </App>
      </I18nProvider>
    </QueryClientProvider>
  )
}

describe('AnalyticsDashboard', () => {
  beforeEach(() => {
    vi.clearAllMocks()
  })

  it('labels contact counts as imported stock and shows the counts', async () => {
    ;(analyticsService.query as ReturnType<typeof vi.fn>).mockResolvedValue({
      data: [{ count: 1234 }],
      meta: { total: 1, query: '', params: [] }
    })
    renderDash()
    expect(screen.getByText('Imported contacts (stock)')).toBeInTheDocument()
    expect(screen.getByText('New imported contacts')).toBeInTheDocument()
    await waitFor(() => expect(screen.getAllByText('1,234').length).toBeGreaterThan(0))
    expect(screen.queryByText('Total Contacts')).not.toBeInTheDocument()
  })

  it('still renders the dashboard when the contact queries fail', async () => {
    ;(analyticsService.query as ReturnType<typeof vi.fn>).mockRejectedValue(new Error('boom'))
    renderDash()
    expect(screen.getByTestId('email-chart')).toBeInTheDocument()
    expect(screen.getByText('Imported contacts (stock)')).toBeInTheDocument()
  })
})
