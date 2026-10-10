import { Collapse, Table, Tooltip, Typography } from 'antd'
import { useLingui } from '@lingui/react/macro'

import type { EmailProfilePlan } from '../../services/api/veridian_email_profiles'
import { formatNumber, useProfileLabels } from './veridian_profile_labels'
import { capacityBar, capacityLimit, capacityRows } from './veridian_profile_rules'

const { Text } = Typography

// Lot 6 (10/10/2026) : la capacité du jour d'un profil, en barre empilée
// (envoyé / restant / inutilisable faute de stock) avec la limite en clair, et le détail par
// fournisseur dépliable. Tous les nombres viennent du plan serveur (VeridianEffectivePlan).
const COLORS = { sent: '#1677ff', remaining: '#52c41a', unusable: '#d9d9d9' }

interface Props {
  plan: EmailProfilePlan
}

export function VeridianCapacityBlock({ plan }: Props) {
  const { t } = useLingui()
  const labels = useProfileLabels()
  const capacity = plan.capacity
  if (!capacity) return null
  const bar = capacityBar(plan)
  const limit = capacityLimit(plan)
  const rows = capacityRows(plan)
  const sentLabel = formatNumber(capacity.sent)
  const closedToday = capacity.window_minutes_left <= 0 && plan.window.configured

  const pct = (value: number) => (bar && bar.total > 0 ? (value / bar.total) * 100 : 0)
  const remainingLabel = bar ? formatNumber(bar.remaining) : ''
  const unusableLabel = bar ? formatNumber(bar.unusable) : ''
  const totalLabel = bar ? formatNumber(bar.total) : ''
  const usefulLabel = bar ? formatNumber(bar.useful) : ''
  const fullDay = capacity.full_day_configured === null ? '' : formatNumber(capacity.full_day_configured)

  return (
    <div data-testid="capacity-block">
      <div data-testid="capacity-summary">
        {bar ? (
          <Text>{t`${sentLabel} sent / ${totalLabel} capacity today`}</Text>
        ) : (
          <Text>{t`${sentLabel} sent, no capacity limit`}</Text>
        )}
        {bar && bar.stockKnown && bar.unusable > 0 && (
          <Text type="secondary"> ({t`${usefulLabel} usable with the contacts due today`})</Text>
        )}
      </div>
      {bar && (
        <div
          data-testid="capacity-bar"
          role="img"
          aria-label={t`${sentLabel} sent, ${remainingLabel} still to send, ${unusableLabel} unusable for lack of contacts`}
          style={{ display: 'flex', height: 10, borderRadius: 5, overflow: 'hidden', background: '#f0f0f0', margin: '6px 0' }}
        >
          <Tooltip title={t`Sent today`}>
            <div data-testid="capacity-sent" style={{ width: `${pct(bar.sent)}%`, background: COLORS.sent }} />
          </Tooltip>
          <Tooltip title={t`Still to send`}>
            <div data-testid="capacity-remaining" style={{ width: `${pct(bar.remaining)}%`, background: COLORS.remaining }} />
          </Tooltip>
          <Tooltip title={t`Unusable: no contact due for these providers`}>
            <div data-testid="capacity-unusable" style={{ width: `${pct(bar.unusable)}%`, background: COLORS.unusable }} />
          </Tooltip>
        </div>
      )}
      {bar && (
        <div data-testid="capacity-legend" style={{ fontSize: 12 }}>
          <span style={{ color: COLORS.sent }}>■</span> {t`Sent`} {sentLabel}
          {'  '}
          <span style={{ color: COLORS.remaining }}>■</span> {t`Still to send`} {remainingLabel}
          {'  '}
          <span style={{ color: COLORS.unusable }}>■</span> {t`Unusable`} {unusableLabel}
        </div>
      )}
      <div data-testid="limiting-factor">
        <Text type="secondary">{t`Limited by`}: </Text>
        <Text>{labels.capacityLimitText(limit)}</Text>
      </div>
      {capacity.stock_known && capacity.stock_limiting_class && (
        <div data-testid="stock-limit">
          <Text type="secondary">{t`Contacts`}: </Text>
          <Text>{labels.stockLimitText(capacity.stock_limiting_class)}</Text>
        </div>
      )}
      {!capacity.stock_known && (
        <div data-testid="stock-unknown">
          <Text type="secondary">{t`Contacts due today: not measured`}</Text>
        </div>
      )}
      {closedToday && capacity.full_day_configured !== null && (
        <div data-testid="typical-day">
          <Text type="secondary">{t`A normal open day: ${fullDay} sends`}</Text>
        </div>
      )}
      <Collapse
        ghost
        size="small"
        items={[
          {
            key: 'detail',
            label: t`Detail by provider`,
            children: (
              <Table
                size="small"
                pagination={false}
                data-testid="capacity-detail"
                rowKey="class"
                dataSource={rows}
                columns={[
                  { title: t`Provider`, dataIndex: 'class', render: (cls: string) => labels.classLabel(cls) },
                  { title: t`Sent`, dataIndex: 'sent', align: 'right', render: (v: number) => formatNumber(v) },
                  {
                    title: t`Still to send`,
                    dataIndex: 'remaining',
                    align: 'right',
                    render: (v: number | null) => (v === null ? '∞' : formatNumber(v))
                  },
                  {
                    title: t`Unusable`,
                    dataIndex: 'unusable',
                    align: 'right',
                    render: (v: number | null) => (v === null ? '' : formatNumber(v))
                  },
                  {
                    title: t`Contacts due`,
                    dataIndex: 'stock',
                    align: 'right',
                    render: (v: number | null) => (v === null ? '' : formatNumber(v))
                  },
                  { title: t`Limit`, dataIndex: 'limiter', render: (v: string) => labels.limiterText(v) }
                ]}
              />
            )
          }
        ]}
      />
    </div>
  )
}
