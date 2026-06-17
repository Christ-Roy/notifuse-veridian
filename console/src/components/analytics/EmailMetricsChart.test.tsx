import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, waitFor, fireEvent } from '@testing-library/react'
import { i18n } from '@lingui/core'
import { I18nProvider } from '@lingui/react'
import { App } from 'antd'
import { EmailMetricsChart } from './EmailMetricsChart'
import { analyticsService } from '../../services/api/analytics'
import { replyStatsApi } from '../../services/api/veridian_reply_stats'
import { ApiError } from '../../services/api/client'
import type { Workspace } from '../../services/api/types'

i18n.loadAndActivate({ locale: 'en', messages: {} })

// Le chart sous-jacent (Recharts) n'a pas de besoin d'être réellement rendu pour
// ces tests d'états (loading/error/data) — on le neutralise pour éviter le bruit
// ResizeObserver/canvas en jsdom.
vi.mock('./ChartVisualization', () => ({
  ChartVisualization: () => <div data-testid="chart-viz" />
}))

vi.mock('../../services/api/analytics', () => ({
  analyticsService: { query: vi.fn() }
}))

vi.mock('../../services/api/veridian_reply_stats', () => ({
  replyStatsApi: { get: vi.fn() }
}))

const okResponse = {
  data: [],
  meta: { total: 0, query: '', params: [] }
}

const workspace = {
  id: 'ws-test',
  name: 'Test',
  settings: { timezone: 'UTC' }
} as unknown as Workspace

const renderChart = () =>
  render(
    <I18nProvider i18n={i18n}>
      <App>
        <EmailMetricsChart workspace={workspace} timeRange={['2024-01-01', '2024-12-31']} />
      </App>
    </I18nProvider>
  )

describe('EmailMetricsChart', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    ;(replyStatsApi.get as ReturnType<typeof vi.fn>).mockResolvedValue({ replied: 0 })
  })

  it('renders metrics (Sent card) when the analytics query succeeds', async () => {
    ;(analyticsService.query as ReturnType<typeof vi.fn>).mockResolvedValue(okResponse)
    renderChart()

    await waitFor(() => {
      // La carte "Sent" doit apparaître quand les données chargent sans erreur.
      expect(screen.getByText('Sent')).toBeInTheDocument()
    })
    // Pas d'état d'erreur affiché en chemin nominal.
    expect(screen.queryByText('Unable to load email metrics')).toBeNull()
  })

  it('shows a HUMAN-READABLE error (never the raw value) + retry on 500', async () => {
    // Reproduit le bug P0 : le backend analytics renvoie {error:true, message:"..."}.
    // Le client API doit extraire le message lisible, jamais afficher "true".
    ;(analyticsService.query as ReturnType<typeof vi.fn>).mockRejectedValue(
      new ApiError('Query failed: pq: column "bounce_type" does not exist', 500, {
        error: true,
        message: 'Query failed: pq: column "bounce_type" does not exist'
      })
    )
    renderChart()

    await waitFor(() => {
      expect(screen.getByText('Unable to load email metrics')).toBeInTheDocument()
    })
    // Le détail technique doit être le message réel, JAMAIS le booléen "true".
    expect(
      screen.getByText(/column "bounce_type" does not exist/)
    ).toBeInTheDocument()
    expect(screen.queryByText(/^true$/)).toBeNull()
    // Un bouton Retry doit être proposé.
    expect(screen.getByRole('button', { name: 'Retry' })).toBeInTheDocument()
  })

  it('retry re-issues the analytics query and recovers when it succeeds', async () => {
    const queryMock = analyticsService.query as ReturnType<typeof vi.fn>
    queryMock.mockRejectedValue(new ApiError('boom', 500, { error: true, message: 'boom' }))
    renderChart()

    await waitFor(() => {
      expect(screen.getByRole('button', { name: 'Retry' })).toBeInTheDocument()
    })
    const callsBefore = queryMock.mock.calls.length

    // Le retry réussit cette fois.
    queryMock.mockResolvedValue(okResponse)
    fireEvent.click(screen.getByRole('button', { name: 'Retry' }))

    await waitFor(() => {
      expect(screen.getByText('Sent')).toBeInTheDocument()
    })
    expect(queryMock.mock.calls.length).toBeGreaterThan(callsBefore)
    expect(screen.queryByText('Unable to load email metrics')).toBeNull()
  })
})
