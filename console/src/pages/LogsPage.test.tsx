import { beforeEach, describe, expect, it, vi } from 'vitest'
import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { i18n } from '@lingui/core'
import { I18nProvider } from '@lingui/react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'

i18n.loadAndActivate({ locale: 'en', messages: {} })

const navigate = vi.fn()
let searchValue: Record<string, unknown> = {}

vi.mock('@tanstack/react-router', () => ({
  useParams: () => ({ workspaceId: 'ws-1' }),
  useSearch: () => searchValue,
  useNavigate: () => navigate
}))
vi.mock('../components/messages/MessageHistoryTab', () => ({
  MessageHistoryTab: (props: { messageType?: string }) => (
    <div data-testid="history-tab" data-type={props.messageType} />
  )
}))
vi.mock('../components/webhooks/InboundWebhookEventsTab', () => ({ InboundWebhookEventsTab: () => <div /> }))
vi.mock('../components/webhooks/OutgoingWebhooksTab', () => ({ OutgoingWebhooksTab: () => <div /> }))

import { LogsPage } from './LogsPage'

const renderPage = () =>
  render(
    <QueryClientProvider client={new QueryClient()}>
      <I18nProvider i18n={i18n}>
        <LogsPage />
      </I18nProvider>
    </QueryClientProvider>
  )

describe('LogsPage : journal commercial / transactionnel', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    searchValue = {}
  })

  it('commercial par defaut, avec un selecteur visible', () => {
    renderPage()
    expect(screen.getByTestId('history-tab')).toHaveAttribute('data-type', 'commercial')
    expect(screen.getByText('Sending log')).toBeInTheDocument()
    expect(screen.getByText('Commercial')).toBeInTheDocument()
    expect(screen.getByText('Transactional')).toBeInTheDocument()
  })

  it('type=transactional ouvre le journal transactionnel', () => {
    searchValue = { type: 'transactional' }
    renderPage()
    expect(screen.getByTestId('history-tab')).toHaveAttribute('data-type', 'transactional')
    expect(screen.getByText('Transactional log')).toBeInTheDocument()
  })

  it('le selecteur change le parametre de recherche type', async () => {
    renderPage()
    await userEvent.click(screen.getByText('Transactional'))
    expect(navigate).toHaveBeenCalledTimes(1)
    const update = navigate.mock.calls[0][0].search as (p: Record<string, unknown>) => Record<string, unknown>
    expect(update({ tab: 'messages' })).toEqual({ tab: 'messages', type: 'transactional' })
  })
})
