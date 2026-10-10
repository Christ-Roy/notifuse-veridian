import { useCallback, useEffect, useState } from 'react'
import { Alert, App, Button, Descriptions, Drawer, Popconfirm, Skeleton, Space, Tag, Typography } from 'antd'
import { useLingui } from '@lingui/react/macro'

import {
  queueExitService,
  queueExplainService,
  type QueueEntryDetail
} from '../../services/api/veridian_queue_explain'
import { formatDateTime, useQueueLabels } from './veridian_queue_labels'
import { outcomeColor } from './veridian_queue_rules'
import { VeridianQueueTrace } from './veridian_queue_trace'

const { Text, Title } = Typography

interface Props {
  workspaceId: string
  entryId: string | null
  canWrite: boolean
  onClose: () => void
  // Appelé après une action qui a changé la file : la page recharge.
  onChanged: () => void
}

// Tiroir d'une entrée de la file : état, horodatages, dernière décision gate par gate.
export function VeridianQueueEntryDrawer({ workspaceId, entryId, canWrite, onClose, onChanged }: Props) {
  const { t } = useLingui()
  const { message } = App.useApp()
  const labels = useQueueLabels()
  const [entry, setEntry] = useState<QueueEntryDetail | null>(null)
  const [loading, setLoading] = useState(false)
  const [error, setError] = useState<string | null>(null)
  const [busy, setBusy] = useState<'recompute' | 'exit' | null>(null)

  const load = useCallback(async () => {
    if (!entryId) return
    setLoading(true)
    try {
      const response = await queueExplainService.explain({ workspace_id: workspaceId, entry_id: entryId })
      if (!response.entry) {
        setEntry(null)
        setError(t`This entry no longer exists.`)
      } else {
        setEntry(response.entry)
        setError(null)
      }
    } catch (err) {
      setEntry(null)
      setError(err instanceof Error ? err.message : t`Could not load the entry`)
    } finally {
      setLoading(false)
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps -- t est stable pour une langue donnée
  }, [workspaceId, entryId])

  useEffect(() => {
    setEntry(null)
    setError(null)
    void load()
  }, [load])

  const recompute = async () => {
    if (!entry) return
    setBusy('recompute')
    try {
      const done = await queueExplainService.recompute({
        workspace_id: workspaceId,
        automation_id: entry.automation_id,
        entry_ids: [entry.id],
        limit: 1
      })
      const recomputed = done.recomputed
      message.success(t`Entries recomputed: ${recomputed}`)
      onChanged()
      await load()
    } catch (err) {
      message.error(err instanceof Error ? err.message : t`The action failed`)
    } finally {
      setBusy(null)
    }
  }

  const exitContact = async () => {
    if (!entry) return
    setBusy('exit')
    try {
      await queueExitService.exitContact({
        workspace_id: workspaceId,
        automation_id: entry.automation_id,
        email: entry.contact_email
      })
      message.success(t`Contact removed from the automation`)
      onChanged()
      onClose()
    } catch (err) {
      message.error(err instanceof Error ? err.message : t`The action failed`)
    } finally {
      setBusy(null)
    }
  }

  const decision = entry?.last_decision ?? null

  return (
    <Drawer
      title={t`Queue entry`}
      open={!!entryId}
      onClose={onClose}
      width={760}
      destroyOnClose
      extra={
        <Button onClick={() => void load()} loading={loading}>
          {t`Refresh`}
        </Button>
      }
    >
      {loading && !entry && <Skeleton active />}
      {error && <Alert type="error" showIcon message={error} />}
      {entry && (
        <Space direction="vertical" size="large" style={{ width: '100%' }}>
          <Descriptions column={2} size="small" bordered>
            <Descriptions.Item label={t`Contact`}>{entry.contact_email}</Descriptions.Item>
            <Descriptions.Item label={t`Status`}>{labels.entryStatusLabel(entry.status)}</Descriptions.Item>
            <Descriptions.Item label={t`Automation`}>{entry.automation_name || entry.automation_id}</Descriptions.Item>
            <Descriptions.Item label={t`Node`}>{entry.node_id || '—'}</Descriptions.Item>
            <Descriptions.Item label={t`Profile`}>{entry.profile_name || '—'}</Descriptions.Item>
            <Descriptions.Item label={t`Recipient class`}>{entry.class || '—'}</Descriptions.Item>
            <Descriptions.Item label={t`Reason`}>
              {entry.reason ? labels.reasonLabel(entry.reason) : '—'}
              {entry.reason_detail ? ` (${labels.reasonDetailLabel(entry.reason, entry.reason_detail)})` : ''}
            </Descriptions.Item>
            <Descriptions.Item label={t`Attempts`}>
              {entry.attempts} / {entry.max_attempts}
            </Descriptions.Item>
            <Descriptions.Item label={t`Created`}>{formatDateTime(entry.created_at)}</Descriptions.Item>
            <Descriptions.Item label={t`Next attempt`}>{formatDateTime(entry.next_retry_at)}</Descriptions.Item>
            <Descriptions.Item label={t`First examined`}>{formatDateTime(entry.first_examined_at)}</Descriptions.Item>
            <Descriptions.Item label={t`Last examined`}>{formatDateTime(entry.last_examined_at)}</Descriptions.Item>
            <Descriptions.Item label={t`Deferred at`}>{formatDateTime(entry.deferred_at)}</Descriptions.Item>
            <Descriptions.Item label={t`Deferred until`}>{formatDateTime(entry.defer_until)}</Descriptions.Item>
            <Descriptions.Item label={t`Times deferred`}>{entry.defer_count}</Descriptions.Item>
            <Descriptions.Item label={t`Last error`}>{entry.last_error || '—'}</Descriptions.Item>
          </Descriptions>

          {canWrite && (
            <Space>
              <Popconfirm
                title={t`Recompute this entry?`}
                description={t`The next attempt and the recorded reason are cleared. Nothing is deleted and no attempt is consumed.`}
                okText={t`Recompute`}
                cancelText={t`Cancel`}
                onConfirm={() => void recompute()}
              >
                <Button loading={busy === 'recompute'}>{t`Recompute`}</Button>
              </Popconfirm>
              <Popconfirm
                title={t`Remove this contact from the automation?`}
                description={t`The contact leaves this automation only. Other contacts are not touched.`}
                okText={t`Remove the contact`}
                okButtonProps={{ danger: true }}
                cancelText={t`Cancel`}
                onConfirm={() => void exitContact()}
              >
                <Button danger loading={busy === 'exit'}>
                  {t`Remove this contact`}
                </Button>
              </Popconfirm>
            </Space>
          )}

          <div>
            <Title level={5} style={{ marginTop: 0 }}>
              {t`Last decision`}
            </Title>
            {!decision && <Text type="secondary">{t`No decision recorded for this entry yet.`}</Text>}
            {decision && (
              <Space direction="vertical" style={{ width: '100%' }}>
                <Space wrap>
                  <Tag color={outcomeColor(decision.outcome)}>{labels.decisionOutcomeLabel(decision.outcome)}</Tag>
                  {decision.reason && <Text>{labels.reasonLabel(decision.reason)}</Text>}
                  <Text type="secondary">{formatDateTime(decision.at)}</Text>
                  {decision.sampled && <Tag>{t`Sampled`}</Tag>}
                </Space>
                {decision.trace ? (
                  <VeridianQueueTrace trace={decision.trace} />
                ) : (
                  <Text type="secondary">{t`No trace was recorded for this decision.`}</Text>
                )}
              </Space>
            )}
          </div>
        </Space>
      )}
    </Drawer>
  )
}
