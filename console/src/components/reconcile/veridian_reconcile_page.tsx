import { useCallback, useEffect, useState } from 'react'
import { useParams } from '@tanstack/react-router'
import { Alert, App, Button, Card, Collapse, Empty, Result, Skeleton, Space, Statistic, Tag, Typography } from 'antd'
import { CheckCircleOutlined, ExclamationCircleOutlined, QuestionCircleOutlined, SyncOutlined } from '@ant-design/icons'
import { useLingui } from '@lingui/react/macro'

import { useWorkspacePermissions } from '../../contexts/AuthContext'
import {
  reconcileService,
  type ReconcileCheck,
  type ReconcileReport,
  type ReconcileResolveAction,
  type ReconcileResolveResult
} from '../../services/api/veridian_reconcile'
import { formatDateTime } from '../send_queue/veridian_queue_labels'
import { formatNumber } from '../sending_profiles/veridian_profile_labels'
import { VeridianReconcileFindingCard } from './veridian_reconcile_finding_card'
import { countBySeverity, notReached, sortFindings } from './veridian_reconcile_rules'

const { Title, Text } = Typography

// Page « Écarts » : le logiciel recoupe ce qu'il croit (statuts, compteurs, classes, stock)
// avec ce qui s'est réellement passé et dit, pour chaque écart, la cause, la preuve et la
// réparation en un clic. Les trois états d'un contrôle sont toujours visibles : concorde,
// écarts, non abouti (un contrôle qui n'a pas pu conclure n'est jamais un succès).
export function ReconcilePage() {
  const { t } = useLingui()
  const { message } = App.useApp()
  const { workspaceId } = useParams({ strict: false }) as { workspaceId?: string }
  const { permissions, loading: loadingPermissions } = useWorkspacePermissions(workspaceId ?? '')
  const canRead = !!(
    (permissions?.contacts?.read || permissions?.contacts?.write) &&
    (permissions?.message_history?.read || permissions?.message_history?.write) &&
    (permissions?.automations?.read || permissions?.automations?.write)
  )
  const canWrite = !!(permissions?.automations?.write && permissions?.contacts?.write)

  const [report, setReport] = useState<ReconcileReport | null>(null)
  const [checks, setChecks] = useState<ReconcileCheck[]>([])
  const [loading, setLoading] = useState(true)
  const [running, setRunning] = useState(false)
  const [error, setError] = useState<string | null>(null)
  const [busyId, setBusyId] = useState<string | null>(null)
  const [results, setResults] = useState<Record<string, ReconcileResolveResult>>({})

  const load = useCallback(async () => {
    if (!workspaceId) return
    setLoading(true)
    try {
      setReport(await reconcileService.report(workspaceId))
      setError(null)
    } catch (err) {
      setError(err instanceof Error ? err.message : t`Could not load the discrepancies`)
    } finally {
      setLoading(false)
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps -- t est stable pour une langue donnée
  }, [workspaceId])

  useEffect(() => {
    if (canRead) void load()
  }, [load, canRead])

  const run = async () => {
    if (!workspaceId) return
    setRunning(true)
    try {
      const rep = await reconcileService.run(workspaceId)
      setReport(rep)
      setChecks(rep.checks)
      setError(null)
    } catch (err) {
      message.error(err instanceof Error ? err.message : t`The check failed`)
    } finally {
      setRunning(false)
    }
  }

  const act = async (id: string, action: ReconcileResolveAction) => {
    if (!workspaceId) return
    setBusyId(id)
    try {
      const res = await reconcileService.resolve(workspaceId, id, action)
      setResults((prev) => ({ ...prev, [id]: res }))
      if (action !== 'dry_run') {
        message.success(res.message)
        await load()
      } else {
        message.info(res.message)
      }
    } catch (err) {
      message.error(err instanceof Error ? err.message : t`The action failed`)
    } finally {
      setBusyId(null)
    }
  }

  if (!loadingPermissions && !canRead) {
    return (
      <div style={{ padding: 24 }}>
        <Result status="403" title={t`Access denied`} subTitle={t`You need read access to contacts, sending history and automations.`} />
      </div>
    )
  }

  const findings = sortFindings(report?.findings ?? []).filter((f) => !f.resolved_at)
  const counts = countBySeverity(findings)
  const unreached = notReached(checks)

  const checkIcon = (c: ReconcileCheck) =>
    c.status === 'ok' ? (
      <CheckCircleOutlined style={{ color: '#389e0d' }} />
    ) : c.status === 'ecarts' ? (
      <ExclamationCircleOutlined style={{ color: '#d46b08' }} />
    ) : (
      <QuestionCircleOutlined style={{ color: '#8c8c8c' }} />
    )
  const checkLabel = (c: ReconcileCheck) =>
    c.status === 'ok' ? t`Everything matches` : c.status === 'ecarts' ? t`Discrepancies found` : t`Could not conclude`

  return (
    <div style={{ padding: 24 }}>
      <Space style={{ width: '100%', justifyContent: 'space-between', marginBottom: 16 }} align="start" wrap>
        <div>
          <Title level={3} style={{ margin: 0 }}>
            {t`Discrepancies`}
          </Title>
          <Text type="secondary">{t`What the software believes, compared with what really happened.`}</Text>
        </div>
        <Button type="primary" icon={<SyncOutlined />} loading={running} onClick={() => void run()} data-testid="reconcile-run">
          {t`Run the check`}
        </Button>
      </Space>

      {error && <Alert type="error" showIcon message={error} style={{ marginBottom: 16 }} />}
      {loading && !report && <Skeleton active />}

      {report && (
        <Space direction="vertical" size="middle" style={{ width: '100%' }}>
          <Space size="large" wrap>
            <Statistic title={t`Critical`} value={counts.critical} formatter={(v) => formatNumber(Number(v))} valueStyle={counts.critical ? { color: '#cf1322' } : undefined} />
            <Statistic title={t`To fix`} value={counts.warning} formatter={(v) => formatNumber(Number(v))} />
            <Statistic title={t`For information`} value={counts.info} formatter={(v) => formatNumber(Number(v))} />
            {report.ran && (
              <Text type="secondary">
                {t`Checked at`} {formatDateTime(report.generated_at)}
              </Text>
            )}
          </Space>

          {checks.length > 0 && (
            <Card size="small" title={t`Checks`} data-testid="reconcile-checks">
              <Space direction="vertical" size={4}>
                {checks.map((c) => (
                  <Text key={c.check}>
                    {checkIcon(c)} {c.title}: <Tag>{checkLabel(c)}</Tag>
                    {c.error ? <Text type="secondary">{c.error}</Text> : null}
                  </Text>
                ))}
              </Space>
            </Card>
          )}
          {unreached.length > 0 && (
            <Alert
              type="warning"
              showIcon
              data-testid="reconcile-not-reached"
              message={t`Some checks could not conclude: that is not a success.`}
              description={unreached.map((c) => c.title).join(', ')}
            />
          )}

          {findings.length === 0 ? (
            <Empty
              description={
                report.ran
                  ? checks.length > 0 && unreached.length === 0
                    ? t`No discrepancy: everything matches.`
                    : t`No open discrepancy.`
                  : t`No open discrepancy. Run the check to compare again now.`
              }
            />
          ) : (
            <div data-testid="reconcile-findings">
              {findings.map((f) => (
                <VeridianReconcileFindingCard
                  key={f.id}
                  finding={f}
                  canWrite={canWrite}
                  busy={busyId === f.id}
                  lastResult={results[f.id]}
                  onAct={(a) => void act(f.id, a)}
                />
              ))}
            </div>
          )}

          {(report.definitions?.length ?? 0) > 0 && (
            <Collapse
              size="small"
              items={[
                {
                  key: 'defs',
                  label: t`How things are counted`,
                  children: (
                    <ul style={{ margin: 0, paddingLeft: 18 }}>
                      {report.definitions!.map((d) => (
                        <li key={d.key}>
                          <Text strong>{d.label}</Text> {d.definition}
                        </li>
                      ))}
                    </ul>
                  )
                }
              ]}
            />
          )}
        </Space>
      )}
    </div>
  )
}

export default ReconcilePage
