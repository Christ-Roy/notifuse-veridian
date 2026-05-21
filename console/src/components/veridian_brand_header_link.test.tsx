import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, waitFor } from '@testing-library/react'
import { i18n } from '@lingui/core'
import { I18nProvider } from '@lingui/react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { VeridianBrandHeaderLink } from './veridian_brand_header_link'

i18n.loadAndActivate({ locale: 'en', messages: {} })

vi.mock('../services/api/veridian', () => ({
  veridianApi: {
    getMode: vi.fn()
  }
}))

import { veridianApi } from '../services/api/veridian'

const renderLink = () => {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  return render(
    <QueryClientProvider client={client}>
      <I18nProvider i18n={i18n}>
        <VeridianBrandHeaderLink />
      </I18nProvider>
    </QueryClientProvider>
  )
}

describe('VeridianBrandHeaderLink', () => {
  beforeEach(() => {
    vi.clearAllMocks()
  })

  it('renders nothing on self-hosted mode', async () => {
    vi.mocked(veridianApi.getMode).mockResolvedValue({
      mode: 'self-hosted',
      signin_url: '/console/signin'
    })
    const { container } = renderLink()
    await waitFor(() => {
      // Le query render-nothing reste null durant et après le fetch self-hosted.
      expect(container.firstChild).toBeNull()
    })
  })

  it('renders the link when veridian-managed with hub_url', async () => {
    vi.mocked(veridianApi.getMode).mockResolvedValue({
      mode: 'veridian-managed',
      signin_url: '/console/signin',
      hub_url: 'https://app.veridian.site'
    })
    renderLink()
    await waitFor(() => {
      expect(screen.getByRole('button', { name: /Veridian dashboard/i })).toBeInTheDocument()
    })
  })

  it('renders nothing when hub_url is missing', async () => {
    vi.mocked(veridianApi.getMode).mockResolvedValue({
      mode: 'veridian-managed',
      signin_url: '/console/signin'
    })
    const { container } = renderLink()
    await waitFor(() => {
      expect(container.firstChild).toBeNull()
    })
  })

  it('renders nothing on API error', async () => {
    vi.mocked(veridianApi.getMode).mockRejectedValue(new Error('boom'))
    const { container } = renderLink()
    await waitFor(() => {
      expect(container.firstChild).toBeNull()
    })
  })
})
