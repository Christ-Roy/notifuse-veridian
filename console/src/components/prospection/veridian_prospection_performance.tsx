import React, { useMemo } from 'react'
import { Alert, Card, Table, Tag, Typography } from 'antd'
import { useLingui } from '@lingui/react/macro'

import type { ProspectionStats } from '../../services/api/veridian_prospection'
import type { Template } from '../../services/api/template'
import { MIN_SAMPLE_FOR_RATE } from '../automations/veridian_flow_labels'
import { formatNumber, formatPercent } from '../sending_profiles/veridian_profile_labels'
import { buildPerformanceRows, type PerformanceRow } from './veridian_prospection_performance_rows'

const { Text } = Typography

interface Props {
  stats?: ProspectionStats
  templates: Template[]
  loading: boolean
  error: boolean
}

// « Performance des campagnes » : une ligne par étape de séquence (et donc par modèle), triable.
// Le taux de réponse est HUMAIN (les réponses automatiques sont exclues), rapporté aux envois
// de l'étape. Les réponses sont cumulées sur les étapes suivantes (cf. domaine) : un taux se lit
// étape par étape, pas en additionnant les lignes.
export const VeridianProspectionPerformance: React.FC<Props> = ({ stats, templates, loading, error }) => {
  const { t } = useLingui()

  const rows = useMemo(() => {
    const names = new Map(templates.map((tpl) => [tpl.id, tpl.name]))
    return buildPerformanceRows(stats?.sequences ?? [], (id) => names.get(id) ?? t`Deleted template`)
  }, [stats, templates, t])

  const numeric = (pick: (row: PerformanceRow) => number) => (a: PerformanceRow, b: PerformanceRow) => pick(a) - pick(b)

  return (
    <Card title={t`Campaign performance`} style={{ marginBottom: 24 }}>
      {error && <Alert type="error" showIcon message={t`Unable to load the prospection figures`} style={{ marginBottom: 12 }} />}
      <Table<PerformanceRow>
        size="small"
        pagination={false}
        loading={loading}
        rowKey="key"
        dataSource={rows}
        scroll={{ x: true }}
        rowClassName={(row) => (row.best ? 'veridian-perf-best' : '')}
        columns={[
          {
            title: t`Sequence`,
            dataIndex: 'sequence',
            sorter: (a, b) => a.sequence.localeCompare(b.sequence)
          },
          {
            title: t`Step`,
            dataIndex: 'label',
            sorter: numeric((row) => row.stage)
          },
          {
            title: t`Model`,
            key: 'models',
            render: (_: unknown, row) => (row.models.length > 0 ? row.models.join(' / ') : <Text type="secondary">-</Text>)
          },
          {
            title: t`Enrolled`,
            dataIndex: 'enrolled',
            align: 'right',
            sorter: numeric((row) => row.enrolled),
            render: (v: number) => formatNumber(v)
          },
          {
            title: t`Sent`,
            dataIndex: 'sent',
            align: 'right',
            sorter: numeric((row) => row.sent),
            render: (v: number) => formatNumber(v)
          },
          {
            title: t`Human replies`,
            dataIndex: 'replies',
            align: 'right',
            sorter: numeric((row) => row.replies),
            render: (v: number) => formatNumber(v)
          },
          {
            title: t`Reply rate`,
            key: 'rate',
            sorter: numeric((row) => row.rate ?? -1),
            render: (_: unknown, row) => (
              <span>
                <span data-testid="perf-rate" data-value={row.rate ?? -1}>
                  {row.rate === null ? '-' : formatPercent(row.rate / 100)}
                </span>{' '}
                {row.best ? (
                  <Tag color="green" bordered={false}>
                    {t`Best`}
                  </Tag>
                ) : row.eligible ? null : (
                  <Text type="secondary" style={{ fontSize: 12 }}>
                    {t`Too little data`}
                  </Text>
                )}
              </span>
            )
          },
          {
            title: t`Unsubscribed`,
            dataIndex: 'unsubscribed',
            align: 'right',
            sorter: numeric((row) => row.unsubscribed),
            render: (v: number) => formatNumber(v)
          },
          {
            title: t`Bounces`,
            dataIndex: 'rejected',
            align: 'right',
            sorter: numeric((row) => row.rejected),
            render: (v: number) => formatNumber(v)
          },
          {
            title: t`Left on reply`,
            dataIndex: 'repliedExits',
            align: 'right',
            sorter: numeric((row) => row.repliedExits),
            render: (v: number) => formatNumber(v)
          }
        ]}
      />
      <Text type="secondary" style={{ fontSize: 12, display: 'block', marginTop: 8 }}>
        {t`Reply rate = human replies / sent of the step. Best is named only from ${MIN_SAMPLE_FOR_RATE} sends per step, within the same sequence.`}
      </Text>
    </Card>
  )
}
