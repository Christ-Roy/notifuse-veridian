import { useCallback, useEffect, useRef, useState } from 'react'
import { Alert, Button, Empty, Input, Select, Skeleton, Space, Table, Tag, Typography } from 'antd'
import type { ColumnsType } from 'antd/es/table'
import { useLingui } from '@lingui/react/macro'

import {
  decisionsService,
  QUEUE_REASON_CODES,
  type QueueDecision
} from '../../services/api/veridian_queue_explain'
import { formatDateTime, useQueueLabels } from './veridian_queue_labels'
import { outcomeColor } from './veridian_queue_rules'
import { VeridianQueueTrace } from './veridian_queue_trace'

const { Text } = Typography

const OUTCOMES = ['sent', 'deferred', 'failed', 'discarded', 'exited', 'recomputed'] as const
const PAGE_SIZE = 50

interface Props {
  workspaceId: string
  automationId?: string
  // Incrémenté par la page pour forcer un rechargement (bouton Actualiser)
  refreshToken: number
  onOpenEntry: (entryId: string) => void
}

// Journal des décisions : e-mail / raison / issue, pagination par curseur, trace dépliable.
export function VeridianQueueDecisions({ workspaceId, automationId, refreshToken, onOpenEntry }: Props) {
  const { t } = useLingui()
  const labels = useQueueLabels()
  const [email, setEmail] = useState('')
  const [reason, setReason] = useState<string | undefined>()
  const [outcome, setOutcome] = useState<string | undefined>()
  const [rows, setRows] = useState<QueueDecision[]>([])
  const [cursor, setCursor] = useState('')
  const [loading, setLoading] = useState(true)
  const [loadingMore, setLoadingMore] = useState(false)
  const [error, setError] = useState<string | null>(null)
  // Une réponse tardive d'une requête périmée ne remplace jamais la dernière demandée.
  const seq = useRef(0)

  const fetchPage = useCallback(
    async (after: string) => {
      return decisionsService.list({
        workspace_id: workspaceId,
        automation_id: automationId,
        email: email.trim() || undefined,
        reason,
        outcome,
        limit: PAGE_SIZE,
        cursor: after || undefined,
        trace: true
      })
    },
    [workspaceId, automationId, email, reason, outcome]
  )

  const reload = useCallback(async () => {
    const mine = ++seq.current
    setLoading(true)
    try {
      const response = await fetchPage('')
      if (mine !== seq.current) return
      setRows(response.decisions)
      setCursor(response.next_cursor || '')
      setError(null)
    } catch (err) {
      if (mine !== seq.current) return
      setError(err instanceof Error ? err.message : t`Could not load the decisions`)
    } finally {
      if (mine === seq.current) setLoading(false)
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps -- t est stable pour une langue donnée
  }, [fetchPage])

  useEffect(() => {
    void reload()
  }, [reload, refreshToken])

  const loadMore = async () => {
    setLoadingMore(true)
    try {
      const response = await fetchPage(cursor)
      setRows((prev) => [...prev, ...response.decisions])
      setCursor(response.next_cursor || '')
    } catch (err) {
      setError(err instanceof Error ? err.message : t`Could not load the decisions`)
    } finally {
      setLoadingMore(false)
    }
  }

  const columns: ColumnsType<QueueDecision> = [
    { title: t`When`, key: 'at', render: (_, d) => formatDateTime(d.at) },
    { title: t`Contact`, key: 'email', dataIndex: 'contact_email' },
    {
      title: t`Outcome`,
      key: 'outcome',
      render: (_, d) => <Tag color={outcomeColor(d.outcome)}>{labels.decisionOutcomeLabel(d.outcome)}</Tag>
    },
    {
      title: t`Reason`,
      key: 'reason',
      render: (_, d) => {
        if (!d.reason) return '—'
        const detail = labels.reasonDetailLabel(d.reason, d.detail)
        return detail ? `${labels.reasonLabel(d.reason)} (${detail})` : labels.reasonLabel(d.reason)
      }
    },
    { title: t`Profile`, key: 'profile', render: (_, d) => d.profile_name || '—' },
    { title: t`Until`, key: 'until', render: (_, d) => formatDateTime(d.until) },
    {
      title: '',
      key: 'entry',
      render: (_, d) =>
        d.entry_id ? (
          <Button type="link" size="small" onClick={() => onOpenEntry(d.entry_id)}>
            {t`Entry`}
          </Button>
        ) : null
    }
  ]

  return (
    <Space direction="vertical" size="middle" style={{ width: '100%' }}>
      <Space wrap>
        <Input.Search
          allowClear
          placeholder={t`Contact e-mail`}
          style={{ width: 260 }}
          onSearch={(value) => setEmail(value)}
        />
        <Select
          allowClear
          placeholder={t`Reason`}
          style={{ width: 280 }}
          value={reason}
          onChange={(value) => setReason(value)}
          options={QUEUE_REASON_CODES.map((code) => ({ value: code, label: labels.reasonLabel(code) }))}
        />
        <Select
          allowClear
          placeholder={t`Outcome`}
          style={{ width: 200 }}
          value={outcome}
          onChange={(value) => setOutcome(value)}
          options={OUTCOMES.map((code) => ({ value: code, label: labels.decisionOutcomeLabel(code) }))}
        />
      </Space>

      {error && <Alert type="error" showIcon message={error} />}
      {loading && rows.length === 0 && <Skeleton active />}
      {!loading && !error && rows.length === 0 && <Empty description={t`No decision matches these filters`} />}

      {rows.length > 0 && (
        <Table<QueueDecision>
          size="small"
          rowKey="id"
          columns={columns}
          dataSource={rows}
          pagination={false}
          scroll={{ x: 'max-content' }}
          expandable={{
            rowExpandable: (d) => !!d.trace,
            expandedRowRender: (d) => (d.trace ? <VeridianQueueTrace trace={d.trace} /> : null)
          }}
        />
      )}

      {cursor && (
        <Button onClick={() => void loadMore()} loading={loadingMore}>
          {t`Load more`}
        </Button>
      )}
      {rows.length > 0 && !cursor && <Text type="secondary">{t`End of the log`}</Text>}
    </Space>
  )
}
