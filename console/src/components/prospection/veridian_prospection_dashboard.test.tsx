import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, waitFor, within, fireEvent } from '@testing-library/react'
import { i18n } from '@lingui/core'
import { I18nProvider } from '@lingui/react'
import { App } from 'antd'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'

import { VeridianProspectionDashboard } from './veridian_prospection_dashboard'
import { analyticsService } from '../../services/api/analytics'
import { emailProfilesOverviewService } from '../../services/api/veridian_email_profiles'
import type { TransactionalWatch } from '../../services/api/veridian_email_profiles'
import { prospectionStatsService } from '../../services/api/veridian_prospection'
import { replyStatsApi } from '../../services/api/veridian_reply_stats'
import type { ProspectionStats } from '../../services/api/veridian_prospection'
import type { Workspace } from '../../services/api/types'
import { basePlan, overviewOf, planClass, profile } from '../sending_profiles/veridian_profile_test_fixtures'

i18n.loadAndActivate({ locale: 'en', messages: {} })

// echarts n'a pas de canvas en jsdom : on neutralise le rendu, pas les données.
vi.mock('echarts/core', () => ({
  use: vi.fn(),
  init: vi.fn(() => ({ setOption: vi.fn(), resize: vi.fn(), dispose: vi.fn() }))
}))
vi.mock('echarts/charts', () => ({ BarChart: {} }))
vi.mock('echarts/components', () => ({ GridComponent: {}, LegendComponent: {}, TooltipComponent: {} }))
vi.mock('echarts/renderers', () => ({ CanvasRenderer: {} }))

vi.mock('../analytics/EmailMetricsChart', () => ({
  EmailMetricsChart: (props: { messageType?: string }) => <div data-testid="email-chart" data-type={props.messageType} />
}))
vi.mock('../analytics/veridian_engagement_by_class', () => ({
  VeridianEngagementByClass: () => <div data-testid="engagement-by-class" />
}))
vi.mock('../../services/api/analytics', () => ({ analyticsService: { query: vi.fn() } }))
vi.mock('../../services/api/veridian_email_profiles', () => ({ emailProfilesOverviewService: { get: vi.fn() } }))
vi.mock('../../services/api/veridian_prospection', () => ({ prospectionStatsService: { get: vi.fn() } }))
vi.mock('../../services/api/veridian_reply_stats', () => ({ replyStatsApi: { get: vi.fn() } }))

const workspace = {
  id: 'ws-test',
  name: 'Test',
  settings: { timezone: 'Europe/Paris' },
  integrations: []
} as unknown as Workspace

// Chiffres alignés sur un relevé réel de robertbrunon (08/10/2026), arrondis.
const stats: ProspectionStats = {
  generated_at: '2026-10-08T08:00:00Z',
  since: null,
  until: null,
  totals: { stock_remaining: 20000, queued_first_mail: 27000 },
  sequences: [
    {
      automation_id: 'devenir',
      name: 'E-commerce à devenir',
      status: 'live',
      list_id: 'ecomdevenir',
      enrolled: 3590,
      completed: 0,
      failed: 1,
      stages: [
        { stage: 1, label: 'J0', node_ids: ['j0a', 'j0b'], sent: 759, queued: 2773, waiting: 0, exited_after: 16 },
        { stage: 2, label: 'J+4', node_ids: ['j4'], sent: 10, queued: 10, waiting: 733, exited_after: 2 },
        { stage: 3, label: 'J+10', node_ids: ['j10'], sent: 0, queued: 0, waiting: 0, exited_after: 0 }
      ],
      exits: { replied: 16, rejected: 0, unsubscribed: 0, excluded: 83, other: 0, total: 99, before_first_mail: 83 },
      replies_human: 12,
      replies_auto: 1,
      sent_contacts: 240,
      reply_rate_human: 0.05
    }
  ],
  segments: [
    {
      list_id: 'ecomdevenir',
      name: 'E-commerce à devenir',
      sequence_ids: ['devenir'],
      active: 3300,
      bounced: 133,
      unsubscribed: 181,
      complained: 0,
      never_contacted: 2500,
      replies_human: 12,
      replies_auto: 1,
      sent_contacts: 240,
      reply_rate_human: 0.05
    }
  ]
}

const relayA = profile('a', {
  name: 'relais-a',
  plan: basePlan({
    sent_today: 149,
    daily_cap_today: 300,
    classes: [
      planClass('google'),
      planClass('microsoft', { slowdown_factor: 4, slowdown_reason: 'hard_bounce_rate', slowdown_rate: 0.099, sent_7d: 40 })
    ]
  })
})
const baseOverview = overviewOf([relayA, profile('b', { name: 'relais-b' })], {
  totals: {
    commercial_sent_today: 319,
    transactional_sent_today: 4,
    commercial_capacity_today: 600,
    active_commercial_profiles: 2,
    paused_profiles: 0
  }
})

const watch = (level: TransactionalWatch['level']): TransactionalWatch => ({
  profile_id: 'tx',
  profile_name: 'asd-transactionnel',
  level,
  blocking: false,
  sent_today: 900,
  baseline_per_day: 10,
  sent_7d: 970,
  hard_bounce_rate_7d: 0,
  complaint_rate_7d: 0,
  policy_refusal_rate_7d: 0,
  alerts:
    level === 'alert'
      ? [{ code: 'volume_spike', level: 'alert', value: 900, watch_threshold: 100, alert_threshold: 200, message: 'x' }]
      : []
})

type Q = {
  measures: string[]
  dimensions: string[]
  timeDimensions?: { dimension: string; granularity: string; dateRange?: [string, string] }[]
}

// Simule le moteur analytics selon la requête reçue.
const engine = async (q: Q) => {
  const dim = q.timeDimensions?.[0]
  const respond = (data: Array<Record<string, unknown>>) => ({ data, meta: { total: data.length, query: '', params: [] } })
  if (q.dimensions.includes('veridian_profile_id')) {
    const field = `sent_at_${dim?.granularity}`
    return respond([
      { veridian_profile_id: 'a', [field]: '2026-10-08T09:00:00', count_sent: 149 },
      { veridian_profile_id: 'b', [field]: '2026-10-08T09:00:00', count_sent: 170 }
    ])
  }
  if (q.measures.includes('count_sent')) return respond([{ sent_at_day: '2026-10-08T00:00:00', count_sent: 240 }])
  if (q.measures.includes('count_bounced_hard')) return respond([{ bounced_at_day: '2026-10-08T00:00:00', count_bounced_hard: 5, count_bounced_soft: 1 }])
  if (q.measures.includes('count_policy_refused')) return respond([{ sent_at_day: '2026-10-08T00:00:00', count_policy_refused: 4 }])
  if (q.measures.includes('count_complained')) return respond([{ complained_at_day: '2026-10-08T00:00:00', count_complained: 2 }])
  if (q.measures.includes('count_unsubscribed')) return respond([{ unsubscribed_at_day: '2026-10-08T00:00:00', count_unsubscribed: 7 }])
  return respond([])
}

const renderDash = (onOpenTransactional?: () => void) => {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  return render(
    <QueryClientProvider client={client}>
      <I18nProvider i18n={i18n}>
        <App>
          <VeridianProspectionDashboard
            workspace={workspace}
            timeRange={['2026-09-25', '2026-10-09']}
            timezone="Europe/Paris"
            onOpenTransactional={onOpenTransactional}
          />
        </App>
      </I18nProvider>
    </QueryClientProvider>
  )
}

describe('VeridianProspectionDashboard', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    vi.mocked(analyticsService.query).mockImplementation(engine as never)
    vi.mocked(emailProfilesOverviewService.get).mockResolvedValue(baseOverview)
    vi.mocked(prospectionStatsService.get).mockResolvedValue(stats)
    vi.mocked(replyStatsApi.get).mockResolvedValue({ replied: 13, replied_human: 12 })
  })

  it('tuiles : envoyés / plafond effectif, réponses humaines et taux, automatiques à part, stock, rotation, réputation', async () => {
    renderDash()
    await waitFor(() => expect(within(screen.getByTestId('tile-sent-today')).getByText('319 / 600')).toBeInTheDocument())
    await waitFor(() => expect(within(screen.getByTestId('tile-human-replies')).getByText('12 (5%)')).toBeInTheDocument())
    expect(within(screen.getByTestId('tile-auto-replies')).getByText('1')).toBeInTheDocument()
    await waitFor(() => expect(within(screen.getByTestId('tile-stock')).getByText('20,000')).toBeInTheDocument())
    expect(within(screen.getByTestId('tile-profiles')).getByText('2')).toBeInTheDocument()
    // un seul couple ralenti (relais-a / Microsoft), aucun arrêt
    expect(within(screen.getByTestId('tile-reputation')).getByText('1')).toBeInTheDocument()
    expect(screen.getByTestId('email-chart')).toHaveAttribute('data-type', 'commercial')
    expect(screen.getByTestId('engagement-by-class')).toBeInTheDocument()
  })

  it("envois par relais : la requête analytics porte la dimension profil et le type commercial ; plafond effectif du jour par relais", async () => {
    renderDash()
    await waitFor(() => expect(screen.getByTestId('sends-chart')).toBeInTheDocument())
    const queries = vi.mocked(analyticsService.query).mock.calls.map((c) => c[0] as unknown as Q & { filters: Array<{ member: string; values: string[] }> })
    const sends = queries.find((q) => q.dimensions.includes('veridian_profile_id'))
    expect(sends).toBeDefined()
    expect(sends!.timeDimensions![0]).toMatchObject({ dimension: 'sent_at', granularity: 'day', dateRange: ['2026-09-25', '2026-10-09'] })
    expect(sends!.filters).toContainEqual({ member: 'message_type', operator: 'equals', values: ['commercial'] })
    await waitFor(() => expect(screen.getByTestId('today-a')).toHaveTextContent('149 / 300'))
    // la période : 149 envois pour relais-a, 170 pour relais-b
    const card = screen.getByText('Sends per relay').closest('.ant-card') as HTMLElement
    const row = within(card).getAllByText('relais-a')[0].closest('tr') as HTMLElement
    expect(within(row).getByText('149', { selector: 'td' })).toBeInTheDocument()
    const rowB = within(card).getAllByText('relais-b')[0].closest('tr') as HTMLElement
    expect(within(rowB).getByText('170', { selector: 'td' })).toBeInTheDocument()
  })

  it('bascule par heure : granularité hour sur le jour courant du fuseau', async () => {
    renderDash()
    await waitFor(() => expect(screen.getByTestId('sends-chart')).toBeInTheDocument())
    fireEvent.click(screen.getByText('Per hour'))
    await waitFor(() => {
      const hourly = vi
        .mocked(analyticsService.query)
        .mock.calls.map((c) => c[0] as unknown as Q)
        .find((q) => q.timeDimensions?.[0]?.granularity === 'hour')
      expect(hourly).toBeDefined()
      const range = hourly!.timeDimensions![0].dateRange!
      expect(range[0]).toBe(range[1])
    })
    expect(screen.getAllByText('Sent today').length).toBeGreaterThan(0)
  })

  it('avancement des séquences : J0, J+4, J+10, en file, en attente, sorties par raison', async () => {
    renderDash()
    const j0 = await screen.findByTestId('stage-1')
    expect(j0).toHaveTextContent('759')
    expect(j0).toHaveTextContent('2,773 in the queue, 0 waiting, 16 left after')
    expect(screen.getByTestId('stage-2')).toHaveTextContent('733 waiting')
    expect(screen.getAllByText('J+10').length).toBeGreaterThan(0)
    expect(screen.getByTestId('exits-devenir')).toHaveTextContent('16 replied, 0 rejected, 0 unsubscribed, 83 excluded')
  })

  it('réponses par séquence et par segment, stock restant, automatiques à part', async () => {
    renderDash()
    const sequenceCard = (await screen.findByText('Replies by sequence')).closest('.ant-card') as HTMLElement
    await waitFor(() => expect(within(sequenceCard).getByText('E-commerce à devenir')).toBeInTheDocument())
    expect(within(sequenceCard).getByText('12')).toBeInTheDocument()
    expect(within(sequenceCard).getByText('5%')).toBeInTheDocument()
    expect(within(sequenceCard).getAllByText('Automatic replies').length).toBeGreaterThan(0)
    const segmentCard = screen.getByText('Segments (lists): replies and remaining stock').closest('.ant-card') as HTMLElement
    expect(within(segmentCard).getByText('2,500')).toBeInTheDocument()
    expect(within(segmentCard).getByText('3,300')).toBeInTheDocument()
  })

  it('rejets durs, mous, refus de politique, désinscriptions, plaintes', async () => {
    renderDash()
    await waitFor(() => expect(within(screen.getByTestId('rej-hard')).getByText('5')).toBeInTheDocument())
    expect(within(screen.getByTestId('rej-soft')).getByText('1')).toBeInTheDocument()
    expect(within(screen.getByTestId('rej-policy')).getByText('4')).toBeInTheDocument()
    expect(within(screen.getByTestId('rej-unsub')).getByText('7')).toBeInTheDocument()
    expect(within(screen.getByTestId('rej-complaint')).getByText('2')).toBeInTheDocument()
  })

  it('réputation par fournisseur : seulement le couple ralenti, avec son taux', async () => {
    renderDash()
    const card = (await screen.findByText('Reputation by recipient provider')).closest('.ant-card') as HTMLElement
    await waitFor(() => expect(within(card).getByText('Microsoft')).toBeInTheDocument())
    expect(within(card).getByText('Slowed ÷4')).toBeInTheDocument()
    expect(within(card).getByText('9.9% bounces')).toBeInTheDocument()
    expect(within(card).queryByText('Google')).not.toBeInTheDocument()
  })

  it('aucun couple ralenti : le dit', async () => {
    vi.mocked(emailProfilesOverviewService.get).mockResolvedValue(overviewOf([profile('b', { name: 'relais-b' })]))
    renderDash()
    await waitFor(() => expect(screen.getByText('No provider is slowed or stopped.')).toBeInTheDocument())
  })

  it("profil transactionnel en alerte : bandeau visible avec lien vers la vue transactionnelle ; sain : rien", async () => {
    const open = vi.fn()
    vi.mocked(emailProfilesOverviewService.get).mockResolvedValue({ ...baseOverview, transactional_watch: watch('alert') })
    const { unmount } = renderDash(open)
    const banner = await screen.findByTestId('transactional-watch-banner')
    expect(banner).toHaveTextContent('Unusual volume: 900 mails today, 10 per day on average over the previous 7 days')
    fireEvent.click(within(banner).getByText('See the transactional dashboard'))
    expect(open).toHaveBeenCalledTimes(1)
    unmount()

    vi.mocked(emailProfilesOverviewService.get).mockResolvedValue({ ...baseOverview, transactional_watch: watch('ok') })
    renderDash(open)
    await waitFor(() => expect(screen.getByTestId('tile-sent-today')).toHaveTextContent('319 / 600'))
    expect(screen.queryByTestId('transactional-watch-banner')).not.toBeInTheDocument()
  })

  it('mesure transactionnelle impossible : affichée, jamais présentée comme saine', async () => {
    vi.mocked(emailProfilesOverviewService.get).mockResolvedValue({ ...baseOverview, transactional_watch: watch('unknown') })
    renderDash()
    const banner = await screen.findByTestId('transactional-watch-banner')
    expect(banner).toHaveTextContent('Not measured')
    expect(banner).toHaveTextContent('Nothing is blocked')
  })

  it("agrégats indisponibles : message d'erreur, le reste du tableau de bord reste affiché", async () => {
    vi.mocked(prospectionStatsService.get).mockRejectedValue(new Error('boom'))
    renderDash()
    await waitFor(() => expect(screen.getAllByText('Unable to load the prospection figures').length).toBeGreaterThan(0))
    expect(screen.getByTestId('email-chart')).toBeInTheDocument()
    await waitFor(() => expect(within(screen.getByTestId('tile-sent-today')).getByText('319 / 600')).toBeInTheDocument())
  })
})
