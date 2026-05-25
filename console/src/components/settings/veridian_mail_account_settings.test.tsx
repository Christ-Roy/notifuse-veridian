import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, waitFor, fireEvent } from '@testing-library/react'
import { i18n } from '@lingui/core'
import { I18nProvider } from '@lingui/react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { VeridianMailAccountSettings } from './veridian_mail_account_settings'

i18n.loadAndActivate({ locale: 'en', messages: {} })

vi.mock('../../services/api/veridian_mail_accounts', () => ({
  veridianMailAccountsApi: {
    list: vi.fn(),
    setDefault: vi.fn()
  }
}))
vi.mock('../../services/api/veridian_mail_provider', () => ({
  veridianMailProviderApi: {
    getChoice: vi.fn(),
    setChoice: vi.fn()
  }
}))

import { veridianMailAccountsApi } from '../../services/api/veridian_mail_accounts'
import { veridianMailProviderApi } from '../../services/api/veridian_mail_provider'

const renderSettings = (workspaceId = 'ws-1') => {
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false } }
  })
  return render(
    <QueryClientProvider client={client}>
      <I18nProvider i18n={i18n}>
        <VeridianMailAccountSettings workspaceId={workspaceId} />
      </I18nProvider>
    </QueryClientProvider>
  )
}

describe('VeridianMailAccountSettings — vague 7 multi-comptes', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    vi.mocked(veridianMailProviderApi.getChoice).mockResolvedValue({
      workspace_id: 'ws-1',
      choice: 'smtp_generic',
      updated_at: ''
    })
    vi.mocked(veridianMailAccountsApi.list).mockResolvedValue({
      hub_available: false,
      accounts: []
    })
  })

  it('renders Mail account heading and both card sections', async () => {
    renderSettings()
    await waitFor(() => {
      expect(screen.getByRole('heading', { name: /Mail account/i })).toBeInTheDocument()
    })
    expect(screen.getByText(/Connected accounts/i)).toBeInTheDocument()
    expect(screen.getByText(/Sender preference/i)).toBeInTheDocument()
  })

  it('shows empty state when hub_available=false', async () => {
    vi.mocked(veridianMailAccountsApi.list).mockResolvedValue({
      hub_available: false,
      accounts: []
    })
    renderSettings()

    await waitFor(() => {
      expect(screen.getByText(/No accounts connected yet/i)).toBeInTheDocument()
    })
    expect(
      screen.getByRole('button', { name: /Connect your first Gmail account/i })
    ).toBeInTheDocument()
    expect(
      screen.getByRole('button', { name: /Connect a Microsoft account/i })
    ).toBeInTheDocument()
  })

  it('shows empty state when hub_available=true but accounts=[]', async () => {
    vi.mocked(veridianMailAccountsApi.list).mockResolvedValue({
      hub_available: true,
      accounts: []
    })
    renderSettings()
    await waitFor(() => {
      expect(screen.getByText(/No accounts connected yet/i)).toBeInTheDocument()
    })
  })

  it('renders connected accounts list with Default tag + needs_reauth badge', async () => {
    vi.mocked(veridianMailAccountsApi.list).mockResolvedValue({
      hub_available: true,
      accounts: [
        {
          id: 'acc1',
          provider: 'google',
          email: 'robert@gmail.com',
          name: 'Robert Brunon',
          is_default: true,
          needs_reauth: false,
          connected_at: '2026-05-20T10:00:00Z'
        },
        {
          id: 'acc2',
          provider: 'microsoft',
          email: 'robert@entreprise.com',
          name: 'Robert (work)',
          is_default: false,
          needs_reauth: true,
          connected_at: '2026-05-22T14:00:00Z'
        }
      ]
    })
    renderSettings()

    await waitFor(() => {
      expect(screen.getByText('robert@gmail.com')).toBeInTheDocument()
    })
    expect(screen.getByText('robert@entreprise.com')).toBeInTheDocument()
    expect(screen.getByText('Default')).toBeInTheDocument()
    expect(screen.getByText('Needs reauth')).toBeInTheDocument()
    expect(screen.getByRole('button', { name: /Reconnect/i })).toBeInTheDocument()
    // acc2 needs_reauth donc Set-as-default n'apparait PAS (priorite Reconnect)
    expect(screen.queryByText('Set as default')).not.toBeInTheDocument()
    // Bouton "Connect another Gmail account" maintenant (vs first)
    expect(
      screen.getByRole('button', { name: /Connect another Gmail account/i })
    ).toBeInTheDocument()
  })

  it('shows Set as default on a non-default healthy account and dispatches setDefault on click', async () => {
    vi.mocked(veridianMailAccountsApi.list).mockResolvedValue({
      hub_available: true,
      accounts: [
        {
          id: 'acc1',
          provider: 'google',
          email: 'a@b.com',
          name: 'A',
          is_default: true,
          needs_reauth: false,
          connected_at: '2026-05-20T10:00:00Z'
        },
        {
          id: 'acc2',
          provider: 'google',
          email: 'c@d.com',
          name: 'C',
          is_default: false,
          needs_reauth: false,
          connected_at: '2026-05-21T10:00:00Z'
        }
      ]
    })
    vi.mocked(veridianMailAccountsApi.setDefault).mockResolvedValue({
      hub_available: true,
      user_id: 'u-hub',
      account_id: 'acc2',
      is_default: true
    })
    renderSettings()

    await waitFor(() => {
      expect(screen.getByText('c@d.com')).toBeInTheDocument()
    })

    fireEvent.click(screen.getByText('Set as default'))

    await waitFor(() => {
      expect(veridianMailAccountsApi.setDefault).toHaveBeenCalledWith('acc2')
    })
  })

  it('defaults to smtp_generic radio when API returns it', async () => {
    renderSettings()
    await waitFor(() => {
      const radio = screen.getByRole('radio', {
        name: /Veridian generic sender/i
      }) as HTMLInputElement
      expect(radio.checked).toBe(true)
    })
  })

  it('mentions the default account email in the hub_gmail info alert', async () => {
    vi.mocked(veridianMailAccountsApi.list).mockResolvedValue({
      hub_available: true,
      accounts: [
        {
          id: 'acc1',
          provider: 'google',
          email: 'default@example.com',
          name: 'Default',
          is_default: true,
          needs_reauth: false,
          connected_at: '2026-05-20T10:00:00Z'
        }
      ]
    })
    vi.mocked(veridianMailProviderApi.getChoice).mockResolvedValue({
      workspace_id: 'ws-1',
      choice: 'hub_gmail',
      updated_at: ''
    })
    renderSettings()

    await waitFor(() => {
      expect(
        screen.getByText(/Sends will route through default@example.com/i)
      ).toBeInTheDocument()
    })
  })

  it('warns about fallback when hub_gmail selected but no default account', async () => {
    vi.mocked(veridianMailAccountsApi.list).mockResolvedValue({
      hub_available: true,
      accounts: []
    })
    vi.mocked(veridianMailProviderApi.getChoice).mockResolvedValue({
      workspace_id: 'ws-1',
      choice: 'hub_gmail',
      updated_at: ''
    })
    renderSettings()

    await waitFor(() => {
      expect(
        screen.getByText(/No default account selected/i)
      ).toBeInTheDocument()
    })
  })

  it('calls setChoice when user clicks the hub_gmail radio (vague 6 preserve)', async () => {
    vi.mocked(veridianMailProviderApi.setChoice).mockResolvedValue({
      workspace_id: 'ws-1',
      choice: 'hub_gmail',
      updated_at: '2026-05-25T12:00:00Z'
    })
    renderSettings()

    const hubRadio = await screen.findByRole('radio', { name: /My connected account/i })
    fireEvent.click(hubRadio)

    await waitFor(() => {
      expect(veridianMailProviderApi.setChoice).toHaveBeenCalledWith('ws-1', 'hub_gmail')
    })
  })

  it('mounts without crashing when both endpoints fail (defensive)', async () => {
    vi.mocked(veridianMailAccountsApi.list).mockResolvedValue({
      hub_available: false,
      accounts: []
    })
    vi.mocked(veridianMailProviderApi.getChoice).mockResolvedValue({
      workspace_id: 'ws-1',
      choice: 'smtp_generic',
      updated_at: ''
    })
    renderSettings()
    await waitFor(() => {
      expect(screen.getByRole('heading', { name: /Mail account/i })).toBeInTheDocument()
    })
    expect(screen.queryByText(/Something went wrong/i)).not.toBeInTheDocument()
  })

  it('test negatif : un shape avec is_default=false partout ne montre PAS de Default tag', async () => {
    vi.mocked(veridianMailAccountsApi.list).mockResolvedValue({
      hub_available: true,
      accounts: [
        {
          id: 'acc1',
          provider: 'google',
          email: 'a@b.com',
          name: 'A',
          is_default: false,
          needs_reauth: false,
          connected_at: '2026-05-20T10:00:00Z'
        }
      ]
    })
    renderSettings()
    await waitFor(() => {
      expect(screen.getByText('a@b.com')).toBeInTheDocument()
    })
    expect(screen.queryByText('Default')).not.toBeInTheDocument()
  })
})
