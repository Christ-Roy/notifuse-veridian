import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen } from '@testing-library/react'
import { i18n } from '@lingui/core'
import { I18nProvider } from '@lingui/react'
import { App } from 'antd'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { AnalyticsDashboard } from './AnalyticsDashboard'
import type { Workspace } from '../../services/api/types'

i18n.loadAndActivate({ locale: 'en', messages: {} })

vi.mock('@tanstack/react-router', () => ({ useNavigate: () => vi.fn() }))
vi.mock('./EmailMetricsChart', () => ({ EmailMetricsChart: () => <div data-testid="email-chart" /> }))
vi.mock('../prospection/veridian_prospection_dashboard', () => ({
  VeridianProspectionDashboard: () => <div data-testid="prospection-dashboard" />
}))
vi.mock('./FailedMessagesTable', () => ({ FailedMessagesTable: () => <div /> }))
vi.mock('./NewContactsTable', () => ({ NewContactsTable: () => <div /> }))
vi.mock('../../services/api/veridian_email_profiles', () => ({
  emailProfilesOverviewService: { get: vi.fn().mockResolvedValue({ totals: {}, profiles: [] }) }
}))

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

  it('la vue par défaut est le tableau de bord de prospection (une seule page)', () => {
    renderDash()
    expect(screen.getByTestId('prospection-dashboard')).toBeInTheDocument()
    expect(screen.queryByTestId('cards-transactional')).not.toBeInTheDocument()
  })
})
