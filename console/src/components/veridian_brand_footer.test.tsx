import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, waitFor } from '@testing-library/react'
import { i18n } from '@lingui/core'
import { I18nProvider } from '@lingui/react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { VeridianBrandFooter } from './veridian_brand_footer'

i18n.loadAndActivate({ locale: 'en', messages: {} })

vi.mock('../services/api/veridian', () => ({
  veridianApi: {
    getMode: vi.fn()
  }
}))

import { veridianApi } from '../services/api/veridian'

const renderFooter = () => {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  return render(
    <QueryClientProvider client={client}>
      <I18nProvider i18n={i18n}>
        <VeridianBrandFooter />
      </I18nProvider>
    </QueryClientProvider>
  )
}

describe('VeridianBrandFooter', () => {
  beforeEach(() => {
    vi.clearAllMocks()
  })

  it('renders nothing in self-hosted mode', async () => {
    vi.mocked(veridianApi.getMode).mockResolvedValue({
      mode: 'self-hosted',
      signin_url: '/console/signin'
    })
    const { container } = renderFooter()
    await waitFor(() => {
      expect(container.firstChild).toBeNull()
    })
  })

  it('renders "Powered by Veridian" link in managed mode', async () => {
    vi.mocked(veridianApi.getMode).mockResolvedValue({
      mode: 'veridian-managed',
      signin_url: '/console/signin',
      hub_url: 'https://app.veridian.site'
    })
    renderFooter()
    await waitFor(() => {
      expect(screen.getByText(/Powered by Veridian/i)).toBeInTheDocument()
      expect(screen.getByRole('link', { name: /Manage subscription/i })).toBeInTheDocument()
    })
  })

  it('falls back to default hub URL when not provided', async () => {
    vi.mocked(veridianApi.getMode).mockResolvedValue({
      mode: 'veridian-managed',
      signin_url: '/console/signin'
    })
    renderFooter()
    await waitFor(() => {
      const link = screen.getByRole('link', { name: /Manage subscription/i })
      expect(link).toHaveAttribute('href', 'https://app.veridian.site/dashboard')
    })
  })

  it('renders nothing on API error (silent degradation)', async () => {
    vi.mocked(veridianApi.getMode).mockRejectedValue(new Error('boom'))
    const { container } = renderFooter()
    await waitFor(() => {
      expect(container.firstChild).toBeNull()
    })
  })
})
