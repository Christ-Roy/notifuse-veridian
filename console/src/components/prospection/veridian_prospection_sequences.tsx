import React from 'react'
import { Alert, Card, Table, Tag, Tooltip, Typography } from 'antd'
import { useLingui } from '@lingui/react/macro'

import type { ProspectionSequence, ProspectionStage, ProspectionStats } from '../../services/api/veridian_prospection'
import { formatNumber } from '../sending_profiles/veridian_profile_labels'

const { Text } = Typography

interface Props {
  stats?: ProspectionStats
  loading: boolean
  error: boolean
}

// Avancement des séquences, étape par étape (J0, J+4, J+10) : combien de contacts ont reçu
// le mail, combien sont en file, combien attendent l'échéance, combien sont sortis après,
// puis les sorties par raison (réponse, rejet, désinscription, exclusion).
export const VeridianProspectionSequences: React.FC<Props> = ({ stats, loading, error }) => {
  const { t } = useLingui()
  const sequences = stats?.sequences ?? []
  const stageCount = sequences.reduce((max, s) => Math.max(max, s.stages.length), 0)

  const stageCell = (stage: ProspectionStage | undefined) => {
    if (!stage) return <Text type="secondary">-</Text>
    const queued = formatNumber(stage.queued)
    const waiting = formatNumber(stage.waiting)
    const left = formatNumber(stage.exited_after)
    return (
      <div data-testid={`stage-${stage.stage}`}>
        <div>
          <strong>{formatNumber(stage.sent)}</strong> <Text type="secondary">{t`sent`}</Text>
        </div>
        <div style={{ fontSize: 12 }}>
          <Text type="secondary">{t`${queued} in the queue, ${waiting} waiting, ${left} left after`}</Text>
        </div>
      </div>
    )
  }

  const stageColumns = Array.from({ length: stageCount }, (_, index) => {
    const label = sequences.map((s) => s.stages[index]?.label).find(Boolean) ?? `#${index + 1}`
    return {
      title: label,
      key: `stage-${index}`,
      render: (_: unknown, row: ProspectionSequence) => stageCell(row.stages[index])
    }
  })

  return (
    <Card title={t`Sequence progress`} style={{ marginBottom: 24 }}>
      {error && <Alert type="error" showIcon message={t`Unable to load the prospection figures`} style={{ marginBottom: 12 }} />}
      <Table<ProspectionSequence>
        size="small"
        pagination={false}
        loading={loading}
        rowKey="automation_id"
        dataSource={sequences}
        scroll={{ x: true }}
        columns={[
          {
            title: t`Sequence`,
            key: 'name',
            render: (_: unknown, row) => (
              <span>
                {row.name}{' '}
                {row.status === 'live' ? <Tag color="green">{t`Live`}</Tag> : <Tag color="orange">{t`Paused`}</Tag>}
              </span>
            )
          },
          { title: t`Enrolled`, dataIndex: 'enrolled', align: 'right', render: (v: number) => formatNumber(v) },
          ...stageColumns,
          {
            title: (
              <Tooltip title={t`Contacts who left the sequence, by reason.`}>
                <span>{t`Exits`}</span>
              </Tooltip>
            ),
            key: 'exits',
            render: (_: unknown, row) => {
              const replied = formatNumber(row.exits.replied)
              const rejected = formatNumber(row.exits.rejected)
              const unsubscribed = formatNumber(row.exits.unsubscribed)
              const excluded = formatNumber(row.exits.excluded)
              return (
                <div data-testid={`exits-${row.automation_id}`}>
                  <div>
                    <strong>{formatNumber(row.exits.total)}</strong>
                  </div>
                  <div style={{ fontSize: 12 }}>
                    <Text type="secondary">
                      {t`${replied} replied, ${rejected} rejected, ${unsubscribed} unsubscribed, ${excluded} excluded`}
                    </Text>
                  </div>
                </div>
              )
            }
          }
        ]}
      />
      <Text type="secondary" style={{ fontSize: 12 }}>
        {t`Sent: contacts who were sent this mail. Excluded: left before any mail was sent (provider excluded on purpose, address filtered out).`}
      </Text>
    </Card>
  )
}
