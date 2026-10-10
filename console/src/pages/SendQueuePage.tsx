import { useCallback, useEffect, useMemo, useRef, useState } from 'react'
import { useParams } from '@tanstack/react-router'
import { Alert, App, Button, Card, Empty, Result, Select, Skeleton, Space, Statistic, Tabs, Typography } from 'antd'
import { ReloadOutlined } from '@ant-design/icons'
import { useLingui } from '@lingui/react/macro'

import { useWorkspacePermissions } from '../contexts/AuthContext'
import {
  queueExplainService,
  QUEUE_REASON_CODES,
  RECOMPUTE_LIMIT,
  type QueueExplain,
  type QueueExplainParams
} from '../services/api/veridian_queue_explain'
import { VeridianQueueDecisions } from '../components/send_queue/veridian_queue_decisions'
import { VeridianQueueEntryDrawer } from '../components/send_queue/veridian_queue_entry_drawer'
import { VeridianQueueGroups } from '../components/send_queue/veridian_queue_groups'
import { useQueueLabels, formatDateTime } from '../components/send_queue/veridian_queue_labels'
import { VeridianQueueReasons } from '../components/send_queue/veridian_queue_reasons'
import {
  collectFilterOptions,
  hasOrphans,
  type FilterOptions,
  type QueueTreeRow
} from '../components/send_queue/veridian_queue_rules'
import { formatNumber, useProfileLabels } from '../components/sending_profiles/veridian_profile_labels'

const { Title, Text } = Typography

type Filters = Pick<QueueExplainParams, 'automation_id' | 'node_id' | 'reason' | 'profile_id' | 'class'>

const EMPTY_OPTIONS: FilterOptions = { automations: [], nodes: [], profiles: [], classes: [] }

// « Pourquoi ça n'envoie pas » : la file d'envoi expliquée par le serveur (queue.explain),
// regroupée par raison, avec le détail gate par gate de la dernière décision.
export function SendQueuePage() {
  const { t } = useLingui()
  const { message } = App.useApp()
  const labels = useQueueLabels()
  const { classLabel } = useProfileLabels()
  const { workspaceId } = useParams({ strict: false }) as { workspaceId?: string }
  const { permissions, loading: loadingPermissions } = useWorkspacePermissions(workspaceId ?? '')
  const canRead = !!(permissions?.automations?.read || permissions?.automations?.write)
  const canWrite = !!permissions?.automations?.write

  const [filters, setFilters] = useState<Filters>({})
  const [data, setData] = useState<QueueExplain | null>(null)
  const [options, setOptions] = useState<FilterOptions>(EMPTY_OPTIONS)
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState<string | null>(null)
  const [openEntry, setOpenEntry] = useState<string | null>(null)
  const [recomputingKey, setRecomputingKey] = useState<string | null>(null)
  const [refreshToken, setRefreshToken] = useState(0)
  const [tab, setTab] = useState('queue')
  const seq = useRef(0)

  const reload = useCallback(async () => {
    if (!workspaceId) return
    const mine = ++seq.current
    setLoading(true)
    try {
      const response = await queueExplainService.explain({ workspace_id: workspaceId, ...filters })
      if (mine !== seq.current) return
      setData(response)
      setOptions((prev) => collectFilterOptions(response.groups, prev))
      setError(null)
    } catch (err) {
      if (mine !== seq.current) return
      setError(err instanceof Error ? err.message : t`Could not load the send queue`)
    } finally {
      if (mine === seq.current) setLoading(false)
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps -- t est stable pour une langue donnée
  }, [workspaceId, filters])

  useEffect(() => {
    if (canRead) void reload()
  }, [reload, canRead])

  const refresh = () => {
    setRefreshToken((n) => n + 1)
    void reload()
  }

  const recompute = async (row: QueueTreeRow) => {
    if (!workspaceId) return
    setRecomputingKey(row.key)
    try {
      const done = await queueExplainService.recompute({
        workspace_id: workspaceId,
        automation_id: row.automation_id,
        node_id: row.node_id,
        reason: row.reason,
        profile_id: row.profile_id,
        limit: RECOMPUTE_LIMIT
      })
      const recomputed = done.recomputed
      message.success(t`Entries recomputed: ${recomputed}`)
      await reload()
    } catch (err) {
      message.error(err instanceof Error ? err.message : t`The action failed`)
    } finally {
      setRecomputingKey(null)
    }
  }

  const setFilter = (key: keyof Filters, value: string | undefined) =>
    setFilters((prev) => ({ ...prev, [key]: value || undefined }))

  const classOptions = useMemo(
    () => options.classes.map((c) => ({ value: c, label: classLabel(c) })),
    // eslint-disable-next-line react-hooks/exhaustive-deps -- classLabel suit la langue
    [options.classes]
  )

  if (!workspaceId) return <div>{t`Loading...`}</div>
  if (loadingPermissions) return <Skeleton active />
  if (!canRead) {
    return <Result status="403" title={t`Access denied`} subTitle={t`You need read access to automations to see the send queue.`} />
  }

  const groups = data?.groups ?? []
  const empty = !loading && !error && data !== null && data.total === 0 && !hasOrphans(data.orphans)
  const orphanCount = data?.orphans?.count ?? 0
  const activeFilters = Object.values(filters).some(Boolean)

  const queueTab = (
    <Space direction="vertical" size="large" style={{ width: '100%' }}>
      <Space wrap>
        <Select
          allowClear
          showSearch
          optionFilterProp="label"
          placeholder={t`Automation`}
          style={{ width: 240 }}
          value={filters.automation_id}
          onChange={(v) => setFilter('automation_id', v)}
          options={options.automations.map((a) => ({ value: a.id, label: a.name }))}
        />
        <Select
          allowClear
          showSearch
          placeholder={t`Node`}
          style={{ width: 140 }}
          value={filters.node_id}
          onChange={(v) => setFilter('node_id', v)}
          options={options.nodes.map((n) => ({ value: n, label: n }))}
        />
        <Select
          allowClear
          placeholder={t`Reason`}
          style={{ width: 280 }}
          value={filters.reason}
          onChange={(v) => setFilter('reason', v)}
          options={QUEUE_REASON_CODES.map((code) => ({ value: code, label: labels.reasonLabel(code) }))}
        />
        <Select
          allowClear
          showSearch
          optionFilterProp="label"
          placeholder={t`Profile`}
          style={{ width: 200 }}
          value={filters.profile_id}
          onChange={(v) => setFilter('profile_id', v)}
          options={options.profiles.map((p) => ({ value: p.id, label: p.name }))}
        />
        <Select
          allowClear
          placeholder={t`Recipient class`}
          style={{ width: 200 }}
          value={filters.class}
          onChange={(v) => setFilter('class', v)}
          options={classOptions}
        />
      </Space>

      {error && (
        <Alert
          type="error"
          showIcon
          message={t`Could not load the send queue`}
          description={error}
          action={
            <Button size="small" onClick={refresh}>
              {t`Retry`}
            </Button>
          }
        />
      )}

      {loading && !data && <Skeleton active paragraph={{ rows: 6 }} />}

      {data && hasOrphans(data.orphans) && (
        <Alert
          type="warning"
          showIcon
          data-testid="queue-orphans"
          message={t`Orphan contacts: ${orphanCount} parked in sending with no queue entry`}
          description={
            <ul style={{ margin: 0, paddingLeft: 18 }}>
              {data.orphans.by_node.map((o) => (
                <li key={`${o.automation_id}-${o.node_id}`}>
                  {o.automation_name || o.automation_id}, {t`Node`} {o.node_id}: {formatNumber(o.count)}
                </li>
              ))}
            </ul>
          }
        />
      )}

      {empty && (
        <Empty
          description={activeFilters ? t`No queue entry matches these filters` : t`The send queue is empty`}
        />
      )}

      {data && data.total > 0 && (
        <>
          <Space size="large" wrap>
            <Statistic title={t`Entries in queue`} value={data.total} formatter={(v) => formatNumber(Number(v))} />
            <Text type="secondary">
              {t`Computed at`} {formatDateTime(data.generated_at)}
            </Text>
          </Space>
          <Card size="small" title={t`By reason`}>
            <VeridianQueueReasons
              groups={groups}
              total={data.total}
              onPickReason={(reason) => setFilter('reason', reason)}
            />
          </Card>
          <Card size="small" title={t`Detail by automation, node, reason and profile`}>
            <VeridianQueueGroups
              groups={groups}
              canWrite={canWrite}
              recomputingKey={recomputingKey}
              onOpenEntry={setOpenEntry}
              onRecompute={(row) => void recompute(row)}
            />
          </Card>
        </>
      )}
    </Space>
  )

  return (
    <div style={{ padding: 24 }}>
      <Space style={{ width: '100%', justifyContent: 'space-between', marginBottom: 16 }} align="start" wrap>
        <div>
          <Title level={3} style={{ margin: 0 }}>
            {t`Send queue`}
          </Title>
          <Text type="secondary">{t`Why mails are waiting, and what the last decision was for each.`}</Text>
        </div>
        <Button icon={<ReloadOutlined />} onClick={refresh} loading={loading}>
          {t`Refresh`}
        </Button>
      </Space>

      <Tabs
        activeKey={tab}
        onChange={setTab}
        items={[
          { key: 'queue', label: t`Queue`, children: queueTab },
          {
            key: 'decisions',
            label: t`Decision log`,
            children: (
              <VeridianQueueDecisions
                workspaceId={workspaceId}
                automationId={filters.automation_id}
                refreshToken={refreshToken}
                onOpenEntry={setOpenEntry}
              />
            )
          }
        ]}
      />

      <VeridianQueueEntryDrawer
        workspaceId={workspaceId}
        entryId={openEntry}
        canWrite={canWrite}
        onClose={() => setOpenEntry(null)}
        onChanged={() => void reload()}
      />
    </div>
  )
}

export default SendQueuePage
