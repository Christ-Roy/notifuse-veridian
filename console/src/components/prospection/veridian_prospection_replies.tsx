import React from 'react'
import { Alert, Card, Table, Tag, Tooltip, Typography } from 'antd'
import { useLingui } from '@lingui/react/macro'

import type { ProspectionSegment, ProspectionSequence, ProspectionStats } from '../../services/api/veridian_prospection'
import { formatNumber, formatPercent } from '../sending_profiles/veridian_profile_labels'
import { isLowBase, unattachedReplies } from './veridian_prospection_rules'

const { Text } = Typography

interface Props {
  stats?: ProspectionStats
  loading: boolean
  error: boolean
  // Totaux globaux de la fenêtre (messages.replyStats) : ils servent à la ligne « Non rattachées »
  totalHuman?: number
  totalAuto?: number
}

// Ligne du tableau par séquence : une séquence vivante, ou la ligne « Non rattachées »
type SequenceRow = Pick<ProspectionSequence, 'automation_id' | 'name' | 'status' | 'replies_human' | 'replies_auto'> & {
  sent_contacts: number | null
  reply_rate_human: number | null
  unattached?: boolean
}

// Réponses humaines et taux de réponse par séquence, puis par segment (liste), avec le
// stock restant. Les réponses automatiques (absence, accusé de réception) sont comptées
// à part et n'entrent jamais dans le taux.
export const VeridianProspectionReplies: React.FC<Props> = ({ stats, loading, error, totalHuman, totalAuto }) => {
  const { t } = useLingui()

  // Un taux sur une petite base ne dit rien (2 sur 2 = 100 %) : grisé, avec « base faible »
  // sous 30 contacts joints. Le chiffre reste affiché, il ne se lit pas comme un résultat.
  const rateCell = (rate: number | null, reached: number | null) => {
    if (rate === null) return '-'
    if (!isLowBase(reached)) return formatPercent(rate)
    return (
      <Tooltip title={t`Fewer than 30 contacts reached: the rate is not significant.`}>
        <span data-testid="low-base-rate">
          <Text type="secondary">{formatPercent(rate)}</Text>{' '}
          <Tag>{t`low base`}</Tag>
        </span>
      </Tooltip>
    )
  }

  const sequenceRows: SequenceRow[] = [...(stats?.sequences ?? [])]
  if (stats && totalHuman !== undefined) {
    const human = unattachedReplies(totalHuman, stats.sequences)
    const auto = unattachedReplies(totalAuto ?? 0, stats.sequences.map((s) => ({ replies_human: s.replies_auto })))
    if (human > 0 || auto > 0) {
      sequenceRows.push({
        automation_id: '__unattached',
        name: t`Unattached`,
        status: '',
        replies_human: human,
        replies_auto: auto,
        sent_contacts: null,
        reply_rate_human: null,
        unattached: true
      })
    }
  }

  const statusTag = (status: string) => {
    if (status === 'live') return <Tag color="green">{t`Live`}</Tag>
    if (status === 'paused') return <Tag color="orange">{t`Paused`}</Tag>
    return <Tag>{status}</Tag>
  }

  return (
    <>
      <Card title={t`Replies by sequence`} style={{ marginBottom: 24 }}>
        {error && <Alert type="error" showIcon message={t`Unable to load the prospection figures`} style={{ marginBottom: 12 }} />}
        <Table<SequenceRow>
          size="small"
          pagination={false}
          loading={loading}
          rowKey="automation_id"
          dataSource={sequenceRows}
          scroll={{ x: true }}
          columns={[
            {
              title: t`Sequence`,
              key: 'name',
              render: (_: unknown, row) =>
                row.unattached ? (
                  <Tooltip title={t`Replies counted in the total that no live sequence carries (contacts of deleted sequences, matching by sender).`}>
                    <span data-testid="replies-unattached">
                      <Text italic>{row.name}</Text>
                    </span>
                  </Tooltip>
                ) : (
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
              render: (v: number | null) => (v === null ? '-' : formatNumber(v))
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
              render: (v: number | null, row) => rateCell(v, row.sent_contacts)
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
              render: (v: number | null, row) => rateCell(v, row.sent_contacts)
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
