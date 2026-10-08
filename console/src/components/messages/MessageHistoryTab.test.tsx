import { beforeEach, describe, expect, it, vi } from 'vitest'
import { render, waitFor } from '@testing-library/react'
import { i18n } from '@lingui/core'
import { I18nProvider } from '@lingui/react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'

i18n.loadAndActivate({ locale: 'en', messages: {} })

vi.mock('../../contexts/AuthContext', () => ({ useAuth: () => ({ workspaces: [] }) }))
vi.mock('../../services/api/messages_history', () => ({
  listMessages: vi.fn().mockResolvedValue({ messages: [], has_more: false })
}))
vi.mock('../../services/api/broadcast', () => ({
  broadcastApi: { list: vi.fn().mockResolvedValue({ broadcasts: [] }) }
}))
vi.mock('../../services/api/list', () => ({
  listsApi: { list: vi.fn().mockResolvedValue({ lists: [] }) }
}))
vi.mock('./MessageHistoryTable', () => ({ MessageHistoryTable: () => <div /> }))

import { listMessages } from '../../services/api/messages_history'
import { MessageHistoryTab } from './MessageHistoryTab'

const renderTab = (messageType?: 'commercial' | 'transactional') =>
  render(
    <QueryClientProvider client={new QueryClient({ defaultOptions: { queries: { retry: false } } })}>
      <I18nProvider i18n={i18n}>
        <MessageHistoryTab workspaceId="ws-1" messageType={messageType} />
      </I18nProvider>
    </QueryClientProvider>
  )

describe('MessageHistoryTab : type de journal', () => {
  beforeEach(() => {
    vi.mocked(listMessages).mockClear()
    window.history.replaceState({}, '', '/console/workspace/ws-1/logs?type=transactional&tab=messages')
  })

  it('envoie message_type=transactional au serveur', async () => {
    renderTab('transactional')
    await waitFor(() => expect(listMessages).toHaveBeenCalled())
    expect(vi.mocked(listMessages).mock.calls.at(-1)![1]).toMatchObject({ message_type: 'transactional' })
  })

  it('commercial par defaut', async () => {
    renderTab()
    await waitFor(() => expect(listMessages).toHaveBeenCalled())
    expect(vi.mocked(listMessages).mock.calls.at(-1)![1]).toMatchObject({ message_type: 'commercial' })
  })

  it("garde le parametre type dans l'URL quand les filtres la reecrivent", async () => {
    renderTab('transactional')
    await waitFor(() => expect(listMessages).toHaveBeenCalled())
    expect(window.location.search).toContain('type=transactional')
  })
})
