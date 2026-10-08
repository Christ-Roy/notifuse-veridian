import React, { useEffect, useMemo, useRef, useState } from 'react'
import { Alert, Card, Progress, Segmented, Skeleton, Space, Table, Tag, Typography } from 'antd'
import { useQuery } from '@tanstack/react-query'
import { useLingui } from '@lingui/react/macro'
import * as echarts from 'echarts/core'
import { BarChart } from 'echarts/charts'
import { GridComponent, LegendComponent, TooltipComponent } from 'echarts/components'
import { CanvasRenderer } from 'echarts/renderers'

import { analyticsService } from '../../services/api/analytics'
import type { EmailProfileOverview, EmailProfilesOverview } from '../../services/api/veridian_email_profiles'
import type { Workspace } from '../../services/api/types'
import { formatNumber, useProfileLabels } from '../sending_profiles/veridian_profile_labels'
import { limitingFactor, profileBlockers, todayProgress } from '../sending_profiles/veridian_profile_rules'
import {
  PROFILE_DIMENSION,
  SENDS_BY_PROFILE_MEASURE,
  buildSendsSeries,
  dayInTimezone,
  daySlots,
  hourSlots,
  type SendBucket,
  type SendsSeries
} from './veridian_prospection_rules'

echarts.use([BarChart, GridComponent, LegendComponent, TooltipComponent, CanvasRenderer])

const { Text } = Typography

// Palette lisible en clair et en sombre, assez de teintes pour un parc de relais.
const SERIES_COLORS = ['#3b82f6', '#10b981', '#f59e0b', '#8b5cf6', '#ef4444', '#06b6d4', '#84cc16', '#ec4899']

interface Props {
  workspace: Workspace
  timeRange: [string, string]
  timezone: string
  overview?: EmailProfilesOverview
  overviewLoading: boolean
}

// Barres empilées par relais (échelle : un seau = un jour ou une heure).
export const SendsStackedBars: React.FC<{ series: SendsSeries; height?: number }> = ({ series, height = 240 }) => {
  const ref = useRef<HTMLDivElement>(null)
  useEffect(() => {
    if (!ref.current) return
    const chart = echarts.init(ref.current)
    chart.setOption({
      color: SERIES_COLORS,
      tooltip: { trigger: 'axis', axisPointer: { type: 'shadow' } },
      legend: { top: 0, type: 'scroll' },
      grid: { left: 40, right: 16, top: 36, bottom: 24 },
      xAxis: { type: 'category', data: series.buckets },
      yAxis: { type: 'value', minInterval: 1 },
      series: series.series.map((s) => ({
        name: s.name,
        type: 'bar',
        stack: 'sends',
        data: s.values
      }))
    })
    const onResize = () => chart.resize()
    window.addEventListener('resize', onResize)
    // Le conteneur peut changer de largeur sans fenetre qui bouge (barre laterale, defilement)
    const observer = typeof ResizeObserver === 'undefined' ? null : new ResizeObserver(onResize)
    observer?.observe(ref.current)
    return () => {
      window.removeEventListener('resize', onResize)
      observer?.disconnect()
      chart.dispose()
    }
  }, [series])
  return <div ref={ref} style={{ height }} data-testid="sends-chart" />
}

export const VeridianProspectionSends: React.FC<Props> = ({
  workspace,
  timeRange,
  timezone,
  overview,
  overviewLoading
}) => {
  const { t } = useLingui()
  const labels = useProfileLabels()
  const [bucket, setBucket] = useState<SendBucket>('day')
  const [hourDay, setHourDay] = useState<'today' | 'yesterday'>('today')

  const hourDate = dayInTimezone(timezone, hourDay === 'today' ? 0 : -1)
  const range: [string, string] = bucket === 'day' ? timeRange : [hourDate, hourDate]

  const { data, isLoading, error } = useQuery({
    queryKey: ['prospection', 'sends', workspace.id, bucket, range[0], range[1], timezone],
    queryFn: () =>
      analyticsService.query(
        {
          schema: 'message_history',
          measures: [SENDS_BY_PROFILE_MEASURE],
          dimensions: [PROFILE_DIMENSION],
          timezone,
          timeDimensions: [{ dimension: 'sent_at', granularity: bucket, dateRange: range }],
          filters: [{ member: 'message_type', operator: 'equals', values: ['commercial'] }]
        },
        workspace.id
      ),
    refetchInterval: 60000
  })

  const profiles = overview?.profiles ?? []
  const commercial = profiles.filter((p) => p.usage !== 'transactional')
  const nameOf = (profileId: string): string => {
    if (profileId === '') return t`Unassigned (older messages)`
    return profiles.find((p) => p.integration_id === profileId)?.name ?? profileId.slice(0, 8)
  }

  const series = useMemo(
    () =>
      buildSendsSeries(
        data?.data,
        bucket,
        bucket === 'day' ? daySlots(timeRange) : hourSlots(),
        nameOf
      ),
    // eslint-disable-next-line react-hooks/exhaustive-deps
    [data, bucket, timeRange, overview]
  )

  const sentByProfile = new Map(series.series.map((s) => [s.profileId, s.total]))
  interface Row {
    key: string
    profile?: EmailProfileOverview
    name: string
    sent: number
  }
  const rows: Row[] = commercial.map((p) => ({
    key: p.integration_id,
    profile: p,
    name: p.name,
    sent: sentByProfile.get(p.integration_id) ?? 0
  }))
  for (const s of series.series) {
    if (!commercial.some((p) => p.integration_id === s.profileId)) {
      rows.push({ key: s.profileId || 'unassigned', name: s.name, sent: s.total })
    }
  }

  const periodLabel =
    bucket === 'day' ? t`Sent over the period` : hourDay === 'today' ? t`Sent today` : t`Sent yesterday`

  return (
    <Card
      title={t`Sends per relay`}
      extra={
        <Space>
          {bucket === 'hour' && (
            <Segmented
              size="small"
              value={hourDay}
              onChange={(v) => setHourDay(v as 'today' | 'yesterday')}
              options={[
                { label: t`Today`, value: 'today' },
                { label: t`Yesterday`, value: 'yesterday' }
              ]}
            />
          )}
          <Segmented
            size="small"
            value={bucket}
            onChange={(v) => setBucket(v as SendBucket)}
            options={[
              { label: t`Per day`, value: 'day' },
              { label: t`Per hour`, value: 'hour' }
            ]}
          />
        </Space>
      }
    >
      {error && <Alert type="error" showIcon message={t`Unable to load the sends`} style={{ marginBottom: 12 }} />}
      {isLoading ? <Skeleton active paragraph={{ rows: 5 }} /> : <SendsStackedBars series={series} />}
      <Table<Row>
        size="small"
        pagination={false}
        loading={overviewLoading}
        rowKey="key"
        dataSource={rows}
        style={{ marginTop: 12 }}
        scroll={{ x: true }}
        columns={[
          { title: t`Relay`, dataIndex: 'name', key: 'name' },
          { title: periodLabel, dataIndex: 'sent', key: 'sent', align: 'right', render: (v: number) => formatNumber(v) },
          {
            title: t`Today / effective cap`,
            key: 'today',
            render: (_: unknown, row: Row) => {
              if (!row.profile) return <Text type="secondary">-</Text>
              const progress = todayProgress(row.profile.plan)
              const label =
                progress.cap === null
                  ? formatNumber(progress.sent)
                  : `${formatNumber(progress.sent)} / ${formatNumber(progress.cap)}`
              return (
                <Space direction="vertical" size={0} style={{ minWidth: 120 }}>
                  <span data-testid={`today-${row.key}`}>{label}</span>
                  {progress.percent !== null && <Progress percent={progress.percent} size="small" showInfo={false} />}
                </Space>
              )
            }
          },
          {
            title: t`Limiting gate`,
            key: 'gate',
            render: (_: unknown, row: Row) =>
              row.profile ? labels.factorText(limitingFactor(row.profile.plan)) : <Text type="secondary">-</Text>
          },
          {
            title: t`State`,
            key: 'state',
            render: (_: unknown, row: Row) => {
              if (!row.profile) return <Text type="secondary">-</Text>
              const blockers = profileBlockers(row.profile)
              if (!row.profile.in_rotation && row.profile.usage !== 'transactional' && blockers.length === 0) {
                return <Tag>{t`Not in the rotation`}</Tag>
              }
              if (blockers.length === 0) return <Tag color="green">{t`Ready to send`}</Tag>
              return (
                <Space direction="vertical" size={2}>
                  {blockers.map((b) => (
                    <Tag key={b.kind} color={b.kind === 'reputation_stopped' ? 'red' : 'orange'}>
                      {labels.blockerText(b)}
                    </Tag>
                  ))}
                </Space>
              )
            }
          }
        ]}
      />
    </Card>
  )
}
