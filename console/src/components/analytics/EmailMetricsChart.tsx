import React, { useState, useEffect } from 'react'
import { Segmented, Alert, Row, Col, Statistic, Space, Tooltip, Card, Button } from 'antd'
import { useLingui, Plural } from '@lingui/react/macro'
import { FontAwesomeIcon } from '@fortawesome/react-fontawesome'
import {
  faPaperPlane,
  faCircleXmark,
  faFaceFrown
} from '@fortawesome/free-regular-svg-icons'
import {
  faFilterCircleXmark,
  faTriangleExclamation,
  faBan,
  faComments
} from '@fortawesome/free-solid-svg-icons'
import { ChartVisualization } from './ChartVisualization'
import { analyticsService, AnalyticsQuery, AnalyticsResponse } from '../../services/api/analytics'
import {
  EMAIL_SERIES,
  EXCLUDED_SERIES,
  MERGED_TIME_DIMENSION,
  MessageTypeFilter,
  buildSeriesQuery,
  mergeSeries
} from './email_metrics_series'
import { replyStatsApi } from '../../services/api/veridian_reply_stats'
import { Workspace } from '../../services/api/types'

interface EmailMetricsChartProps {
  workspace: Workspace
  timeRange?: [string, string]
  timezone?: string
}

export const EmailMetricsChart: React.FC<EmailMetricsChartProps> = ({
  workspace,
  timeRange = ['2024-01-01', '2024-12-31'],
  timezone
}) => {
  const { t } = useLingui()
  const [messageTypeFilter, setMessageTypeFilter] = useState<MessageTypeFilter>('all')
  const [data, setData] = useState<AnalyticsResponse | null>(null)
  const [loading, setLoading] = useState(false)
  const [statsLoading, setStatsLoading] = useState(false)
  const [error, setError] = useState<string | null>(null)
  // Veridian — KPI taux de RÉPONSE (cold outbound). Le signal reply vit dans une
  // table séparée (veridian_contact_reply, stop-on-reply Lot 3), donc un endpoint
  // dédié, pas une mesure du moteur analytics message_history.
  // `replied` = total tous types (humain + automatique) ; `repliedHuman` = réponses
  // humaines seules (champ replied_human, absent sur un backend ancien => null).
  const [replied, setReplied] = useState<number>(0)
  const [repliedHuman, setRepliedHuman] = useState<number | null>(null)
  // Veridian — Échecs = exclusions volontaires (pre-filter, classe exclue) + échecs
  // réels (SMTP). Mesure `count_failed_excluded` fournie par le backend ; null tant
  // qu'elle n'existe pas (la répartition est alors indisponible, jamais inventée).
  const [failedExcluded, setFailedExcluded] = useState<number | null>(null)

  // State to track which chart lines are visible
  const [visibleLines, setVisibleLines] = useState<Record<string, boolean>>({
    count_sent: true,
    count_bounced: true,
    count_complained: true,
    count_unsubscribed: true,
    count_failed: true
  })

  // Function to toggle line visibility
  const toggleLineVisibility = (measure: string) => {
    setVisibleLines((prev) => ({
      ...prev,
      [measure]: !prev[measure]
    }))
  }

  const effectiveTimezone = timezone || workspace.settings.timezone || 'UTC'

  // Requête SYNTHÉTIQUE décrivant la série fusionnée (voir email_metrics_series.ts :
  // chaque mesure est groupée sur sa propre date d'événement). Sert uniquement à
  // ChartVisualization (mesures visibles + colonne temporelle).
  const buildChartQuery = (): AnalyticsQuery => ({
    schema: 'message_history',
    measures: [
      'count_sent',
      'count_bounced',
      'count_complained',
      'count_unsubscribed',
      'count_failed'
    ].filter((measure) => visibleLines[measure]),
    dimensions: [],
    timezone: effectiveTimezone,
    timeDimensions: [{ dimension: MERGED_TIME_DIMENSION, granularity: 'day', dateRange: timeRange }],
    filters: []
  })

  const fetchData = async (filter: MessageTypeFilter) => {
    try {
      setLoading(true)
      setStatsLoading(true)
      setError(null)

      // Une requête par famille de mesures (chacune sur sa date d'événement), plus
      // le reply (contact-level, donc PAS filtré par broadcast/transactional : on le
      // compte sur la même fenêtre uniquement) et les exclusions volontaires.
      // Best-effort : un échec du reply ou des exclusions ne casse pas le dashboard.
      const [seriesResponses, replyResponse, excludedResponse] = await Promise.all([
        Promise.all(
          EMAIL_SERIES.map((def) =>
            analyticsService
              .query(buildSeriesQuery(def, filter, timeRange, effectiveTimezone), workspace.id)
              .then((response) => ({ def, response }))
          )
        ),
        replyStatsApi
          .get({ workspace_id: workspace.id, start: timeRange[0], end: timeRange[1] })
          .catch((replyErr) => {
            console.error('Failed to fetch reply stats:', replyErr)
            return { replied: 0, replied_human: undefined }
          }),
        analyticsService
          .query(buildSeriesQuery(EXCLUDED_SERIES, filter, timeRange, effectiveTimezone), workspace.id)
          .catch(() => null)
      ])

      setData(mergeSeries(seriesResponses))
      setReplied(replyResponse.replied)
      setRepliedHuman(
        typeof replyResponse.replied_human === 'number' ? replyResponse.replied_human : null
      )
      setFailedExcluded(
        excludedResponse?.data
          ? excludedResponse.data.reduce((acc, row) => acc + toNumber(row.count_failed_excluded), 0)
          : null
      )
    } catch (err) {
      console.error('Failed to fetch email metrics:', err)
      setError(err instanceof Error ? err.message : t`Failed to fetch email metrics`)
    } finally {
      setLoading(false)
      setStatsLoading(false)
    }
  }

  useEffect(() => {
    fetchData(messageTypeFilter)
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [workspace.id, messageTypeFilter, timeRange, timezone])

  const handleFilterChange = (value: MessageTypeFilter) => {
    setMessageTypeFilter(value)
  }

  // Define the stats type
  interface EmailStats {
    count_sent: number
    count_bounced: number
    // Veridian — split hard/soft du bounce (KPI cold). Hard = adresse morte
    // (réputation), soft = transitoire. Voir le tooltip de la carte Bounced.
    count_bounced_hard: number
    count_bounced_soft: number
    count_complained: number
    count_unsubscribed: number
    count_failed: number
  }

  // Helper function to safely convert unknown to number
  const toNumber = (value: unknown): number => {
    if (typeof value === 'number') return value
    if (typeof value === 'string') {
      const parsed = parseFloat(value)
      return isNaN(parsed) ? 0 : parsed
    }
    return 0
  }

  // Extract and aggregate stats from the stats response (sum up all daily values)
  const emptyStats: EmailStats = {
    count_sent: 0,
    count_bounced: 0,
    count_bounced_hard: 0,
    count_bounced_soft: 0,
    count_complained: 0,
    count_unsubscribed: 0,
    count_failed: 0
  }

  const stats: EmailStats =
    data?.data?.reduce<EmailStats>(
      (acc, row) => ({
        count_sent: acc.count_sent + toNumber(row.count_sent),
        count_bounced: acc.count_bounced + toNumber(row.count_bounced),
        count_bounced_hard: acc.count_bounced_hard + toNumber(row.count_bounced_hard),
        count_bounced_soft: acc.count_bounced_soft + toNumber(row.count_bounced_soft),
        count_complained: acc.count_complained + toNumber(row.count_complained),
        count_unsubscribed: acc.count_unsubscribed + toNumber(row.count_unsubscribed),
        count_failed: acc.count_failed + toNumber(row.count_failed)
      }),
      { ...emptyStats }
    ) || emptyStats

  const getRate = (numerator: number, denominator: number) => {
    if (denominator === 0) return '-'
    const percentage = (numerator / denominator) * 100
    if (percentage === 0 || percentage >= 10) {
      return `${Math.round(percentage)}%`
    }
    return `${percentage.toFixed(1)}%`
  }

  // Réponses : taux sur les réponses HUMAINES (replied_human, sinon replied) ;
  // les réponses automatiques (replied - replied_human) sont affichées à part.
  const humanReplies = repliedHuman ?? replied
  const autoReplies = Math.max(0, replied - humanReplies)
  // Échec réel = échecs totaux - exclusions volontaires (jamais négatif).
  const realFailed = Math.max(0, stats.count_failed - (failedExcluded ?? 0))

  // Define colors that match the icon colors in the statistics cards
  const chartColors = {
    count_sent: '#3b82f6', // blue-500
    count_bounced: '#f97316', // orange-500
    count_complained: '#f97316', // orange-500
    count_unsubscribed: '#f97316', // orange-500
    count_failed: '#ef4444' // red-500
  }

  // Define measure titles for tooltip display
  const measureTitles = {
    count_sent: t`Sent`,
    count_bounced: t`Bounced`,
    count_complained: t`Complaints`,
    count_unsubscribed: t`Unsubscribes`,
    count_failed: t`Failed (excluded + real)`
  }

  return (
    <Card
      title={t`Email Metrics`}
      extra={
        <Segmented
          value={messageTypeFilter}
          onChange={handleFilterChange}
          options={[
            { label: t`All`, value: 'all' },
            { label: t`Broadcasts`, value: 'broadcasts' },
            { label: t`Transactional`, value: 'transactional' }
          ]}
        />
      }
    >
      {/* Error Alert — état d'erreur PROPRE : message lisible + retry. On ne
          montre JAMAIS une valeur brute non parlante (ex: "true") à l'écran :
          le client API extrait déjà un message humain (cf. client.ts), et on
          affiche un libellé d'aide explicite au-dessus du détail technique. */}
      {error && (
        <Alert
          message={t`Unable to load email metrics`}
          description={
            <Space direction="vertical" size={8} style={{ width: '100%' }}>
              <span>{t`The dashboard could not load your email metrics. This is usually temporary — please try again.`}</span>
              <span style={{ fontSize: 12, opacity: 0.75, wordBreak: 'break-word' }}>
                {error}
              </span>
              <Button
                size="small"
                onClick={() => fetchData(messageTypeFilter)}
                loading={loading || statsLoading}
              >
                {t`Retry`}
              </Button>
            </Space>
          }
          type="error"
          showIcon
          style={{ marginBottom: 16 }}
        />
      )}

      {/* Veridian — mails cold en texte brut : ni pixel ni lien suivi, donc les
          ouvertures et clics n'existent pas. On l'affiche, au lieu de zéros. */}
      <Alert
        type="info"
        showIcon
        message={t`Opens and clicks are not tracked: plain-text emails, no pixel or tracked link`}
        style={{ marginBottom: 16 }}
      />

      {/* Stats Row */}
      <Row gutter={[16, 16]} wrap className="flex-nowrap overflow-x-auto">
        <Col span={3}>
          <Tooltip
            title={!visibleLines.count_sent ? t`${stats.count_sent} total emails sent (hidden from chart)` : t`${stats.count_sent} total emails sent`}
          >
            <div
              className="p-2 cursor-pointer hover:bg-gray-50 rounded transition-colors"
              style={{ opacity: visibleLines.count_sent ? 1 : 0.5 }}
              onClick={() => toggleLineVisibility('count_sent')}
            >
              <Statistic
                title={
                  <Space className="font-medium">
                    <FontAwesomeIcon
                      icon={faPaperPlane}
                      style={{ opacity: 0.7 }}
                      className="text-blue-500"
                    />{' '}
                    {t`Sent`}
                  </Space>
                }
                value={stats.count_sent}
                valueStyle={{ fontSize: '16px' }}
                loading={statsLoading}
              />
            </div>
          </Tooltip>
        </Col>
        {/* Veridian — Reply rate : LE KPI #1 du cold outreach (conversion réelle).
            replied = contacts uniques ayant répondu sur la fenêtre ; ratio approx.
            replies / sent (un contact peut avoir reçu plusieurs envois).
            ⚠️ Le signal reply est CONTACT-level (table veridian_contact_reply, pas
            rattaché à un envoi) → il IGNORE le filtre All/Broadcasts/Transactional :
            tooltip explicite pour lever la confusion (ticket
            2026-06-17-reply-kpi-ignore-message-type-filter.md, option 1). */}
        <Col span={3}>
          <Tooltip
            title={t`${humanReplies} contacts replied by a human (reply rate = human replies / sent, the #1 cold outreach KPI). Automatic replies (out of office, acknowledgements) are not counted in the rate. All campaigns combined: the reply signal is not tied to a specific send, so this card ignores the All/Broadcasts/Transactional filter.`}
          >
            <div className="p-2 rounded">
              <Statistic
                title={
                  <Space className="font-medium">
                    <FontAwesomeIcon
                      icon={faComments}
                      style={{ opacity: 0.7 }}
                      className="text-emerald-500"
                    />{' '}
                    {t`Replies`}
                  </Space>
                }
                value={getRate(humanReplies, stats.count_sent)}
                valueStyle={{ fontSize: '16px' }}
                loading={statsLoading}
              />
              {autoReplies > 0 && (
                <div className="text-xs text-gray-500" data-testid="auto-replies">
                  <Plural
                    value={autoReplies}
                    one="+# automatic reply"
                    other="+# automatic replies"
                  />
                </div>
              )}
            </div>
          </Tooltip>
        </Col>
        <Col span={3}>
          {/* Veridian — la carte Bounced reste le TOTAL ; le split hard/soft est
              révélé au survol. Hard = adresse morte (réputation grillée, suppression
              immédiate) ; soft = transitoire (boîte pleine, greylisting). Note : sur
              le flux actuel seuls les hard posent bounced_at sur message_history, donc
              soft ~0 ici (cf. 2026-06-16-kpi-bounce-hard-soft-dashboard.md). */}
          <Tooltip
            title={
              <span>
                {!visibleLines.count_bounced
                  ? t`${stats.count_bounced} emails bounced back (hidden from chart)`
                  : t`${stats.count_bounced} emails bounced back`}
                <br />
                {t`Hard: ${stats.count_bounced_hard} (dead address) · Soft: ${stats.count_bounced_soft} (transient)`}
              </span>
            }
          >
            <div
              className="p-2 cursor-pointer hover:bg-gray-50 rounded transition-colors"
              style={{ opacity: visibleLines.count_bounced ? 1 : 0.5 }}
              onClick={() => toggleLineVisibility('count_bounced')}
            >
              <Statistic
                title={
                  <Space className="font-medium">
                    <FontAwesomeIcon
                      icon={faTriangleExclamation}
                      style={{ opacity: 0.7 }}
                      className="text-orange-500"
                    />{' '}
                    {t`Bounced`}
                  </Space>
                }
                value={getRate(stats.count_bounced, stats.count_sent)}
                valueStyle={{ fontSize: '16px' }}
                loading={statsLoading}
              />
            </div>
          </Tooltip>
        </Col>
        <Col span={3}>
          <Tooltip
            title={!visibleLines.count_complained ? t`${stats.count_complained} total complaints (hidden from chart)` : t`${stats.count_complained} total complaints`}
          >
            <div
              className="p-2 cursor-pointer hover:bg-gray-50 rounded transition-colors"
              style={{ opacity: visibleLines.count_complained ? 1 : 0.5 }}
              onClick={() => toggleLineVisibility('count_complained')}
            >
              <Statistic
                title={
                  <Space className="font-medium">
                    <FontAwesomeIcon
                      icon={faFaceFrown}
                      style={{ opacity: 0.7 }}
                      className="text-orange-500"
                    />{' '}
                    {t`Complaints`}
                  </Space>
                }
                value={getRate(stats.count_complained, stats.count_sent)}
                valueStyle={{ fontSize: '16px' }}
                loading={statsLoading}
              />
            </div>
          </Tooltip>
        </Col>
        <Col span={3}>
          <Tooltip
            title={!visibleLines.count_unsubscribed ? t`${stats.count_unsubscribed} total unsubscribes (hidden from chart)` : t`${stats.count_unsubscribed} total unsubscribes`}
          >
            <div
              className="p-2 cursor-pointer hover:bg-gray-50 rounded transition-colors"
              style={{ opacity: visibleLines.count_unsubscribed ? 1 : 0.5 }}
              onClick={() => toggleLineVisibility('count_unsubscribed')}
            >
              <Statistic
                title={
                  <Space className="font-medium">
                    <FontAwesomeIcon
                      icon={faBan}
                      style={{ opacity: 0.7 }}
                      className="text-orange-500"
                    />{' '}
                    {t`Unsub.`}
                  </Space>
                }
                value={getRate(stats.count_unsubscribed, stats.count_sent)}
                valueStyle={{ fontSize: '16px' }}
                loading={statsLoading}
              />
            </div>
          </Tooltip>
        </Col>
        <Col span={3}>
          <Tooltip
            title={
              failedExcluded === null
                ? t`Deliberate exclusions (invalid address, excluded provider class): breakdown unavailable, total failures: ${stats.count_failed}`
                : t`${failedExcluded} recipients deliberately excluded before sending (invalid address, excluded provider class). Not a delivery problem.`
            }
          >
            <div className="p-2 rounded">
              <Statistic
                title={
                  <Space className="font-medium">
                    <FontAwesomeIcon
                      icon={faFilterCircleXmark}
                      style={{ opacity: 0.7 }}
                      className="text-gray-500"
                    />{' '}
                    {t`Deliberately excluded`}
                  </Space>
                }
                value={failedExcluded === null ? '-' : failedExcluded}
                valueStyle={{ fontSize: '16px' }}
                loading={statsLoading}
              />
            </div>
          </Tooltip>
        </Col>
        <Col span={3}>
          <Tooltip
            title={
              failedExcluded === null
                ? t`${stats.count_failed} total failures (deliberate exclusions not yet separated)`
                : !visibleLines.count_failed
                  ? t`${realFailed} emails really failed to send (hidden from chart, the chart line shows all failures)`
                  : t`${realFailed} emails really failed to send (the chart line shows all failures)`
            }
          >
            <div
              className="p-2 cursor-pointer hover:bg-gray-50 rounded transition-colors"
              style={{ opacity: visibleLines.count_failed ? 1 : 0.5 }}
              onClick={() => toggleLineVisibility('count_failed')}
            >
              <Statistic
                title={
                  <Space className="font-medium">
                    <FontAwesomeIcon
                      icon={faCircleXmark}
                      style={{ opacity: 0.7 }}
                      className="text-red-500"
                    />{' '}
                    {failedExcluded === null ? t`Failed` : t`Real failures`}
                  </Space>
                }
                value={getRate(failedExcluded === null ? stats.count_failed : realFailed, stats.count_sent)}
                valueStyle={{ fontSize: '16px' }}
                loading={statsLoading}
              />
            </div>
          </Tooltip>
        </Col>
      </Row>

      {/* Chart */}
      <ChartVisualization
        data={data}
        chartType="line"
        query={buildChartQuery()}
        loading={loading}
        error={error}
        height={220}
        showLegend={false}
        colors={chartColors}
        measureTitles={measureTitles}
      />
    </Card>
  )
}
