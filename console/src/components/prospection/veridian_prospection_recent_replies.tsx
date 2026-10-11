import React from 'react'
import { Alert, Card, Empty, Table, Tooltip, Typography } from 'antd'
import { useLingui } from '@lingui/react/macro'

import dayjs from '../../lib/dayjs'
import type { ProspectionRecentReply, ProspectionStats } from '../../services/api/veridian_prospection'

const { Text } = Typography

interface Props {
  stats?: ProspectionStats
  loading: boolean
  error: boolean
  timezone: string
}

// Les 10 dernières réponses humaines : qui, de quelle séquence, et quand (« il y a 3 jours »
// dans la langue de la console, la date exacte en infobulle, dans le fuseau du tableau de bord).
export const VeridianProspectionRecentReplies: React.FC<Props> = ({ stats, loading, error, timezone }) => {
  const { t } = useLingui()
  const rows = stats?.recent_human_replies ?? []
  const at = (iso: string) => dayjs(iso).tz(timezone || 'Europe/Paris')

  return (
    <Card title={t`Latest human replies`} style={{ marginBottom: 24 }} data-testid="recent-human-replies">
      {error && <Alert type="error" showIcon message={t`Unable to load the prospection figures`} style={{ marginBottom: 12 }} />}
      <Table<ProspectionRecentReply>
        size="small"
        pagination={false}
        loading={loading}
        rowKey={(r) => `${r.contact_email}-${r.replied_at}`}
        dataSource={rows}
        scroll={{ x: true }}
        locale={{ emptyText: <Empty image={Empty.PRESENTED_IMAGE_SIMPLE} description={t`No human reply on this period`} /> }}
        columns={[
          {
            title: t`Company`,
            key: 'company',
            render: (_: unknown, r) => (r.company ? <Text strong>{r.company}</Text> : <Text type="secondary">{t`Unknown company`}</Text>)
          },
          { title: t`Contact`, dataIndex: 'contact_email', key: 'contact' },
          { title: t`Sequence`, dataIndex: 'sequence', key: 'sequence', render: (v: string) => v || '-' },
          {
            title: t`Replied`,
            key: 'replied_at',
            align: 'right',
            render: (_: unknown, r) => (
              <Tooltip title={at(r.replied_at).format('LLL')}>
                <span data-testid="reply-relative-date">{at(r.replied_at).fromNow()}</span>
              </Tooltip>
            )
          }
        ]}
      />
    </Card>
  )
}
