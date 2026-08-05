import { beforeEach, describe, expect, it, vi } from 'vitest'
import { render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { i18n } from '@lingui/core'
import { I18nProvider } from '@lingui/react'
import { App as AntApp } from 'antd'

import type { EmailProvider, Integration, Workspace } from '../../services/api/types'
import { Integrations } from './Integrations'

i18n.loadAndActivate({ locale: 'en', messages: {} })

vi.mock('../../services/api/workspace', async () => {
  const actual = await vi.importActual<typeof import('../../services/api/workspace')>(
    '../../services/api/workspace'
  )
  return {
    ...actual,
    workspaceService: {
      update: vi.fn().mockResolvedValue({}),
      get: vi.fn(),
      createIntegration: vi.fn(),
      updateIntegration: vi.fn(),
      deleteIntegration: vi.fn()
    }
  }
})

vi.mock('../../services/api/list', () => ({
  listsApi: { list: vi.fn().mockResolvedValue({ lists: [] }) }
}))

vi.mock('../../services/api/email', () => ({
  emailService: { testProvider: vi.fn() }
}))

vi.mock('../../services/api/veridian_email_profiles', async () => {
  const actual = await vi.importActual<typeof import('../../services/api/veridian_email_profiles')>(
    '../../services/api/veridian_email_profiles'
  )
  return {
    ...actual,
    emailProfilesUsageService: {
      get: vi.fn().mockResolvedValue({
        date: '2026-08-05',
        total_used: 12,
        profiles: [
          {
            integration_id: 'gmail-a',
            used: 12,
            cap: 30,
            remaining: 18,
            by_provider_class: { google: 7, microsoft: 5 }
          },
          {
            integration_id: 'gmail-b',
            used: 0,
            cap: 30,
            remaining: 30,
            by_provider_class: {}
          }
        ]
      })
    }
  }
})

import { workspaceService } from '../../services/api/workspace'
import { emailProfilesUsageService } from '../../services/api/veridian_email_profiles'
import { emailService } from '../../services/api/email'

const gmailProvider = (email: string, verified = true): EmailProvider => ({
  kind: 'smtp',
  rate_limit_per_minute: 1,
  veridian_profile_daily_cap: 30,
  veridian_credentials_configured: true,
  veridian_transport_verified_at: verified ? '2026-08-05T12:00:00Z' : undefined,
  smtp: {
    host: 'smtp.gmail.com',
    port: 587,
    username: email,
    use_tls: true,
    auth_type: 'basic',
    has_password: true
  },
  senders: [{ id: email, email, name: email.split('@')[0], is_default: true }]
})

const profile = (id: string, email: string): Integration => ({
  id,
  name: id === 'gmail-a' ? 'Gmail principal' : 'Gmail secondaire',
  type: 'email',
  email_provider: gmailProvider(email),
  created_at: '',
  updated_at: ''
})

const workspace = {
  id: 'ws-1',
  name: 'Workspace',
  created_at: '',
  updated_at: '',
  settings: {
    timezone: 'Europe/Paris',
    email_tracking_enabled: true,
    default_language: 'fr',
    languages: ['fr'],
    marketing_email_provider_id: 'gmail-a',
    veridian_marketing_email_provider_ids: ['gmail-a']
  },
  integrations: [
    profile('gmail-a', 'principal@gmail.com'),
    profile('gmail-b', 'secondaire@gmail.com')
  ]
} as Workspace

const renderIntegrations = (onSave = vi.fn()) =>
  render(
    <I18nProvider i18n={i18n}>
      <AntApp>
        <Integrations workspace={workspace} loading={false} onSave={onSave} isOwner />
      </AntApp>
    </I18nProvider>
  )

describe('Integrations Gmail sending profile wiring', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    vi.mocked(workspaceService.get).mockResolvedValue({ workspace } as never)
    vi.mocked(emailService.testProvider).mockResolvedValue({ success: true })
  })

  it('loads authoritative usage and adds a second profile to backend rotation settings', async () => {
    const user = userEvent.setup()
    const onSave = vi.fn()
    renderIntegrations(onSave)

    await waitFor(() => expect(emailProfilesUsageService.get).toHaveBeenCalledWith('ws-1'))
    expect(await screen.findByText('12 / 30 sent today')).toBeInTheDocument()

    await user.click(screen.getByRole('button', { name: 'Add to rotation' }))
    await user.click(await screen.findByRole('button', { name: 'Yes' }))

    await waitFor(() => expect(workspaceService.update).toHaveBeenCalledTimes(1))
    expect(vi.mocked(workspaceService.update).mock.calls[0][0].settings).toMatchObject({
      marketing_email_provider_id: 'gmail-a',
      veridian_marketing_email_provider_ids: ['gmail-a', 'gmail-b']
    })
    expect(onSave).toHaveBeenCalledWith(workspace)
  })

  it('keeps the write-only credential input empty while editing', async () => {
    const user = userEvent.setup()
    renderIntegrations()

    await user.click(screen.getByRole('button', { name: 'Edit Gmail principal' }))

    expect(screen.getByLabelText('Google app password')).toHaveValue('')
    expect(
      screen.getByText('Leave blank to keep the currently saved app password.')
    ).toBeInTheDocument()
  })

  it('tests only a saved integration and refreshes persisted verification state', async () => {
    const user = userEvent.setup()
    const onSave = vi.fn()
    renderIntegrations(onSave)

    await user.click(screen.getByRole('button', { name: 'Test Gmail secondaire' }))
    await user.type(screen.getByPlaceholderText('recipient@example.com'), 'recipient@example.com')
    await user.click(screen.getByRole('button', { name: 'Send Test Email' }))

    await waitFor(() =>
      expect(emailService.testProvider).toHaveBeenCalledWith(
        'ws-1',
        'gmail-b',
        'recipient@example.com'
      )
    )
    expect(workspaceService.get).toHaveBeenCalledWith('ws-1')
    expect(onSave).toHaveBeenCalledWith(workspace)
  }, 15_000)

  it('does not allow an unverified profile to join rotation', async () => {
    const unverifiedWorkspace = {
      ...workspace,
      integrations: [
        workspace.integrations![0],
        {
          ...workspace.integrations![1],
          email_provider: gmailProvider('secondaire@gmail.com', false)
        }
      ]
    } as Workspace

    render(
      <I18nProvider i18n={i18n}>
        <AntApp>
          <Integrations workspace={unverifiedWorkspace} loading={false} onSave={vi.fn()} isOwner />
        </AntApp>
      </I18nProvider>
    )

    expect(await screen.findByText('Not tested')).toBeInTheDocument()
    expect(screen.getByRole('button', { name: 'Add to rotation' })).toBeDisabled()
  })

  it('does not invent a sent or sendable state when the usage API is unavailable', async () => {
    vi.mocked(emailProfilesUsageService.get).mockRejectedValueOnce(new Error('unavailable'))
    renderIntegrations()

    expect(await screen.findByText("Today's usage is unavailable")).toBeInTheDocument()
    expect(screen.queryByText(/emails sent today/)).not.toBeInTheDocument()
    expect(screen.queryByText(/Ready to send/i)).not.toBeInTheDocument()
  })
})
