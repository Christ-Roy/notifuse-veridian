import React from 'react'
import { Col, Row, Statistic, Tooltip } from 'antd'
import { useQuery } from '@tanstack/react-query'
import { useLingui } from '@lingui/react/macro'

import type { Workspace } from '../../services/api/types'
import { analyticsService } from '../../services/api/analytics'
import { emailProfilesOverviewService } from '../../services/api/veridian_email_profiles'
import { prospectionStatsService } from '../../services/api/veridian_prospection'
import { replyStatsApi } from '../../services/api/veridian_reply_stats'
import { EmailMetricsChart } from '../analytics/EmailMetricsChart'
import { VeridianEngagementByClass } from '../analytics/veridian_engagement_by_class'
import { buildSeriesQuery, EMAIL_SERIES } from '../analytics/email_metrics_series'
import { formatNumber, formatPercent } from '../sending_profiles/veridian_profile_labels'
import { VeridianProspectionSends } from './veridian_prospection_sends'
import { VeridianProspectionReplies } from './veridian_prospection_replies'
import { VeridianProspectionSequences } from './veridian_prospection_sequences'
import { VeridianProspectionRejections } from './veridian_prospection_rejections'
import { VeridianProspectionReputation } from './veridian_prospection_reputation'
import { VeridianTransactionalWatchBanner } from './veridian_transactional_watch'
import { reputationCouples, sumMeasure } from './veridian_prospection_rules'

interface Props {
  workspace: Workspace
  timeRange: [string, string]
  timezone?: string
  // Ouvre la vue transactionnelle (bandeau d'alerte du profil transactionnel)
  onOpenTransactional?: () => void
}

// Tableau de bord de prospection (lot 5) : UNE page pour la vue commerciale. Il ne
// recalcule rien : les envois, rejets et plaintes viennent du moteur analytics, les
// plafonds et la réputation de emailProfiles.overview, les réponses globales de
// messages.replyStats, et seuls les agrégats qui n'existaient pas (réponses par séquence
// et par liste, avancement des séquences, stock) de prospection.stats.
export const VeridianProspectionDashboard: React.FC<Props> = ({
  workspace,
  timeRange,
  timezone,
  onOpenTransactional
}) => {
  const { t } = useLingui()
  const tz = timezone || workspace.settings?.timezone || 'UTC'
  const refreshKey = `${timeRange[0]}-${timeRange[1]}-${tz}`

  const overviewQuery = useQuery({
    queryKey: ['analytics', 'profiles-overview', workspace.id],
    queryFn: () => emailProfilesOverviewService.get(workspace.id),
    refetchInterval: 60000
  })
  const statsQuery = useQuery({
    queryKey: ['prospection', 'stats', workspace.id, timeRange[0], timeRange[1]],
    queryFn: () => prospectionStatsService.get({ workspace_id: workspace.id, start: timeRange[0], end: timeRange[1] }),
    refetchInterval: 60000
  })
  // Taux de réponse global : réponses humaines de la période / envois de la période (même
  // définition que la carte « Replies » du graphique de métriques).
  const repliesQuery = useQuery({
    queryKey: ['prospection', 'replies', workspace.id, timeRange[0], timeRange[1]],
    queryFn: () => replyStatsApi.get({ workspace_id: workspace.id, start: timeRange[0], end: timeRange[1] }),
    refetchInterval: 60000
  })
  const sentQuery = useQuery({
    queryKey: ['prospection', 'sent-total', workspace.id, timeRange[0], timeRange[1], tz],
    queryFn: async () => {
      const response = await analyticsService.query(
        buildSeriesQuery(EMAIL_SERIES[0], 'commercial', timeRange, tz),
        workspace.id
      )
      return sumMeasure(response.data, 'count_sent')
    },
    refetchInterval: 60000
  })

  const overview = overviewQuery.data
  const totals = overview?.totals
  const capacity = totals?.commercial_capacity_today
  const sentToday = formatNumber(totals?.commercial_sent_today ?? 0)
  const sentVersusCapacity = capacity === null || capacity === undefined ? sentToday : `${sentToday} / ${formatNumber(capacity)}`

  const replied = repliesQuery.data?.replied ?? 0
  const humanReplies = repliesQuery.data?.replied_human ?? replied
  const autoReplies = Math.max(0, replied - humanReplies)
  const sentTotal = sentQuery.data ?? 0
  const replyRate = sentTotal > 0 ? formatPercent(humanReplies / sentTotal) : '-'

  const couples = reputationCouples(overview)
  const stopped = couples.filter((c) => c.issue.stopped).length

  const tile = (testId: string, title: React.ReactNode, value: React.ReactNode, loading: boolean, hint?: string) => (
    <Col xs={12} md={8} xl={4} key={testId}>
      <Tooltip title={hint}>
        <div className="bg-gray-100 p-4 rounded-lg" style={{ height: '110px' }} data-testid={testId}>
          <Statistic title={title} value={value as string | number} valueStyle={{ fontSize: '22px', fontWeight: 'bold' }} loading={loading} />
        </div>
      </Tooltip>
    </Col>
  )

  return (
    <div>
      <VeridianTransactionalWatchBanner watch={overview?.transactional_watch} onOpen={onOpenTransactional} />

      <Row gutter={[16, 16]} className="mb-8" data-testid="cards-commercial">
        {tile(
          'tile-sent-today',
          t`Sent today / effective cap`,
          sentVersusCapacity,
          overviewQuery.isLoading,
          t`Sent today by the rotation profiles, over today's effective capacity.`
        )}
        {tile(
          'tile-human-replies',
          t`Human replies`,
          `${formatNumber(humanReplies)} (${replyRate})`,
          repliesQuery.isLoading || sentQuery.isLoading,
          t`Human replies over the period, and reply rate over the mails sent during the period.`
        )}
        {tile(
          'tile-auto-replies',
          t`Automatic replies`,
          formatNumber(autoReplies),
          repliesQuery.isLoading,
          t`Out-of-office and acknowledgements: counted apart, never in the reply rate.`
        )}
        {tile(
          'tile-stock',
          t`Remaining stock`,
          formatNumber(statsQuery.data?.totals.stock_remaining ?? 0),
          statsQuery.isLoading,
          t`Active contacts who have not been sent any mail yet, all segments.`
        )}
        {tile(
          'tile-profiles',
          t`Profiles in rotation`,
          totals?.active_commercial_profiles ?? 0,
          overviewQuery.isLoading
        )}
        {tile(
          'tile-reputation',
          t`Providers slowed or stopped`,
          stopped > 0 ? `${couples.length} (${stopped})` : couples.length,
          overviewQuery.isLoading,
          t`Relay and recipient provider pairs slowed or stopped by the reputation fuse. In brackets, how many are stopped.`
        )}
      </Row>

      <EmailMetricsChart
        key={`email-metrics-${refreshKey}`}
        workspace={workspace}
        timeRange={timeRange}
        timezone={tz}
        messageType="commercial"
      />

      <div className="mt-8">
        <VeridianProspectionSends
          key={`sends-${refreshKey}`}
          workspace={workspace}
          timeRange={timeRange}
          timezone={tz}
          overview={overview}
          overviewLoading={overviewQuery.isLoading}
        />
      </div>

      <div className="mt-8">
        <VeridianProspectionSequences stats={statsQuery.data} loading={statsQuery.isLoading} error={statsQuery.isError} />
        <VeridianProspectionReplies stats={statsQuery.data} loading={statsQuery.isLoading} error={statsQuery.isError} />
        <VeridianProspectionRejections
          key={`rejections-${refreshKey}`}
          workspace={workspace}
          timeRange={timeRange}
          timezone={tz}
        />
        <VeridianProspectionReputation overview={overview} loading={overviewQuery.isLoading} />
      </div>

      <VeridianEngagementByClass key={`engagement-by-class-${refreshKey}`} workspace={workspace} timeRange={timeRange} />
    </div>
  )
}
