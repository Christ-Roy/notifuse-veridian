import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, waitFor, fireEvent } from '@testing-library/react'
import { i18n } from '@lingui/core'
import { I18nProvider } from '@lingui/react'
import { FailedMessagesTable } from './FailedMessagesTable'
import { listMessages } from '../../services/api/messages_history'
import type { Workspace } from '../../services/api/types'

i18n.loadAndActivate({ locale: 'en', messages: {} })

const navigate = vi.fn()
vi.mock('@tanstack/react-router', () => ({
  useNavigate: () => navigate
}))

vi.mock('../../services/api/messages_history', () => ({
  listMessages: vi.fn()
}))

vi.mock('../messages/MessageHistoryTable', () => ({
  MessageHistoryTable: ({ messages, loading }: { messages: unknown[]; loading: boolean }) => (
    <div data-testid="table" data-loading={String(loading)} data-count={messages.length} />
  )
}))

const workspace = { id: 'ws1', settings: { timezone: 'UTC' } } as unknown as Workspace

const renderIt = () =>
  render(
    <I18nProvider i18n={i18n}>
      <FailedMessagesTable workspace={workspace} />
    </I18nProvider>
  )

describe('FailedMessagesTable (lot 4 : vue commerciale)', () => {
  beforeEach(() => {
    vi.clearAllMocks()
  })

  it('demande uniquement les échecs COMMERCIAUX et affiche les lignes reçues', async () => {
    vi.mocked(listMessages).mockResolvedValue({ messages: [{ id: 'm1' }, { id: 'm2' }] } as never)
    renderIt()
    await waitFor(() => expect(screen.getByTestId('table').getAttribute('data-count')).toBe('2'))
    expect(listMessages).toHaveBeenCalledWith('ws1', {
      limit: 5,
      is_failed: true,
      message_type: 'commercial'
    })
  })

  it("affiche l'erreur lisible quand la lecture échoue", async () => {
    vi.spyOn(console, 'error').mockImplementation(() => {})
    vi.mocked(listMessages).mockRejectedValue(new Error('boom'))
    renderIt()
    await waitFor(() => expect(screen.getByText(/boom/)).toBeTruthy())
  })

  it('« Voir plus » ouvre le journal commercial filtré sur les échecs', async () => {
    vi.mocked(listMessages).mockResolvedValue({ messages: [] } as never)
    renderIt()
    await waitFor(() => expect(listMessages).toHaveBeenCalled())
    fireEvent.click(screen.getByText('View more'))
    expect(navigate).toHaveBeenCalledWith({
      to: '/console/workspace/$workspaceId/logs',
      params: { workspaceId: 'ws1' },
      search: { is_failed: 'true', type: 'commercial' }
    })
  })
})
