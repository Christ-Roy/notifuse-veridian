import { useMemo } from 'react'
import { Button, Table } from 'antd'
import type { ColumnsType } from 'antd/es/table'
import { useLingui } from '@lingui/react/macro'

import type { QueueGroup } from '../../services/api/veridian_queue_explain'
import { formatNumber } from '../sending_profiles/veridian_profile_labels'
import { formatDateTime, useQueueLabels } from './veridian_queue_labels'
import { reasonTotals, type ReasonTotal } from './veridian_queue_rules'

interface Props {
  groups: QueueGroup[]
  total: number
  // Clique sur une raison : la page filtre dessus.
  onPickReason: (reason: string) => void
}

// « Pourquoi ça n'envoie pas » en une table : une ligne par raison, avec compteur, jamais
// examinées, plus ancienne et fourchette de prochaine tentative.
export function VeridianQueueReasons({ groups, total, onPickReason }: Props) {
  const { t } = useLingui()
  const labels = useQueueLabels()
  const rows = useMemo(() => reasonTotals(groups), [groups])

  const columns: ColumnsType<ReasonTotal> = [
    {
      title: t`Reason`,
      key: 'reason',
      render: (_, row) => (
        <Button type="link" style={{ padding: 0 }} onClick={() => onPickReason(row.reason)}>
          {labels.reasonLabel(row.reason)}
        </Button>
      )
    },
    {
      title: t`Entries`,
      key: 'count',
      align: 'right',
      render: (_, row) => formatNumber(row.count)
    },
    {
      title: t`Share`,
      key: 'share',
      align: 'right',
      render: (_, row) => (total > 0 ? `${Math.round((row.count / total) * 100)} %` : '—')
    },
    {
      title: t`Never examined`,
      key: 'never',
      align: 'right',
      render: (_, row) => formatNumber(row.never_examined)
    },
    { title: t`Oldest`, key: 'oldest', render: (_, row) => formatDateTime(row.oldest) },
    {
      title: t`Next attempt`,
      key: 'next',
      render: (_, row) =>
        row.next_min && row.next_max && row.next_min !== row.next_max
          ? `${formatDateTime(row.next_min)} → ${formatDateTime(row.next_max)}`
          : formatDateTime(row.next_min ?? row.next_max)
    }
  ]

  return (
    <Table<ReasonTotal>
      size="small"
      rowKey="reason"
      columns={columns}
      dataSource={rows}
      pagination={false}
      scroll={{ x: 'max-content' }}
      data-testid="queue-reasons"
    />
  )
}
