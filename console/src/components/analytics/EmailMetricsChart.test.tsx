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
  ChartVisualization: ({ data }: { data: { data: unknown[] } | null }) => (
    <div data-testid="chart-viz" data-rows={JSON.stringify(data?.data ?? [])} />
  )
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

type Q = {
  measures: string[]
  timeDimensions?: { dimension: string; dateRange?: [string, string] }[]
}

// Simule le moteur analytics : une ligne par jour pour la dimension temporelle
// demandee, avec les mesures demandees. `byDimension` donne, par dimension, les
// lignes {day, ...mesures} que la base renverrait.
const engine =
  (byDimension: Record<string, Array<Record<string, unknown>>>) => async (q: Q) => {
    const dim = q.timeDimensions?.[0]?.dimension ?? ''
    const rows = (byDimension[dim] ?? []).map((r) => {
      const out: Record<string, unknown> = { [`${dim}_day`]: r.day }
      for (const m of q.measures) out[m] = r[m] ?? 0
      return out
    })
    return { data: rows, meta: { total: rows.length, query: '', params: [] } }
  }

// Une seule ligne de totaux (sent_at, failed_at) : suffit aux tests de cartes.
const totals = (v: Record<string, number>) =>
  engine({
    sent_at: [{ day: '2024-06-01T00:00:00Z', count_sent: v.count_sent ?? 0 }],
    failed_at: [
      {
        day: '2024-06-01T00:00:00Z',
        count_failed: v.count_failed ?? 0,
        count_failed_excluded: v.count_failed_excluded ?? 0
      }
    ]
  })

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
  it('drops Delivered/Opens/Clicks cards and shows the untracked note', async () => {
    ;(analyticsService.query as ReturnType<typeof vi.fn>).mockResolvedValue(okResponse)
    renderChart()
    await waitFor(() => expect(screen.getByText('Sent')).toBeInTheDocument())
    expect(screen.queryByText('Delivered')).toBeNull()
    expect(screen.queryByText('Opens')).toBeNull()
    expect(screen.queryByText('Clicks')).toBeNull()
    expect(
      screen.getByText('Opens and clicks are not tracked: plain-text emails, no pixel or tracked link')
    ).toBeInTheDocument()
  })

  it('uses replied_human for the rate and shows automatic replies aside', async () => {
    ;(replyStatsApi.get as ReturnType<typeof vi.fn>).mockResolvedValue({
      replied: 10,
      replied_human: 4
    })
    ;(analyticsService.query as ReturnType<typeof vi.fn>).mockImplementation(
      totals({ count_sent: 100 })
    )
    renderChart()
    await waitFor(() => expect(screen.getByText('4.0%')).toBeInTheDocument())
    expect(screen.getByTestId('auto-replies').textContent).toContain('6')
  })

  it('falls back to replied when replied_human is absent, no auto note', async () => {
    ;(replyStatsApi.get as ReturnType<typeof vi.fn>).mockResolvedValue({ replied: 10 })
    ;(analyticsService.query as ReturnType<typeof vi.fn>).mockImplementation(
      totals({ count_sent: 100 })
    )
    renderChart()
    await waitFor(() => expect(screen.getByText('10%')).toBeInTheDocument())
    expect(screen.queryByTestId('auto-replies')).toBeNull()
  })

  it('splits deliberate exclusions from real failures when the measure exists', async () => {
    ;(analyticsService.query as ReturnType<typeof vi.fn>).mockImplementation(
      totals({ count_sent: 1000, count_failed: 171, count_failed_excluded: 170 })
    )
    renderChart()
    await waitFor(() => expect(screen.getByText('Deliberately excluded')).toBeInTheDocument())
    expect(screen.getByText('Real failures')).toBeInTheDocument()
    expect(screen.getByText('170')).toBeInTheDocument()
    expect(screen.getByText('0.1%')).toBeInTheDocument()
  })

  it('keeps a single Failed card when the exclusion measure is unavailable', async () => {
    ;(analyticsService.query as ReturnType<typeof vi.fn>).mockImplementation(async (q: Q) => {
      if (q.measures.length === 1 && q.measures[0] === 'count_failed_excluded') {
        throw new Error('unknown measure')
      }
      return totals({ count_sent: 1000, count_failed: 171 })(q)
    })
    renderChart()
    await waitFor(() => expect(screen.getByText('Failed')).toBeInTheDocument())
    expect(screen.getByText('Deliberately excluded')).toBeInTheDocument()
    expect(screen.queryByText('Real failures')).toBeNull()
  })
  // Regression : la courbe etait groupee sur created_at (date de mise en file).
  // Un message cree le 30/09 et envoye le 06/10 doit compter le 06/10.
  it('plots sends on their sent_at day, not on created_at (queued 30/09, sent 06/10)', async () => {
    const calls: Q[] = []
    ;(analyticsService.query as ReturnType<typeof vi.fn>).mockImplementation(async (q: Q) => {
      calls.push(q)
      return engine({
        // Ce que renverrait l'ANCIEN groupement : tout le volume le jour de creation.
        created_at: [
          { day: '2026-09-30T00:00:00Z', count_sent: 800 },
          { day: '2026-10-06T00:00:00Z', count_sent: 0 }
        ],
        sent_at: [
          { day: '2026-09-30T00:00:00Z', count_sent: 5 },
          { day: '2026-10-05T00:00:00Z', count_sent: 300 },
          { day: '2026-10-06T00:00:00Z', count_sent: 273 }
        ],
        bounced_at: [{ day: '2026-10-06T00:00:00Z', count_bounced: 14 }]
      })(q)
    })
    render(
      <I18nProvider i18n={i18n}>
        <App>
          <EmailMetricsChart workspace={workspace} timeRange={['2026-09-24', '2026-10-08']} />
        </App>
      </I18nProvider>
    )
    await waitFor(() => expect(screen.getByTestId('chart-viz').getAttribute('data-rows')).not.toBe('[]'))
    const rows = JSON.parse(screen.getByTestId('chart-viz').getAttribute('data-rows') as string)
    const byDay = Object.fromEntries(rows.map((r: Record<string, unknown>) => [String(r.event_date_day).slice(0, 10), r]))
    expect(byDay['2026-10-05'].count_sent).toBe(300)
    expect(byDay['2026-10-06'].count_sent).toBe(273)
    expect(byDay['2026-09-30'].count_sent).toBe(5)
    // Les rejets restent sur leur propre date.
    expect(byDay['2026-10-06'].count_bounced).toBe(14)
    // Aucune requete de mesure d'evenement ne groupe sur created_at.
    expect(calls.every((q) => q.timeDimensions?.[0]?.dimension !== 'created_at')).toBe(true)
    expect(calls.find((q) => q.measures.includes('count_sent'))?.timeDimensions?.[0]?.dimension).toBe('sent_at')
    expect(calls.find((q) => q.measures.includes('count_bounced'))?.timeDimensions?.[0]?.dimension).toBe('bounced_at')
    // La carte Envoye = somme des envois de la fenetre (5+300+273).
    await waitFor(() => expect(screen.getByText('578')).toBeInTheDocument())
  })

  it('vue transactionnelle : filtre message_type, aucun appel reply, ni carte Replies ni note texte brut', async () => {
    ;(analyticsService.query as ReturnType<typeof vi.fn>).mockResolvedValue(okResponse)
    render(
      <I18nProvider i18n={i18n}>
        <App>
          <EmailMetricsChart
            workspace={workspace}
            timeRange={['2024-01-01', '2024-12-31']}
            messageType="transactional"
          />
        </App>
      </I18nProvider>
    )
    await waitFor(() => expect(analyticsService.query).toHaveBeenCalled())
    const filters = (analyticsService.query as ReturnType<typeof vi.fn>).mock.calls.map(
      (c) => JSON.stringify(c[0].filters)
    )
    expect(filters.every((f) => f.includes('"message_type"') && f.includes('transactional'))).toBe(true)
    expect(replyStatsApi.get).not.toHaveBeenCalled()
    expect(screen.queryByText('Replies')).not.toBeInTheDocument()
    expect(screen.queryByText(/Opens and clicks are not tracked/)).not.toBeInTheDocument()
  })

  it('vue commerciale : appelle reply et montre la carte Replies', async () => {
    ;(analyticsService.query as ReturnType<typeof vi.fn>).mockResolvedValue(okResponse)
    renderChart()
    await waitFor(() => expect(replyStatsApi.get).toHaveBeenCalled())
    expect(screen.getByText('Replies')).toBeInTheDocument()
  })

  it('pluralises automatic replies (1 reponse / N reponses)', async () => {
    ;(replyStatsApi.get as ReturnType<typeof vi.fn>).mockResolvedValue({ replied: 3, replied_human: 2 })
    ;(analyticsService.query as ReturnType<typeof vi.fn>).mockImplementation(totals({ count_sent: 100 }))
    renderChart()
    await waitFor(() => expect(screen.getByTestId('auto-replies').textContent).toBe('+1 automatic reply'))
  })

  it('pluralises many automatic replies', async () => {
    ;(replyStatsApi.get as ReturnType<typeof vi.fn>).mockResolvedValue({ replied: 6, replied_human: 2 })
    ;(analyticsService.query as ReturnType<typeof vi.fn>).mockImplementation(totals({ count_sent: 100 }))
    renderChart()
    await waitFor(() => expect(screen.getByTestId('auto-replies').textContent).toBe('+4 automatic replies'))
  })
})
