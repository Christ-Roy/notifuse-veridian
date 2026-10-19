import { describe, expect, it, vi, beforeEach } from 'vitest'
import { render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { i18n } from '@lingui/core'
import { I18nProvider } from '@lingui/react'
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
        <WorkspaceMembers
          workspaceId="ws1"
          members={[]}
          loading={false}
          onMembersChange={vi.fn()}
          isOwner={true}
        />
      </I18nProvider>
    </QueryClientProvider>
  )
  await user.click(await screen.findByRole('button', { name: 'Invite Member' }))
  await user.type(await screen.findByPlaceholderText('Enter email address'), email)
  // le mode managed est charge de facon asynchrone : laisser la requete se resoudre
  await waitFor(() => expect(qc.getQueryData(['veridian-mode'])).toBeTruthy())
  await user.click(screen.getByRole('button', { name: 'Send Invitation' }))
}

describe('WorkspaceMembers: invitation when the Hub flow is not configured', () => {
  beforeEach(() => {
    inviteViaHub.mockReset()
    inviteLocal.mockReset().mockResolvedValue({ status: 'success' })
  })

  it('falls back to the local invitation on a 503 from the Hub route', async () => {
    inviteViaHub.mockRejectedValue(new ApiError('hub invitation flow disabled (self-hosted mode)', 503))
    await openAndSend('nouveau@example.com')
    await waitFor(() => expect(inviteLocal).toHaveBeenCalledTimes(1))
    expect(inviteLocal.mock.calls[0][0]).toMatchObject({ workspace_id: 'ws1', email: 'nouveau@example.com' })
  })

  it('uses the Hub only when it answers (no local invitation)', async () => {
    inviteViaHub.mockResolvedValue({ status: 'success' })
    await openAndSend('autre@example.com')
    await waitFor(() => expect(inviteViaHub).toHaveBeenCalledTimes(1))
    expect(inviteLocal).not.toHaveBeenCalled()
  })

  it('does not fall back on another error (502 Hub down stays an error)', async () => {
    inviteViaHub.mockRejectedValue(new ApiError('bad gateway', 502))
    await openAndSend('x@example.com')
    await waitFor(() => expect(inviteViaHub).toHaveBeenCalledTimes(1))
    expect(inviteLocal).not.toHaveBeenCalled()
  })
})
