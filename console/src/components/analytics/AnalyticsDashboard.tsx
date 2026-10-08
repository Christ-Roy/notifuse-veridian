import React from 'react'
import { Row, Col, Statistic, Button, Tooltip } from 'antd'
import { useNavigate } from '@tanstack/react-router'
import { useQuery } from '@tanstack/react-query'
import { useLingui } from '@lingui/react/macro'
import numbro from 'numbro'
import { EmailMetricsChart } from './EmailMetricsChart'
import { VeridianEngagementByClass } from './veridian_engagement_by_class'
// import { NewContactsTable } from './NewContactsTable'
import { Workspace } from '../../services/api/types'
import { FailedMessagesTable } from './FailedMessagesTable'
import { NewContactsTable } from './NewContactsTable'
import { analyticsService } from '../../services/api/analytics'
import { emailProfilesOverviewService } from '../../services/api/veridian_email_profiles'
import type { MessageTypeFilter } from './email_metrics_series'

interface AnalyticsDashboardProps {
  workspace: Workspace
  timeRange: [string, string]
  timezone?: string
  // Vue affichee : commercial (defaut) ou transactionnel, jamais additionnees
  messageType?: MessageTypeFilter
}

export const AnalyticsDashboard: React.FC<AnalyticsDashboardProps> = ({
  workspace,
  timeRange,
  timezone,
  messageType = 'commercial'
}) => {
  const { t } = useLingui()
  const navigate = useNavigate()
  const isCommercial = messageType === 'commercial'

  // Use timeRange and timezone as refresh key to update components when they change
  const refreshKey = `${timeRange[0]}-${timeRange[1]}-${timezone || ''}`

  // Query for total contacts count
  const { data: totalContactsData, isLoading: totalContactsLoading } = useQuery({
    queryKey: ['analytics', 'total-contacts', workspace.id],
    queryFn: async () => {
      return analyticsService.query(
        {
          schema: 'contacts',
          measures: ['count'],
          dimensions: [],
          filters: []
        },
        workspace.id
      )
    },
    refetchInterval: 60000 // Refetch every minute
  })

  // Query for new contacts in the given date range
  const { data: newContactsData, isLoading: newContactsLoading } = useQuery({
    queryKey: ['analytics', 'new-contacts', workspace.id, timeRange[0], timeRange[1]],
    queryFn: async () => {
      return analyticsService.query(
        {
          schema: 'contacts',
          measures: ['count'],
          dimensions: [],
          filters: [
            {
              member: 'created_at',
              operator: 'inDateRange',
              values: timeRange
            }
          ]
        },
        workspace.id
      )
    },
    refetchInterval: 60000 // Refetch every minute
  })

  // Profils d'envoi : totaux du jour et profil transactionnel (emailProfiles.overview)
  const { data: overview, isLoading: overviewLoading } = useQuery({
    queryKey: ['analytics', 'profiles-overview', workspace.id],
    queryFn: () => emailProfilesOverviewService.get(workspace.id),
    refetchInterval: 60000
  })
  const totals = overview?.totals
  const transactionalProfile = overview?.profiles.find((p) => p.usage === 'transactional')
  const transactionalSender =
    transactionalProfile?.senders.find((sender) => sender.is_default) ?? transactionalProfile?.senders[0]

  // Calculate totals
  const totalContacts = totalContactsData?.data?.[0]?.['count'] || 0
  const newContactsCount = newContactsData?.data?.[0]?.['count'] || 0

  // Number formatter for statistics (loading state handled by Statistic's `loading` prop)
  const formatStat = (value: number | string) => numbro(value).format({ thousandSeparated: true })

  const handleNavigateToProfiles = () => {
    navigate({
      to: '/console/workspace/$workspaceId/sending-profiles',
      params: { workspaceId: workspace.id }
    })
  }

  const capacity = totals?.commercial_capacity_today
  const commercialSent = formatStat(totals?.commercial_sent_today ?? 0)
  const sentVersusCapacity =
    capacity === null || capacity === undefined
      ? commercialSent
      : `${commercialSent} / ${formatStat(capacity)}`

  return (
    <div>
      {/* Cartes du haut : une vue = une famille, jamais de melange */}
      {isCommercial ? (
        <Row gutter={[16, 16]} className="mb-8" data-testid="cards-commercial">
          <Col xs={24} sm={12} md={6}>
            <div className="p-4 rounded-lg bg-gray-100" style={{ height: '110px' }}>
              <Statistic
                title={
                  <Tooltip title={t`Imported stock, not the audience actually contacted.`}>
                    <span>{t`Imported contacts (stock)`}</span>
                  </Tooltip>
                }
                value={totalContacts as number}
                valueStyle={{ fontSize: '24px', fontWeight: 'bold' }}
                loading={totalContactsLoading}
                formatter={(value) => formatStat(value as number)}
              />
            </div>
          </Col>
          <Col xs={24} sm={12} md={6}>
            <div className="bg-gray-100 p-4 rounded-lg" style={{ height: '110px' }}>
              <Statistic
                title={
                  <Tooltip title={t`Newly imported contacts, not the audience actually contacted.`}>
                    <span>{t`New imported contacts`}</span>
                  </Tooltip>
                }
                value={newContactsCount as number}
                valueStyle={{ fontSize: '24px', fontWeight: 'bold' }}
                loading={newContactsLoading}
                formatter={(value) => formatStat(value as number)}
              />
            </div>
          </Col>
          <Col xs={24} sm={12} md={6}>
            <div className="bg-gray-100 p-4 rounded-lg" style={{ height: '110px' }}>
              <Statistic
                title={t`Profiles in rotation`}
                value={totals?.active_commercial_profiles ?? 0}
                valueStyle={{ fontSize: '24px', fontWeight: 'bold' }}
                loading={overviewLoading}
              />
            </div>
          </Col>
          <Col xs={24} sm={12} md={6}>
            <div className="bg-gray-100 p-4 rounded-lg" style={{ height: '110px' }}>
              <Statistic
                title={
                  <Tooltip title={t`Sent today by the rotation profiles, over today's capacity.`}>
                    <span>{t`Sent today / capacity`}</span>
                  </Tooltip>
                }
                value={sentVersusCapacity}
                valueStyle={{ fontSize: '24px', fontWeight: 'bold' }}
                loading={overviewLoading}
              />
            </div>
          </Col>
        </Row>
      ) : (
        <>
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
        </>
      )}

      {/* Email Metrics Chart - Full Width */}
      <EmailMetricsChart
        key={`email-metrics-${refreshKey}`}
        workspace={workspace}
        timeRange={timeRange}
        timezone={timezone}
        messageType={messageType}
      />

      {/* Engagement par classe, nouveaux contacts et echecs : vue commerciale seulement */}
      {isCommercial && (
        <>
      {/* Veridian — engagement par classe de provider destinataire (KPI cold :
          repérer une classe qui se dégrade). Cf.
          2026-06-16-kpi-engagement-par-classe-provider.md. */}
      <VeridianEngagementByClass
        key={`engagement-by-class-${refreshKey}`}
        workspace={workspace}
        timeRange={timeRange}
      />

      <div className="mt-8">
        <NewContactsTable key={`new-contacts-${refreshKey}`} workspace={workspace} />
      </div>

      <div className="mt-8">
        <FailedMessagesTable key={`failed-messages-${refreshKey}`} workspace={workspace} />
      </div>
        </>
      )}
    </div>
  )
}
