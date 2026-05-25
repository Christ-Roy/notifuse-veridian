import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, waitFor, fireEvent } from '@testing-library/react'
import { i18n } from '@lingui/core'
import { I18nProvider } from '@lingui/react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { VeridianMailAccountSettings } from './veridian_mail_account_settings'

i18n.loadAndActivate({ locale: 'en', messages: {} })

vi.mock('../../services/api/veridian_mail_provider', () => ({
  veridianMailProviderApi: {
    getChoice: vi.fn(),
    setChoice: vi.fn()
  }
}))

import { veridianMailProviderApi } from '../../services/api/veridian_mail_provider'

const renderSettings = (workspaceId: string) => {
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

describe('VeridianMailAccountSettings', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    vi.mocked(veridianMailProviderApi.getChoice).mockResolvedValue({
      workspace_id: 'ws-1',
      choice: 'smtp_generic',
      updated_at: ''
    })
  })

  it('renders the Card title and reassurance copy', async () => {
    renderSettings('ws-1')
    await waitFor(() => {
      expect(screen.getByRole('heading', { name: /Mail account/i })).toBeInTheDocument()
      expect(
        screen.getByText(/Choose how transactional emails/i)
      ).toBeInTheDocument()
    })
  })

  it('defaults to smtp_generic radio when API returns it', async () => {
    renderSettings('ws-1')
    await waitFor(() => {
      const radio = screen.getByRole('radio', {
        name: /Veridian generic sender/i
      }) as HTMLInputElement
      expect(radio.checked).toBe(true)
    })
  })

  it('renders the Connect my Gmail button with Hub redirect target', async () => {
    renderSettings('ws-1')
    await waitFor(() => {
      expect(screen.getByRole('button', { name: /Connect my Gmail/i })).toBeInTheDocument()
    })
  })

  it('shows the fallback warning only when hub_gmail is selected', async () => {
    vi.mocked(veridianMailProviderApi.getChoice).mockResolvedValue({
      workspace_id: 'ws-1',
      choice: 'hub_gmail',
      updated_at: ''
    })
    renderSettings('ws-1')
    await waitFor(() => {
      expect(
        screen.getByText(/sends will automatically fall back to the generic sender/i)
      ).toBeInTheDocument()
    })
  })

  it('does NOT show the fallback warning when smtp_generic is selected', async () => {
    renderSettings('ws-1')
    await waitFor(() => {
      expect(
        screen.queryByText(/sends will automatically fall back to the generic sender/i)
      ).not.toBeInTheDocument()
    })
  })

  it('calls setChoice when user clicks the hub_gmail radio', async () => {
    vi.mocked(veridianMailProviderApi.setChoice).mockResolvedValue({
      workspace_id: 'ws-1',
      choice: 'hub_gmail',
      updated_at: '2026-05-25T12:00:00Z'
    })
    renderSettings('ws-1')

    const hubRadio = await screen.findByRole('radio', { name: /My Gmail connected via Hub/i })
    fireEvent.click(hubRadio)

    await waitFor(() => {
      expect(veridianMailProviderApi.setChoice).toHaveBeenCalledWith('ws-1', 'hub_gmail')
    })
  })
})
