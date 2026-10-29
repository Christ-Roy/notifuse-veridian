import React from 'react'
import { Alert, Card, Table, Tag, Typography } from 'antd'
import { useLingui } from '@lingui/react/macro'

import type { StockOverview, StockRow, StockStatus } from '../../services/api/veridian_stock'
import { formatNumber, formatPercent } from '../sending_profiles/veridian_profile_labels'
import { restockOrder, sortByUrgency, statusTone } from './veridian_stock_rules'

const { Text } = Typography

interface Props {
  overview?: StockOverview
  loading: boolean
  error: boolean
}

const TAG_COLOR = { good: 'green', warn: 'orange', bad: 'red', muted: 'default' } as const

// Stock par fournisseur destinataire : combien de contacts jamais contactés il reste, pour combien de
// jours d'envoi, et combien aller chercher. Le serveur calcule tout (même définition que le script
// stock-par-classe.py) ; cet écran ne recalcule rien.
export const VeridianProspectionStock: React.FC<Props> = ({ overview, loading, error }) => {
  const { t } = useLingui()

  const statusLabel = (status: StockStatus): string => {
    switch (status) {
      case 'ok':
        return t`Covered`
      case 'bas':
        return t`Low`
      case 'critique':
        return t`Critical`
      case 'sans_plafond':
        return t`No capacity`
      default:
        return t`Idle`
    }
  }

  const days = (value: number | null) => (value === null ? '-' : t`${formatNumber(value)} d`)
  // Un serveur plus ancien (ou une réponse vide) ne doit jamais casser le tableau de bord : tout est optionnel.
  const classes = Array.isArray(overview?.classes) ? overview.classes : []
  const rows = sortByUrgency(classes)
  const toSource = restockOrder(classes)

  return (
    <Card title={t`Stock by recipient provider`} style={{ marginBottom: 24 }} data-testid="stock-by-provider">
      {error && <Alert type="warning" showIcon style={{ marginBottom: 12 }} message={t`The stock could not be read.`} />}
      {overview && overview.config_source === 'none' && (
        <Alert
          type="info"
          showIcon
          style={{ marginBottom: 12 }}
          message={t`No list of enrolled contacts is defined: set it with the stock:config command.`}
        />
      )}
      <Table<StockRow>
        size="small"
        pagination={false}
        loading={loading}
        rowKey="class"
        dataSource={rows}
        locale={{ emptyText: t`No contact in stock.` }}
        scroll={{ x: true }}
        columns={[
          { title: t`Provider`, dataIndex: 'label', key: 'provider' },
          { title: t`Never mailed`, key: 'unsent', align: 'right', render: (_: unknown, r) => formatNumber(r.unsent) },
          { title: t`Capacity per day`, key: 'capacity', align: 'right', render: (_: unknown, r) => formatNumber(r.capacity_nominal) },
          { title: t`Days of coverage`, key: 'days', align: 'right', render: (_: unknown, r) => days(r.days_unsent) },
          {
            title: t`State`,
            key: 'state',
            render: (_: unknown, r) => <Tag color={TAG_COLOR[statusTone(r.status)]}>{statusLabel(r.status)}</Tag>
          },
          {
            title: t`To source`,
            key: 'need',
            align: 'right',
            render: (_: unknown, r) => (r.restock_need > 0 ? <Text strong>{formatNumber(r.restock_need)}</Text> : <Text type="secondary">0</Text>)
          },
          { title: t`Reply rate`, key: 'reply', align: 'right', render: (_: unknown, r) => formatPercent(r.reply_rate_smoothed) }
        ]}
      />
      {overview && toSource.length > 0 && (
        <div style={{ marginTop: 12 }}>
          <Text type="secondary">
            {t`To source for ${overview.target_days} days, best repliers first:`}{' '}
            {toSource.map((r) => `${r.label} ${formatNumber(r.restock_need)}`).join(', ')}
          </Text>
        </div>
      )}
    </Card>
  )
}
