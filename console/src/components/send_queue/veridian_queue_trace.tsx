import { Alert, Collapse, Space, Table, Tag, Typography } from 'antd'
import { useLingui } from '@lingui/react/macro'
import { i18n } from '@lingui/core'

import type { DecisionTrace, TraceCandidate, TraceGate } from '../../services/api/veridian_queue_explain'
import { formatDateTime, useQueueLabels } from './veridian_queue_labels'
import { formatDelaySeconds, formatGateValue, outcomeColor, verdictColor } from './veridian_queue_rules'

const { Text } = Typography

// Dernière décision, gate par gate : un bloc par profil candidat, la trace brute repliée.
export function VeridianQueueTrace({ trace }: { trace: DecisionTrace }) {
  const { t } = useLingui()
  const labels = useQueueLabels()
  const locale = i18n.locale || 'en'

  const gateColumns = [
    {
      title: t`Gate`,
      key: 'gate',
      render: (_: unknown, g: TraceGate) => labels.gateLabel(g.gate)
    },
    {
      title: t`Verdict`,
      key: 'verdict',
      render: (_: unknown, g: TraceGate) => (
        <Tag color={verdictColor(g.verdict)}>{labels.verdictLabel(g.verdict)}</Tag>
      )
    },
    { title: t`Value`, key: 'value', render: (_: unknown, g: TraceGate) => formatGateValue(g.value) },
    { title: t`Limit`, key: 'limit', render: (_: unknown, g: TraceGate) => formatGateValue(g.limit) },
    {
      title: t`Delay`,
      key: 'delay',
      render: (_: unknown, g: TraceGate) => formatDelaySeconds(g.delay_s, locale)
    },
    {
      title: t`Name`,
      key: 'name',
      render: (_: unknown, g: TraceGate) => (g.name ? g.name : g.detail ? g.detail : '—')
    }
  ]

  const decision = trace.decision

  return (
    <div data-testid="queue-trace">
      <Space wrap size="small" style={{ marginBottom: 8 }}>
        <Text type="secondary">{t`Recipient class`}</Text>
        <Tag>{trace.class || '—'}</Tag>
        {trace.anchor && (
          <Tag color={trace.anchor.available ? 'green' : 'orange'}>
            {trace.anchor.available ? t`Original sender available` : t`Original sender unavailable`}
          </Tag>
        )}
      </Space>

      {trace.level === 'reduced' && (
        <Alert
          type="info"
          showIcon
          style={{ marginBottom: 8 }}
          message={t`Reduced trace: only the gates that decided are recorded.`}
        />
      )}

      {trace.candidates.length === 0 && <Text type="secondary">{t`No candidate profile was evaluated.`}</Text>}

      {trace.candidates.map((candidate: TraceCandidate) => (
        <div key={`${candidate.profile}-${candidate.from}`} style={{ marginBottom: 12 }} data-testid="queue-candidate">
          <Space wrap size="small" style={{ marginBottom: 4 }}>
            <Text strong>{candidate.profile_name || candidate.profile}</Text>
            {candidate.from && <Text type="secondary">{candidate.from}</Text>}
            <Tag color={outcomeColor(candidate.outcome)}>{labels.candidateOutcomeLabel(candidate.outcome)}</Tag>
          </Space>
          <Table<TraceGate>
            size="small"
            pagination={false}
            rowKey={(g, i) => `${g.gate}-${i}`}
            columns={gateColumns}
            dataSource={candidate.gates}
            locale={{ emptyText: t`No gate recorded` }}
          />
        </div>
      ))}

      {decision && (
        <Space wrap size="small" style={{ marginBottom: 8 }}>
          <Text strong>{t`Decision`}</Text>
          <Tag color={outcomeColor(decision.outcome)}>{labels.decisionOutcomeLabel(decision.outcome)}</Tag>
          {decision.reason && <Text>{labels.reasonLabel(decision.reason)}</Text>}
          {decision.until && (
            <Text type="secondary">
              {t`until`} {formatDateTime(decision.until)}
            </Text>
          )}
        </Space>
      )}

      <Collapse
        size="small"
        items={[
          {
            key: 'raw',
            label: t`Raw trace (JSON)`,
            children: (
              <pre style={{ margin: 0, maxHeight: 320, overflow: 'auto', fontSize: 12 }}>
                {JSON.stringify(trace, null, 2)}
              </pre>
            )
          }
        ]}
      />
    </div>
  )
}
