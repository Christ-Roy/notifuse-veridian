import { useEffect, useMemo, useState } from 'react'
import { Button, Popconfirm, Space, Table, Tag, Typography } from 'antd'
import type { ColumnsType } from 'antd/es/table'
import { useLingui } from '@lingui/react/macro'

import type { QueueGroup } from '../../services/api/veridian_queue_explain'
import { formatNumber } from '../sending_profiles/veridian_profile_labels'
import { formatDateTime, useQueueLabels } from './veridian_queue_labels'
import { buildQueueTree, defaultExpandedKeys, type QueueTreeRow } from './veridian_queue_rules'

const { Text } = Typography

interface Props {
  groups: QueueGroup[]
  canWrite: boolean
  recomputingKey: string | null
  onOpenEntry: (entryId: string) => void
  onRecompute: (row: QueueTreeRow) => void
}

const shortId = (id: string) => (id.length > 8 ? id.slice(0, 8) : id)

// Tableau groupé et dépliable : automation > nœud > raison > profil. La feuille (profil)
// porte l'échantillon d'entrées ; chaque ligne peut déclencher un recalcul borné.
export function VeridianQueueGroups({ groups, canWrite, recomputingKey, onOpenEntry, onRecompute }: Props) {
  const { t } = useLingui()
  const labels = useQueueLabels()
  const tree = useMemo(() => buildQueueTree(groups), [groups])
  const [expanded, setExpanded] = useState<string[]>([])

  useEffect(() => {
    setExpanded(defaultExpandedKeys(tree))
  }, [tree])

  const nameOf = (row: QueueTreeRow) => {
    switch (row.level) {
      case 'automation':
        return <Text strong>{row.name || row.value || '—'}</Text>
      case 'node': {
        const node = row.value
        return <Text>{node ? t`Node ${node}` : t`No node`}</Text>
      }
      case 'reason': {
        const detail = labels.reasonDetailLabel(row.value, row.reason_detail)
        return (
          <Space size={4}>
            <Text>{labels.reasonLabel(row.value)}</Text>
            {detail && <Tag>{detail}</Tag>}
          </Space>
        )
      }
      default:
        return <Text>{row.name || row.value || t`No profile`}</Text>
    }
  }

  const columns: ColumnsType<QueueTreeRow> = [
    { title: t`Group`, key: 'name', render: (_, row) => nameOf(row) },
    {
      title: t`Entries`,
      key: 'count',
      align: 'right',
      render: (_, row) => formatNumber(row.count)
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
    },
    {
      title: t`Sample`,
      key: 'sample',
      render: (_, row) =>
        row.sample_entry_ids.length === 0 ? null : (
          <Space size={2} wrap>
            {row.sample_entry_ids.map((id) => (
              <Button key={id} type="link" size="small" onClick={() => onOpenEntry(id)} title={id}>
                {shortId(id)}
              </Button>
            ))}
          </Space>
        )
    }
  ]

  if (canWrite) {
    columns.push({
      title: '',
      key: 'actions',
      render: (_, row) =>
        row.automation_id ? (
          <Popconfirm
            title={t`Recompute this group?`}
            description={t`Up to 5000 entries get their next attempt and recorded reason cleared. Nothing is deleted and no attempt is consumed.`}
            okText={t`Recompute`}
            cancelText={t`Cancel`}
            onConfirm={() => onRecompute(row)}
          >
            <Button size="small" loading={recomputingKey === row.key}>
              {t`Recompute`}
            </Button>
          </Popconfirm>
        ) : null
    })
  }

  return (
    <Table<QueueTreeRow>
      size="small"
      rowKey="key"
      columns={columns}
      dataSource={tree}
      pagination={false}
      expandable={{ expandedRowKeys: expanded, onExpandedRowsChange: (keys) => setExpanded(keys.map(String)) }}
      scroll={{ x: 'max-content' }}
    />
  )
}
