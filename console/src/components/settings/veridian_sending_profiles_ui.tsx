import { ClockCircleOutlined, MailOutlined, SafetyCertificateOutlined } from '@ant-design/icons'
import { Button, Card, Col, Collapse, Row, Space, Tag, Typography } from 'antd'
import { useLingui } from '@lingui/react/macro'

import type {
  EmailProvider,
  Integration,
  VeridianProviderClass,
  VeridianSendingWindow,
  Workspace
} from '../../services/api/types'
import { VERIDIAN_PROVIDER_CLASSES } from '../../services/api/workspace'
import { marketingProfileIds } from './veridian_email_profiles'
import {
  type EmailProfilesUsageResponse,
  type EmailProfileUsage
} from '../../services/api/veridian_email_profiles'

const { Text } = Typography

const PROVIDER_LABELS: Record<VeridianProviderClass, string> = {
  google: 'Google',
  microsoft: 'Microsoft',
  yahoo_aol: 'Yahoo / AOL',
  freemail_fr: 'FAI français',
  corporate: 'Corporate (classification legacy)',
  ovh: 'OVH',
  ionos: 'IONOS / 1&1',
  apple_icloud: 'Apple iCloud',
  security_gateway: 'Passerelles anti-spam',
  other_hoster: 'Autres hébergeurs',
  corporate_selfhost: 'Corporate auto-hébergé'
}

const PROVIDER_GROUPS: Array<{
  title: string
  classes: VeridianProviderClass[]
}> = [
  {
    title: 'Mailbox networks',
    classes: ['google', 'microsoft', 'yahoo_aol', 'freemail_fr', 'apple_icloud']
  },
  {
    title: 'MX and hosting networks',
    classes: ['corporate', 'ovh', 'ionos', 'security_gateway', 'other_hoster', 'corporate_selfhost']
  }
]

const DAY_LABELS: Record<number, string> = {
  0: 'Sun',
  1: 'Mon',
  2: 'Tue',
  3: 'Wed',
  4: 'Thu',
  5: 'Fri',
  6: 'Sat'
}

const pad = (value = 0) => value.toString().padStart(2, '0')

const describeSendingWindow = (
  window: VeridianSendingWindow | undefined,
  fallbackTimezone: string
): string => {
  if (!window) return '24/7'
  const days = window.days?.length
    ? window.days.map((day) => DAY_LABELS[day] || String(day)).join(', ')
    : 'Every day'
  const start = `${pad(window.start_hour)}:${pad(window.start_minute)}`
  const end = `${pad(window.end_hour)}:${pad(window.end_minute)}`
  return `${days} · ${start}-${end} · ${window.timezone || fallbackTimezone}`
}

const emailIntegrations = (
  workspace: Workspace
): Array<Integration & { email_provider: EmailProvider }> =>
  (workspace.integrations || []).filter(
    (integration): integration is Integration & { email_provider: EmailProvider } =>
      integration.type === 'email' && !!integration.email_provider
  )

export function SendingProfilesOverview({
  workspace,
  usage,
  usageLoading,
  usageError
}: {
  workspace: Workspace
  usage?: EmailProfilesUsageResponse | null
  usageLoading?: boolean
  usageError?: boolean
}) {
  const { t } = useLingui()
  const profiles = emailIntegrations(workspace)
  const activeIds = marketingProfileIds(workspace.settings)
  const activeCount = profiles.filter((profile) => activeIds.includes(profile.id)).length
  const settingsHref = `/console/workspace/${workspace.id}/settings/cold-outreach`

  return (
    <Card className="!mb-5" styles={{ body: { padding: 18 } }}>
      <Row gutter={[16, 16]} align="middle">
        <Col xs={24} md={14}>
          <Space direction="vertical" size={4}>
            <Space wrap>
              <MailOutlined style={{ color: '#1677ff' }} />
              <Text strong>{t`Sending profile rotation`}</Text>
              <Tag color={activeCount > 1 ? 'green' : 'blue'}>
                {activeCount > 1
                  ? t`${activeCount} active profiles`
                  : t`${activeCount} active profile`}
              </Tag>
            </Space>
            <Text type="secondary">
              {activeCount > 1
                ? t`Marketing sends rotate across the selected profiles. Each queued email keeps the profile chosen for it.`
                : t`Add another profile to spread marketing volume across independent Gmail accounts.`}
            </Text>
            <Text type="secondary" style={{ fontSize: 12 }}>
              {usageLoading
                ? t`Loading today's usage...`
                : usageError || !usage
                  ? t`Today's usage is unavailable`
                  : usage.total_used === usage.total_accepted
                    ? t`${usage.total_accepted} emails accepted today (${usage.date})`
                    : t`${usage.total_accepted} accepted, ${usage.total_used} quota slots used today (${usage.date})`}
            </Text>
          </Space>
        </Col>
        <Col xs={24} md={10}>
          <div className="rounded-lg border border-gray-200 bg-gray-50 p-3">
            <Space size="small" align="start">
              <ClockCircleOutlined className="mt-1 text-gray-500" />
              <div>
                <Text strong>{t`Global sending window`}</Text>
                <div>
                  <Text type="secondary" style={{ fontSize: 12 }}>
                    {describeSendingWindow(
                      workspace.settings.veridian_sending_window,
                      workspace.settings.timezone || 'Europe/Paris'
                    )}
                  </Text>
                </div>
                <Button type="link" size="small" href={settingsHref} className="!-ml-2 !px-2">
                  {t`Configure policy`}
                </Button>
              </div>
            </Space>
          </div>
        </Col>
      </Row>
    </Card>
  )
}

export function RecipientProviderPolicy({
  workspace,
  provider,
  usage
}: {
  workspace: Workspace
  provider: EmailProvider
  usage?: EmailProfileUsage
}) {
  const { t } = useLingui()
  const unknownUsageClasses = Object.entries(usage?.accepted_by_provider_class || {}).filter(
    ([providerClass]) => !VERIDIAN_PROVIDER_CLASSES.includes(providerClass as VeridianProviderClass)
  )
  const items = [
    {
      key: 'recipient-policy',
      forceRender: true,
      label: (
        <Space wrap>
          <SafetyCertificateOutlined />
          <span>{t`Recipient-provider policy`}</span>
          <Text type="secondary" style={{ fontSize: 12 }}>
            {t`Google, Microsoft and other mailbox networks`}
          </Text>
        </Space>
      ),
      children: (
        <Space direction="vertical" size={8} style={{ width: '100%' }}>
          {PROVIDER_GROUPS.map((group) => (
            <div key={group.title} className="w-full">
              <Text type="secondary" style={{ fontSize: 11, textTransform: 'uppercase' }}>
                {group.title}
              </Text>
              <Space direction="vertical" size={8} style={{ width: '100%', marginTop: 8 }}>
                {group.classes.map((providerClass) => {
                  const profileRate = provider.veridian_provider_class_rates?.[providerClass]
                  const workspaceRate =
                    workspace.settings.veridian_provider_class_rates?.[providerClass]
                  const profileCap = provider.veridian_provider_class_daily_cap?.[providerClass]
                  const workspaceCap =
                    workspace.settings.veridian_provider_class_daily_cap?.[providerClass]
                  const inherited = profileRate === undefined && profileCap === undefined
                  return (
                    <Row key={providerClass} gutter={8} align="middle" role="row">
                      <Col xs={24} sm={6}>
                        <Text strong>{PROVIDER_LABELS[providerClass]}</Text>
                      </Col>
                      <Col xs={12} sm={4}>
                        <Text type="secondary" style={{ fontSize: 12 }}>
                          {(profileRate ?? workspaceRate)
                            ? t`${profileRate ?? workspaceRate} / min`
                            : t`No rate cap`}
                        </Text>
                      </Col>
                      <Col xs={12} sm={4}>
                        <Text type="secondary" style={{ fontSize: 12 }}>
                          {(profileCap ?? workspaceCap)
                            ? t`${profileCap ?? workspaceCap} / day`
                            : t`No daily cap`}
                        </Text>
                      </Col>
                      <Col xs={12} sm={5}>
                        <Text type="secondary" style={{ fontSize: 12 }}>
                          {usage
                            ? t`${usage.accepted_by_provider_class?.[providerClass] || 0} accepted today`
                            : t`Usage unavailable`}
                        </Text>
                      </Col>
                      <Col xs={12} sm={5} className="sm:text-right">
                        <Tag color={inherited ? 'default' : 'gold'}>
                          {inherited ? t`Inherited` : t`Profile`}
                        </Tag>
                      </Col>
                    </Row>
                  )
                })}
              </Space>
            </div>
          ))}
          {unknownUsageClasses.length > 0 && (
            <div className="w-full">
              <Text type="warning" style={{ fontSize: 11, textTransform: 'uppercase' }}>
                {t`Unrecognized backend classes`}
              </Text>
              {unknownUsageClasses.map(([providerClass, sent]) => (
                <Row key={providerClass} gutter={8} align="middle" role="row" className="mt-2">
                  <Col xs={24} sm={14}>
                    <Text strong>{t`Unknown class: ${providerClass}`}</Text>
                  </Col>
                  <Col xs={16} sm={5}>
                    <Text type="secondary" style={{ fontSize: 12 }}>
                      {t`${sent} accepted today`}
                    </Text>
                  </Col>
                  <Col xs={8} sm={5} className="sm:text-right">
                    <Tag color="warning">{t`Review mapping`}</Tag>
                  </Col>
                </Row>
              ))}
            </div>
          )}
          <Text type="secondary" style={{ fontSize: 12 }}>
            {t`Profile overrides take priority. Missing values inherit the global workspace policy.`}
          </Text>
        </Space>
      )
    }
  ]

  return <Collapse ghost size="small" items={items} />
}
