import React from 'react'
import { Alert, Card, Table, Tag, Tooltip, Typography } from 'antd'
import { useLingui } from '@lingui/react/macro'

import type { ProspectionSegment, ProspectionSequence, ProspectionStats } from '../../services/api/veridian_prospection'
import { formatNumber, formatPercent } from '../sending_profiles/veridian_profile_labels'

const { Text } = Typography

interface Props {
  stats?: ProspectionStats
  loading: boolean
  error: boolean
}

const rateText = (rate: number | null): string => (rate === null ? '-' : formatPercent(rate))

// Réponses humaines et taux de réponse par séquence, puis par segment (liste), avec le
// stock restant. Les réponses automatiques (absence, accusé de réception) sont comptées
// à part et n'entrent jamais dans le taux.
export const VeridianProspectionReplies: React.FC<Props> = ({ stats, loading, error }) => {
  const { t } = useLingui()
  const statusTag = (status: string) => {
    if (status === 'live') return <Tag color="green">{t`Live`}</Tag>
    if (status === 'paused') return <Tag color="orange">{t`Paused`}</Tag>
    return <Tag>{status}</Tag>
  }

  return (
    <>
      <Card title={t`Replies by sequence`} style={{ marginBottom: 24 }}>
        {error && <Alert type="error" showIcon message={t`Unable to load the prospection figures`} style={{ marginBottom: 12 }} />}
        <Table<ProspectionSequence>
          size="small"
          pagination={false}
          loading={loading}
          rowKey="automation_id"
          dataSource={stats?.sequences ?? []}
          scroll={{ x: true }}
          columns={[
            {
              title: t`Sequence`,
              key: 'name',
              render: (_: unknown, row) => (
                <span>
                  {row.name} {statusTag(row.status)}
                </span>
              )
            },
            {
              title: (
                <Tooltip title={t`Distinct contacts who were sent a mail during the period.`}>
                  <span>{t`Contacts reached`}</span>
                </Tooltip>
              ),
              dataIndex: 'sent_contacts',
              align: 'right',
              render: (v: number) => formatNumber(v)
            },
            {
              title: t`Human replies`,
              dataIndex: 'replies_human',
              align: 'right',
              render: (v: number) => <strong>{formatNumber(v)}</strong>
            },
            {
              title: (
                <Tooltip title={t`Human replies over contacts reached during the period.`}>
                  <span>{t`Reply rate`}</span>
                </Tooltip>
              ),
              dataIndex: 'reply_rate_human',
              align: 'right',
              render: (v: number | null) => rateText(v)
            },
            {
              title: (
                <Tooltip title={t`Out-of-office and acknowledgements: counted apart, never in the rate.`}>
                  <span>{t`Automatic replies`}</span>
                </Tooltip>
              ),
              dataIndex: 'replies_auto',
              align: 'right',
              render: (v: number) => <Text type="secondary">{formatNumber(v)}</Text>
            }
          ]}
        />
        <Text type="secondary" style={{ fontSize: 12 }}>
          {t`A contact enrolled in two sequences counts in both: the rows do not add up.`}
        </Text>
      </Card>

      <Card title={t`Segments (lists): replies and remaining stock`} style={{ marginBottom: 24 }}>
        <Table<ProspectionSegment>
          size="small"
          pagination={false}
          loading={loading}
          rowKey="list_id"
          dataSource={stats?.segments ?? []}
          scroll={{ x: true }}
          columns={[
            { title: t`Segment`, dataIndex: 'name', key: 'name' },
            {
              title: t`Active contacts`,
              dataIndex: 'active',
              align: 'right',
              render: (v: number) => formatNumber(v)
            },
            {
              title: (
                <Tooltip title={t`Active contacts who have not been sent any mail yet.`}>
                  <span>{t`Remaining stock`}</span>
                </Tooltip>
              ),
              dataIndex: 'never_contacted',
              align: 'right',
              render: (v: number) => <strong>{formatNumber(v)}</strong>
            },
            {
              title: t`Contacts reached`,
              dataIndex: 'sent_contacts',
              align: 'right',
              render: (v: number) => formatNumber(v)
            },
            {
              title: t`Human replies`,
              dataIndex: 'replies_human',
              align: 'right',
              render: (v: number) => <strong>{formatNumber(v)}</strong>
            },
            {
              title: t`Reply rate`,
              dataIndex: 'reply_rate_human',
              align: 'right',
              render: (v: number | null) => rateText(v)
            },
            {
              title: t`Automatic replies`,
              dataIndex: 'replies_auto',
              align: 'right',
              render: (v: number) => <Text type="secondary">{formatNumber(v)}</Text>
            },
            {
              title: t`Rejected`,
              dataIndex: 'bounced',
              align: 'right',
              render: (v: number) => formatNumber(v)
            },
            {
              title: t`Unsubscribed`,
              dataIndex: 'unsubscribed',
              align: 'right',
              render: (v: number) => formatNumber(v)
            }
          ]}
        />
      </Card>
    </>
  )
}
