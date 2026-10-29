import { useState } from 'react'
import { Alert, Button, Card, Collapse, Popconfirm, Space, Tag, Typography } from 'antd'
import { useLingui } from '@lingui/react/macro'

import type {
  ReconcileFinding,
  ReconcileResolveAction,
  ReconcileResolveResult
} from '../../services/api/veridian_reconcile'
import { formatDateTime } from '../send_queue/veridian_queue_labels'
import { canApply, evidenceLines } from './veridian_reconcile_rules'

const { Text, Paragraph } = Typography

interface Props {
  finding: ReconcileFinding
  canWrite: boolean
  busy: boolean
  lastResult?: ReconcileResolveResult
  onAct: (action: ReconcileResolveAction) => void
}

// Un écart : sa phrase, ce qui était attendu et constaté, la cause en français, la preuve
// et la réparation en un clic. Les textes viennent du serveur (en français).
export function VeridianReconcileFindingCard({ finding, canWrite, busy, lastResult, onAct }: Props) {
  const { t } = useLingui()
  const [open, setOpen] = useState<string[]>([])
  const severity = {
    critical: { color: 'red', label: t`Critical` },
    warning: { color: 'orange', label: t`To fix` },
    info: { color: 'blue', label: t`For information` }
  }[finding.severity]
  const action = finding.proposed_action
  const lines = evidenceLines(finding.evidence)

  return (
    <Card size="small" data-testid="reconcile-finding" data-severity={finding.severity} style={{ marginBottom: 12 }}>
      <Space direction="vertical" size={8} style={{ width: '100%' }}>
        <Space wrap>
          <Tag color={severity.color}>{severity.label}</Tag>
          <Text strong>{finding.title}</Text>
        </Space>
        <Space size="large" wrap>
          <Text>
            <Text type="secondary">{t`Expected`}</Text> {finding.expected}
          </Text>
          <Text>
            <Text type="secondary">{t`Observed`}</Text> {finding.observed}
          </Text>
        </Space>
        <Paragraph style={{ margin: 0 }}>
          <Text type="secondary">{t`Cause`}</Text> {finding.explanation}
        </Paragraph>
        {lines.length > 0 && (
          <Collapse
            size="small"
            ghost
            activeKey={open}
            onChange={(k) => setOpen(Array.isArray(k) ? k : [k])}
            items={[
              {
                key: 'evidence',
                label: t`Evidence`,
                children: (
                  <ul style={{ margin: 0, paddingLeft: 18 }} data-testid="reconcile-evidence">
                    {lines.map((l) => (
                      <li key={l.key}>
                        <Text type="secondary">{l.key}</Text> {l.value}
                      </li>
                    ))}
                  </ul>
                )
              }
            ]}
          />
        )}
        <Text type="secondary" style={{ fontSize: 12 }}>
          {t`First seen`} {formatDateTime(finding.first_seen_at)}
        </Text>

        {lastResult && (
          <Alert
            type={lastResult.verified || lastResult.action !== 'apply' ? 'success' : 'warning'}
            showIcon
            message={lastResult.message}
            data-testid="reconcile-result"
          />
        )}

        {canApply(finding) && action && (
          <Space wrap>
            <Popconfirm
              title={action.label}
              description={action.impact}
              okText={t`Apply`}
              cancelText={t`Cancel`}
              disabled={!canWrite}
              onConfirm={() => onAct('apply')}
            >
              <Button type="primary" disabled={!canWrite} loading={busy} data-testid="reconcile-apply">
                {action.label}
              </Button>
            </Popconfirm>
            <Button disabled={busy} onClick={() => onAct('dry_run')} data-testid="reconcile-dry-run">
              {t`Preview`}
            </Button>
            <Popconfirm
              title={t`Dismiss this discrepancy for 7 days?`}
              description={t`Use this when the situation is intended. It comes back after 7 days if it is still observed.`}
              okText={t`Dismiss`}
              cancelText={t`Cancel`}
              disabled={!canWrite}
              onConfirm={() => onAct('dismiss')}
            >
              <Button type="text" disabled={!canWrite || busy} data-testid="reconcile-dismiss">
                {t`Dismiss for 7 days`}
              </Button>
            </Popconfirm>
          </Space>
        )}
        {!action && !finding.resolved_at && (
          <Space wrap>
            <Text type="secondary">{t`No automatic repair for this discrepancy: the cause above says what to do.`}</Text>
            <Popconfirm
              title={t`Dismiss this discrepancy for 7 days?`}
              okText={t`Dismiss`}
              cancelText={t`Cancel`}
              disabled={!canWrite}
              onConfirm={() => onAct('dismiss')}
            >
              <Button type="text" disabled={!canWrite || busy}>
                {t`Dismiss for 7 days`}
              </Button>
            </Popconfirm>
          </Space>
        )}
      </Space>
    </Card>
  )
}
