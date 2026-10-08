import React from 'react'
import { Card, Table, Tag, Typography } from 'antd'
import { useLingui } from '@lingui/react/macro'

import type { EmailProfilesOverview } from '../../services/api/veridian_email_profiles'
import { formatPercent, useProfileLabels } from '../sending_profiles/veridian_profile_labels'
import { reputationCouples, type ReputationCouple } from './veridian_prospection_rules'

const { Text } = Typography

interface Props {
  overview?: EmailProfilesOverview
  loading: boolean
}

// Réputation par fournisseur destinataire : seulement les couples (relais, fournisseur)
// ralentis ou arrêtés. Le plan du serveur fait foi (même calcul que le fusible du worker).
export const VeridianProspectionReputation: React.FC<Props> = ({ overview, loading }) => {
  const { t } = useLingui()
  const labels = useProfileLabels()
  const couples = reputationCouples(overview)

  return (
    <Card title={t`Reputation by recipient provider`} style={{ marginBottom: 24 }}>
      <Table<ReputationCouple>
        size="small"
        pagination={false}
        loading={loading}
        rowKey={(row) => `${row.profileId}:${row.issue.class}`}
        dataSource={couples}
        locale={{ emptyText: t`No provider is slowed or stopped.` }}
        scroll={{ x: true }}
        columns={[
          { title: t`Relay`, dataIndex: 'profileName', key: 'relay' },
          { title: t`Provider`, key: 'class', render: (_: unknown, row) => labels.classLabel(row.issue.class) },
          {
            title: t`State`,
            key: 'state',
            render: (_: unknown, row) => {
              const factor = row.issue.factor
              return row.issue.stopped ? (
                <Tag color="red">{t`Stopped`}</Tag>
              ) : (
                <Tag color="orange">{t`Slowed ÷${factor}`}</Tag>
              )
            }
          },
          {
            title: t`Reason`,
            key: 'reason',
            render: (_: unknown, row) => labels.reasonText(row.issue)
          },
          {
            title: t`Sent over 7 days`,
            key: 'sent7d',
            align: 'right',
            render: (_: unknown, row) => <Text type="secondary">{row.issue.sent7d}</Text>
          },
          {
            title: t`Triggering rate`,
            key: 'rate',
            align: 'right',
            render: (_: unknown, row) => (row.issue.rate === null ? '-' : formatPercent(row.issue.rate))
          }
        ]}
      />
    </Card>
  )
}
