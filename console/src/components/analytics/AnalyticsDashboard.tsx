import React from 'react'
import { Row, Col, Statistic, Button } from 'antd'
import { useNavigate } from '@tanstack/react-router'
import { useQuery } from '@tanstack/react-query'
import { useLingui } from '@lingui/react/macro'
import numbro from 'numbro'
import { EmailMetricsChart } from './EmailMetricsChart'
import { Workspace } from '../../services/api/types'
import { FailedMessagesTable } from './FailedMessagesTable'
import { NewContactsTable } from './NewContactsTable'
import { emailProfilesOverviewService } from '../../services/api/veridian_email_profiles'
import { VeridianProspectionDashboard } from '../prospection/veridian_prospection_dashboard'
import { VeridianTransactionalWatchCard } from '../prospection/veridian_transactional_watch'
import type { MessageTypeFilter } from './email_metrics_series'

interface AnalyticsDashboardProps {
  workspace: Workspace
  timeRange: [string, string]
  timezone?: string
  // Vue affichee : commercial (defaut) ou transactionnel, jamais additionnees
  messageType?: MessageTypeFilter
  // Ouvre la vue transactionnelle (lien du bandeau d'alerte de la vue commerciale)
  onOpenTransactional?: () => void
}

export const AnalyticsDashboard: React.FC<AnalyticsDashboardProps> = ({
  workspace,
  timeRange,
  timezone,
  messageType = 'commercial',
  onOpenTransactional
}) => {
  const { t } = useLingui()
  const navigate = useNavigate()
  const isCommercial = messageType === 'commercial'

  // Use timeRange and timezone as refresh key to update components when they change
  const refreshKey = `${timeRange[0]}-${timeRange[1]}-${timezone || ''}`

  // Profils d'envoi : profil transactionnel et surveillance (emailProfiles.overview).
  // La vue commerciale a sa propre lecture dans le tableau de bord de prospection.
  const { data: overview, isLoading: overviewLoading } = useQuery({
    queryKey: ['analytics', 'profiles-overview', workspace.id],
    queryFn: () => emailProfilesOverviewService.get(workspace.id),
    refetchInterval: 60000,
    enabled: !isCommercial
  })
  const totals = overview?.totals
  const transactionalProfile = overview?.profiles.find((p) => p.usage === 'transactional')
  const transactionalSender =
    transactionalProfile?.senders.find((sender) => sender.is_default) ?? transactionalProfile?.senders[0]

  // Number formatter for statistics (loading state handled by Statistic's `loading` prop)
  const formatStat = (value: number | string) => numbro(value).format({ thousandSeparated: true })

  const handleNavigateToProfiles = () => {
    navigate({
      to: '/console/workspace/$workspaceId/sending-profiles',
      params: { workspaceId: workspace.id }
    })
  }

  // Vue commerciale : le tableau de bord de prospection (lot 5), puis les echecs recents
  // et les derniers contacts importes.
  if (isCommercial) {
    return (
      <div>
        <VeridianProspectionDashboard
          workspace={workspace}
          timeRange={timeRange}
          timezone={timezone}
          onOpenTransactional={onOpenTransactional}
        />
        <div className="mt-8">
          <NewContactsTable key={`new-contacts-${refreshKey}`} workspace={workspace} />
        </div>
        <div className="mt-8">
          <FailedMessagesTable key={`failed-messages-${refreshKey}`} workspace={workspace} />
        </div>
      </div>
    )
  }

  return (
    <div>
      <Row gutter={[16, 16]} className="mb-4" data-testid="cards-transactional">
        <Col xs={24} sm={12}>
          <div className="bg-gray-100 p-4 rounded-lg" style={{ height: '110px' }}>
            <div className="text-gray-500 text-sm mb-2">{t`Transactional profile`}</div>
            {transactionalProfile ? (
              <div>
                <div className="mb-1">
                  <span className="font-medium">{transactionalProfile.name}</span>
                </div>
                {transactionalSender && (
                  <div className="text-sm text-gray-600">{transactionalSender.email}</div>
                )}
              </div>
            ) : (
              <div>
                <div className="text-gray-400 mb-2">{t`Not configured`}</div>
                <Button size="small" type="primary" onClick={handleNavigateToProfiles}>
                  {t`Configure`}
                </Button>
              </div>
            )}
          </div>
        </Col>
        <Col xs={24} sm={12}>
          <div className="bg-gray-100 p-4 rounded-lg" style={{ height: '110px' }}>
            <Statistic
              title={t`Sent today`}
              value={totals?.transactional_sent_today ?? 0}
              valueStyle={{ fontSize: '24px', fontWeight: 'bold' }}
              loading={overviewLoading}
              formatter={(value) => formatStat(value as number)}
            />
          </div>
        </Col>
      </Row>
      <div className="mb-8 text-sm text-gray-600" data-testid="transactional-no-cap">
        {t`These emails are subject to no cap: a transactional email always goes out.`}
      </div>

      <VeridianTransactionalWatchCard watch={overview?.transactional_watch} />

      {/* Email Metrics Chart - Full Width */}
      <EmailMetricsChart
        key={`email-metrics-${refreshKey}`}
        workspace={workspace}
        timeRange={timeRange}
        timezone={timezone}
        messageType={messageType}
      />
    </div>
  )
}
