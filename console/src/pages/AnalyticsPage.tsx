import { useState } from 'react'
import { useParams, useNavigate, useSearch } from '@tanstack/react-router'
import { Segmented, Select, Space, Result, Button, Spin } from 'antd'
import { useQuery } from '@tanstack/react-query'
import dayjs from 'dayjs'
import { useAuth } from '../contexts/AuthContext'
import { AnalyticsDashboard } from '../components/analytics/AnalyticsDashboard'
import { TIMEZONE_OPTIONS } from '../lib/timezones'
import { getBrowserTimezone } from '../lib/timezoneNormalizer'
import { useLingui } from '@lingui/react/macro'
import { emailProfilesOverviewService } from '../services/api/veridian_email_profiles'
import { pickDashboardTimezone } from '../components/prospection/veridian_prospection_rules'
import type { MessageTypeFilter } from '../components/analytics/email_metrics_series'

type TimePeriod = '7D' | '14D' | '30D' | '90D'

export function AnalyticsPage() {
  const { t } = useLingui()
  const navigate = useNavigate()
  const { workspaceId } = useParams({ from: '/console/workspace/$workspaceId' })
  const { workspaces } = useAuth()
  // Vue Commercial | Transactionnel, memorisee dans la route (?view=transactional)
  const viewSearch = useSearch({ strict: false }) as { view?: string }
  const messageType: MessageTypeFilter = viewSearch.view === 'transactional' ? 'transactional' : 'commercial'

  const [selectedPeriod, setSelectedPeriod] = useState<TimePeriod>('14D')
  // Fuseau choisi à la main ; tant qu'il est null, on prend celui du workspace, sinon celui de la
  // fenêtre d'envoi des profils, sinon celui du navigateur (jamais un fuseau arbitraire).
  const [userTimezone, setUserTimezone] = useState<string | null>(null)

  const workspace = workspaces.find((w) => w.id === workspaceId)

  const { data: overview, isLoading: overviewLoading } = useQuery({
    queryKey: ['analytics', 'profiles-overview', workspaceId],
    queryFn: () => emailProfilesOverviewService.get(workspaceId),
    enabled: !!workspace,
    refetchInterval: 60000
  })
  const defaultTimezone = pickDashboardTimezone({
    workspaceTimezone: workspace?.settings?.timezone,
    overview,
    browserTimezone: getBrowserTimezone()
  })
  const selectedTimezone = userTimezone ?? defaultTimezone

  // Calculate time range based on selected period
  const getTimeRangeFromPeriod = (period: TimePeriod): [string, string] => {
    const endDate = dayjs().add(1, 'day') // Use tomorrow instead of today
    let startDate: dayjs.Dayjs

    switch (period) {
      case '7D':
        startDate = endDate.subtract(7, 'days')
        break
      case '14D':
        startDate = endDate.subtract(14, 'days')
        break
      case '30D':
        startDate = endDate.subtract(30, 'days')
        break
      case '90D':
        startDate = endDate.subtract(90, 'days')
        break
      default:
        startDate = endDate.subtract(30, 'days')
    }

    return [startDate.format('YYYY-MM-DD'), endDate.format('YYYY-MM-DD')]
  }

  const timeRange = getTimeRangeFromPeriod(selectedPeriod)

  const handlePeriodChange = (value: TimePeriod) => {
    setSelectedPeriod(value)
  }

  const handleTimezoneChange = (value: string) => {
    setUserTimezone(value)
  }

  if (!workspace) {
    return (
      <Result
        status="404"
        title={t`Workspace not found`}
        subTitle={t`The requested workspace could not be found.`}
        extra={
          <Button type="primary" onClick={() => navigate({ to: '/console' })}>
            {t`Back to workspaces`}
          </Button>
        }
      />
    )
  }

  return (
    <div className="p-6">
      <div className="flex justify-between items-center mb-6">
        <div className="text-2xl font-medium">{t`Dashboard`}</div>
        <Space>
          <Segmented
            value={messageType}
            onChange={(value) =>
              navigate({
                search: ((prev: Record<string, unknown>) => ({
                  ...prev,
                  view: value === 'transactional' ? 'transactional' : undefined
                })) as never
              })
            }
            options={[
              { label: t`Commercial`, value: 'commercial' },
              { label: t`Transactional`, value: 'transactional' }
            ]}
          />
          <Select
            value={selectedTimezone}
            onChange={handleTimezoneChange}
            options={TIMEZONE_OPTIONS}
            optionFilterProp="label"
            variant="filled"
            style={{ width: 170 }}
            placeholder={t`Select timezone`}
            showSearch
            filterOption={(input, option) =>
              (option?.label ?? '').toString().toLowerCase().includes(input.toLowerCase())
            }
          />
          <Segmented
            value={selectedPeriod}
            onChange={handlePeriodChange}
            options={[
              { label: '7D', value: '7D' },
              { label: '14D', value: '14D' },
              { label: '30D', value: '30D' },
              { label: '90D', value: '90D' }
            ]}
          />
        </Space>
      </div>
      {overviewLoading && userTimezone === null ? (
        <div className="flex justify-center py-16">
          <Spin />
        </div>
      ) : (
        <AnalyticsDashboard
          workspace={workspace}
          timeRange={timeRange}
          timezone={selectedTimezone}
          messageType={messageType}
          onOpenTransactional={() =>
            navigate({
              search: ((prev: Record<string, unknown>) => ({ ...prev, view: 'transactional' })) as never
            })
          }
        />
      )}
    </div>
  )
}
