import { beforeEach, describe, expect, it, vi } from 'vitest'
import { render, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { i18n } from '@lingui/core'
import { I18nProvider } from '@lingui/react'
import { App as AntApp } from 'antd'

import { ApiAgentsSettings } from './ApiAgentsSettings'

i18n.loadAndActivate({ locale: 'en', messages: {} })

vi.mock('../../services/api/workspace', async () => {
  const actual = await vi.importActual<typeof import('../../services/api/workspace')>(
    '../../services/api/workspace'
  )
  return {
    ...actual,
    workspaceService: {
      listAPIKeys: vi.fn(),
      createAPIKey: vi.fn(),
      revokeAPIKey: vi.fn(),
      createAgentInstallToken: vi.fn()
    }
  }
})

import { workspaceService } from '../../services/api/workspace'

const renderSettings = (canManageKeys = true) =>
  render(
    <I18nProvider i18n={i18n}>
      <AntApp>
        <ApiAgentsSettings workspaceId="ws-1" canManageKeys={canManageKeys} />
      </AntApp>
    </I18nProvider>
  )

describe('ApiAgentsSettings', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    vi.mocked(workspaceService.listAPIKeys).mockResolvedValue({ keys: [] })
  })

  it('lists existing API keys with masked email and no last-used date shown as "Not available"', async () => {
    vi.mocked(workspaceService.listAPIKeys).mockResolvedValue({
      keys: [
        {
          user_id: 'agent-1',
          name: 'agent-ci',
          masked_email: 'age***@notifuse.app',
          created_at: '2026-10-01T00:00:00Z',
          veridian_owned: false
        }
      ]
    })

    renderSettings()

    await waitFor(() => {
      expect(screen.getByText('agent-ci')).toBeInTheDocument()
    })
    expect(screen.getByText('age***@notifuse.app')).toBeInTheDocument()
    expect(screen.getByText('Not available')).toBeInTheDocument()
  })

  it('creates a key, shows the token once, and refreshes the list', async () => {
    const user = userEvent.setup()
    vi.mocked(workspaceService.createAPIKey).mockResolvedValue({
      token: 'jwt-token-abc',
      email: 'my_agent@notifuse.app'
    })

    renderSettings()

    await waitFor(() => expect(workspaceService.listAPIKeys).toHaveBeenCalled())

    await user.click(screen.getByRole('button', { name: /Create API Key/i }))
    const dialog = await screen.findByRole('dialog')
    const input = within(dialog).getByPlaceholderText('my_agent')
    await user.type(input, 'My Agent')

    await user.click(within(dialog).getByRole('button', { name: /^Create API Key$/i }))

    await waitFor(() => {
      expect(screen.getByDisplayValue('jwt-token-abc')).toBeInTheDocument()
    })
    expect(workspaceService.createAPIKey).toHaveBeenCalledWith({
      workspace_id: 'ws-1',
      email_prefix: 'my_agent'
    })
  })

  it('revokes a key after confirmation', async () => {
    const user = userEvent.setup()
    vi.mocked(workspaceService.listAPIKeys).mockResolvedValue({
      keys: [
        {
          user_id: 'agent-1',
          name: 'agent-ci',
          masked_email: 'age***@notifuse.app',
          created_at: '2026-10-01T00:00:00Z',
          veridian_owned: false
        }
      ]
    })
    vi.mocked(workspaceService.revokeAPIKey).mockResolvedValue({ status: 'success' })

    renderSettings()

    await waitFor(() => expect(screen.getByText('agent-ci')).toBeInTheDocument())

    const revokeButtons = screen.getAllByRole('button')
    const revokeBtn = revokeButtons.find((b) => b.querySelector('svg'))
    expect(revokeBtn).toBeTruthy()
    await user.click(revokeBtn!)

    const confirmButton = await screen.findByRole('button', { name: /^Revoke$/i })
    await user.click(confirmButton)

    await waitFor(() => {
      expect(workspaceService.revokeAPIKey).toHaveBeenCalledWith({
        workspace_id: 'ws-1',
        user_id: 'agent-1'
      })
    })
  })

  it('generates a one-time install command without ever showing the raw API key', async () => {
    const user = userEvent.setup()
    vi.mocked(workspaceService.createAgentInstallToken).mockResolvedValue({
      status: 'success',
      install_token: 'one-time-token-xyz',
      expires_at: new Date(Date.now() + 10 * 60 * 1000).toISOString(),
      expires_in_seconds: 600
    })

    renderSettings()
    await waitFor(() => expect(workspaceService.listAPIKeys).toHaveBeenCalled())

    await user.click(screen.getByRole('button', { name: /Connect my agent/i }))
    const dialog = await screen.findByRole('dialog')
    await user.click(within(dialog).getByRole('button', { name: /Generate command/i }))

    await waitFor(() => {
      expect(screen.getByDisplayValue(/one-time-token-xyz/)).toBeInTheDocument()
    })
    const commandField = screen.getByDisplayValue(/one-time-token-xyz/) as HTMLTextAreaElement
    expect(commandField.value).toContain('/agent/install.sh')
    expect(commandField.value).toContain('--token one-time-token-xyz')
    // The real API key must NEVER appear anywhere on this screen.
    expect(screen.queryByText(/jwt-token-abc/)).not.toBeInTheDocument()
  })

  it('shows the non-technical 3-step explanation on demand', async () => {
    const user = userEvent.setup()
    renderSettings()
    await waitFor(() => expect(workspaceService.listAPIKeys).toHaveBeenCalled())

    await user.click(screen.getByRole('button', { name: /Connect my agent/i }))
    await screen.findByRole('dialog')
    await user.click(screen.getByRole('button', { name: /not technical/i }))

    expect(screen.getByText(/Generate a one-time command/i)).toBeInTheDocument()
    expect(screen.getByText(/Give it to your AI agent/i)).toBeInTheDocument()
  })

  it('hides create/revoke actions for a member without manage rights', async () => {
    vi.mocked(workspaceService.listAPIKeys).mockResolvedValue({
      keys: [
        {
          user_id: 'agent-1',
          name: 'agent-ci',
          masked_email: 'age***@notifuse.app',
          created_at: '2026-10-01T00:00:00Z',
          veridian_owned: false
        }
      ]
    })

    renderSettings(false)

    await waitFor(() => expect(screen.getByText('agent-ci')).toBeInTheDocument())
    expect(screen.queryByRole('button', { name: /Create API Key/i })).not.toBeInTheDocument()
    expect(screen.queryByRole('button', { name: /Connect my agent/i })).not.toBeInTheDocument()
  })
})
