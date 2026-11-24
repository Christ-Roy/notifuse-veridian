import { describe, expect, it, vi, beforeEach } from 'vitest'
import { render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { i18n } from '@lingui/core'
import { I18nProvider } from '@lingui/react'
import { App } from 'antd'
import { WorkspaceMembers } from './WorkspaceMembers'
import { ApiError } from '../../services/api/client'

const inviteViaHub = vi.fn()
const inviteLocal = vi.fn()
vi.mock('../../services/api/workspace', () => ({
  workspaceService: {
    inviteMemberViaHub: (...a: unknown[]) => inviteViaHub(...a),
    inviteMember: (...a: unknown[]) => inviteLocal(...a),
    removeMember: vi.fn(),
    deleteInvitation: vi.fn()
  }
}))
vi.mock('../../services/api/veridian', () => ({
  veridianApi: { getMode: vi.fn().mockResolvedValue({ mode: 'veridian-managed' }) }
}))

i18n.loadAndActivate({ locale: 'en', messages: {} })

async function openAndSend(email: string) {
  const user = userEvent.setup()
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  render(
    <QueryClientProvider client={qc}>
      <I18nProvider i18n={i18n}>
        <App>
          <WorkspaceMembers
            workspaceId="ws1"
            members={[]}
            loading={false}
            onMembersChange={vi.fn()}
            isOwner={true}
          />
        </App>
      </I18nProvider>
    </QueryClientProvider>
  )
  await user.click(await screen.findByRole('button', { name: 'Invite Member' }))
  await user.type(await screen.findByPlaceholderText('Enter email address'), email)
  // le mode managed est charge de facon asynchrone : laisser la requete se resoudre
  await waitFor(() => expect(qc.getQueryData(['veridian-mode'])).toBeTruthy())
  await user.click(screen.getByRole('button', { name: 'Send Invitation' }))
}

describe('WorkspaceMembers: in managed mode, members are invited by the Hub only', () => {
  beforeEach(() => {
    inviteViaHub.mockReset()
    inviteLocal.mockReset().mockResolvedValue({ status: 'success' })
  })

  it.each([502, 503])('never creates a local invitation on a %i from the Hub route', async (status) => {
    inviteViaHub.mockRejectedValue(new ApiError('hub invitation flow disabled', status))
    await openAndSend('nouveau@example.com')
    expect(
      await screen.findByText('The Veridian Hub is temporarily unavailable. Please try again in a moment.')
    ).toBeInTheDocument()
    expect(inviteLocal).not.toHaveBeenCalled()
    expect(screen.queryByText('Invitation sent to nouveau@example.com')).not.toBeInTheDocument()
  })

  it('sends the invitation through the Hub', async () => {
    inviteViaHub.mockResolvedValue({ status: 'success' })
    await openAndSend('autre@example.com')
    await waitFor(() => expect(inviteViaHub).toHaveBeenCalledTimes(1))
    expect(inviteViaHub.mock.calls[0][0]).toEqual({ workspace_id: 'ws1', email: 'autre@example.com', role: 'member' })
    expect(inviteLocal).not.toHaveBeenCalled()
    expect(await screen.findByText('Invitation sent to autre@example.com')).toBeInTheDocument()
  })

  it('shows no permission table the Hub would ignore', async () => {
    inviteViaHub.mockResolvedValue({ status: 'success' })
    await openAndSend('droits@example.com')
    expect(screen.getByText(/joins with full permissions/)).toBeInTheDocument()
    expect(screen.queryAllByRole('switch')).toHaveLength(0)
  })
})
