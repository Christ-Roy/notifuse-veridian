import { describe, expect, it, vi } from 'vitest'
import { render, screen, within } from '@testing-library/react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { i18n } from '@lingui/core'
import { I18nProvider } from '@lingui/react'
import { App } from 'antd'
import { WorkspaceMembers } from './WorkspaceMembers'
import type { WorkspaceMember } from '../../services/api/types'

vi.mock('../../services/api/workspace', () => ({
  workspaceService: { removeMember: vi.fn(), deleteInvitation: vi.fn(), inviteMemberViaHub: vi.fn() }
}))
vi.mock('../../services/api/veridian', () => ({
  veridianApi: { getMode: vi.fn().mockResolvedValue({ mode: 'veridian-managed' }) }
}))

i18n.loadAndActivate({ locale: 'en', messages: {} })

const noPerms = {} as WorkspaceMember['permissions']
const member = (user_id: string, email: string, role: string): WorkspaceMember => ({
  user_id,
  workspace_id: 'ws1',
  role,
  email,
  type: 'user',
  created_at: '2026-11-01T00:00:00Z',
  updated_at: '2026-11-01T00:00:00Z',
  permissions: noPerms
})

const members = [
  member('u-own', 'proprio@example.com', 'owner'),
  member('u-adm', 'admin@example.com', 'admin'),
  member('u-mem', 'membre@example.com', 'member')
]

function renderMembers(props: { isOwner: boolean; currentUserId?: string }) {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  render(
    <QueryClientProvider client={qc}>
      <I18nProvider i18n={i18n}>
        <App>
          <WorkspaceMembers
            workspaceId="ws1"
            members={members}
            loading={false}
            onMembersChange={vi.fn()}
            {...props}
          />
        </App>
      </I18nProvider>
    </QueryClientProvider>
  )
}

const rowOf = (email: string) => screen.getByText(email).closest('tr') as HTMLElement
const removeButtons = (row: HTMLElement) => within(row).queryAllByRole('button', { name: /remove member/i })
const editButtons = (row: HTMLElement) => within(row).queryAllByRole('button', { name: /edit permissions/i })

describe('WorkspaceMembers : role admin', () => {
  it('affiche « Veridian Admin » pour un admin, avec toutes les permissions (rien a editer)', () => {
    renderMembers({ isOwner: true, currentUserId: 'u-own' })
    const row = rowOf('admin@example.com')
    expect(within(row).getByText('Veridian Admin')).toBeInTheDocument()
    expect(within(row).getByText('Full Access')).toBeInTheDocument()
    expect(editButtons(row)).toHaveLength(0)
    // le membre simple garde son tableau de droits editable
    expect(editButtons(rowOf('membre@example.com'))).toHaveLength(1)
  })

  it('le bouton retirer est present sur un admin, absent sur un proprietaire', () => {
    renderMembers({ isOwner: true, currentUserId: 'u-own' })
    expect(removeButtons(rowOf('admin@example.com'))).toHaveLength(1)
    expect(removeButtons(rowOf('membre@example.com'))).toHaveLength(1)
    expect(removeButtons(rowOf('proprio@example.com'))).toHaveLength(0)
  })

  it('un admin connecte peut retirer un membre mais ni un proprietaire ni lui-meme', () => {
    renderMembers({ isOwner: true, currentUserId: 'u-adm' })
    expect(removeButtons(rowOf('membre@example.com'))).toHaveLength(1)
    expect(removeButtons(rowOf('proprio@example.com'))).toHaveLength(0)
    expect(removeButtons(rowOf('admin@example.com'))).toHaveLength(0)
  })

  it('un simple membre ne voit aucun bouton', () => {
    renderMembers({ isOwner: false, currentUserId: 'u-mem' })
    expect(screen.queryAllByRole('button', { name: /remove member/i })).toHaveLength(0)
  })
})
