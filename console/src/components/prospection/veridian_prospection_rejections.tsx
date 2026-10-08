import React from 'react'
import { Alert, Card, Col, Row, Statistic, Tooltip } from 'antd'
import { useQuery } from '@tanstack/react-query'
import { useLingui } from '@lingui/react/macro'

import { analyticsService } from '../../services/api/analytics'
import type { Workspace } from '../../services/api/types'
import { buildSeriesQuery } from '../analytics/email_metrics_series'
import { formatNumber } from '../sending_profiles/veridian_profile_labels'
import { REJECTION_SERIES, rejectionTotals } from './veridian_prospection_rules'

interface Props {
  workspace: Workspace
  timeRange: [string, string]
  timezone: string
}

// Rejets (durs, mous, refus de politique), désinscriptions et plaintes de la période,
// par le moteur analytics (chaque mesure sur sa propre date d'événement).
export const VeridianProspectionRejections: React.FC<Props> = ({ workspace, timeRange, timezone }) => {
  const { t } = useLingui()
  const { data, isLoading, error } = useQuery({
    queryKey: ['prospection', 'rejections', workspace.id, timeRange[0], timeRange[1], timezone],
    queryFn: async () => {
      const responses = await Promise.all(
        REJECTION_SERIES.map((def) =>
          analyticsService.query(buildSeriesQuery(def, 'commercial', timeRange, timezone), workspace.id)
        )
      )
      return rejectionTotals(responses.map((r) => r.data))
    },
    refetchInterval: 60000
  })

  const tile = (testId: string, title: string, hint: string, value: number | undefined) => (
    <Col xs={12} sm={8} md={4} key={testId}>
      <Tooltip title={hint}>
        <div className="bg-gray-100 p-3 rounded-lg" data-testid={testId}>
          <Statistic
            title={title}
            value={value ?? 0}
            valueStyle={{ fontSize: '20px', fontWeight: 'bold' }}
            loading={isLoading}
            formatter={(v) => formatNumber(v as number)}
          />
        </div>
      </Tooltip>
    </Col>
  )

  return (
    <Card title={t`Rejections, unsubscribes and complaints`} style={{ marginBottom: 24 }}>
      {error && <Alert type="error" showIcon message={t`Unable to load the rejections`} style={{ marginBottom: 12 }} />}
      <Row gutter={[12, 12]}>
        {tile('rej-hard', t`Hard bounces`, t`Dead address: the contact is removed from the sequences.`, data?.hard)}
        {tile('rej-soft', t`Soft bounces`, t`Transient failure (full mailbox, greylisting).`, data?.soft)}
        {tile(
          'rej-policy',
          t`Policy refusals`,
          t`The server refused the message for policy reasons (5.7.x: spam filter, reputation, authentication). Dated on the send date.`,
          data?.policy
        )}
        {tile('rej-unsub', t`Unsubscribes`, t`Contacts who unsubscribed.`, data?.unsubscribed)}
        {tile('rej-complaint', t`Complaints`, t`Spam complaints reported by the recipient.`, data?.complained)}
      </Row>
    </Card>
  )
}
