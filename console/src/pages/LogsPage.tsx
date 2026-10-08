import { Typography, Tabs, Segmented } from 'antd'
import { useParams, useSearch, useNavigate } from '@tanstack/react-router'
import { useQueryClient } from '@tanstack/react-query'
import { useLingui } from '@lingui/react/macro'
import { MessageHistoryTab } from '../components/messages/MessageHistoryTab'
import { InboundWebhookEventsTab } from '../components/webhooks/InboundWebhookEventsTab'
import { OutgoingWebhooksTab } from '../components/webhooks/OutgoingWebhooksTab'
import { messageTypeFromSearch } from '../layouts/veridian_sidebar_model'
import type { MessageType } from '../services/api/messages_history'

const { Text } = Typography

export function LogsPage() {
  const { workspaceId } = useParams({ strict: false })
  const search = useSearch({ strict: false }) as { tab?: string; type?: string }
  const navigate = useNavigate()
  // Journal commercial (defaut) ou transactionnel : meme page, parametre de recherche `type`
  const messageType = messageTypeFromSearch(search)
  const queryClient = useQueryClient()
  const { t } = useLingui()

  if (!workspaceId) {
    return <div>{t`Loading...`}</div>
  }

  const handleRefreshInboundWebhookEvents = () => {
    queryClient.invalidateQueries({ queryKey: ['inbound-webhook-events', workspaceId] })
  }

  return (
    <div className="p-6">
      <div className="mb-6">
        <div className="text-2xl font-medium">
          {messageType === 'transactional' ? t`Transactional log` : t`Sending log`}
        </div>
        <Text type="secondary">{t`Monitor message delivery status and webhook events`}</Text>
      </div>

      <div className="mb-4">
        <Segmented
          options={[
            { label: t`Commercial`, value: 'commercial' },
            { label: t`Transactional`, value: 'transactional' }
          ]}
          value={messageType}
          onChange={(value) =>
            navigate({
              search: ((prev: Record<string, unknown>) => ({
                ...prev,
                type: value as MessageType
              })) as never
            })
          }
        />
      </div>

      <Tabs
        defaultActiveKey={search.tab || 'messages'}
        items={[
          {
            key: 'messages',
            label: t`Message History`,
            children: <MessageHistoryTab workspaceId={workspaceId} messageType={messageType} />
          },
          {
            key: 'incoming-webhooks',
            label: t`Incoming Webhooks`,
            children: (
              <InboundWebhookEventsTab workspaceId={workspaceId} onRefresh={handleRefreshInboundWebhookEvents} />
            )
          },
          {
            key: 'outgoing-webhooks',
            label: t`Outgoing Webhooks`,
            children: <OutgoingWebhooksTab workspaceId={workspaceId} />
          }
        ]}
      />
    </div>
  )
}
