import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { render, screen, waitFor, fireEvent } from '@testing-library/react'
import { i18n } from '@lingui/core'
import { I18nProvider } from '@lingui/react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { VeridianOAuthButtons } from './veridian_oauth_buttons'

i18n.loadAndActivate({ locale: 'en', messages: {} })

vi.mock('../services/api/veridian', () => ({
  veridianApi: {
    getMode: vi.fn()
  }
}))

import { veridianApi } from '../services/api/veridian'

const renderButtons = () => {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  return render(
    <QueryClientProvider client={client}>
      <I18nProvider i18n={i18n}>
        <VeridianOAuthButtons />
      </I18nProvider>
    </QueryClientProvider>
  )
}

const setHostname = (hostname: string) => {
  Object.defineProperty(window, 'location', {
    value: {
      ...window.location,
      hostname,
      href: `https://${hostname}/console/signin`
    },
    writable: true,
    configurable: true
  })
}

describe('VeridianOAuthButtons', () => {
  const originalLocation = window.location

  beforeEach(() => {
    vi.clearAllMocks()
    setHostname('notifuse.app.veridian.site')
  })

  afterEach(() => {
    Object.defineProperty(window, 'location', {
      value: originalLocation,
      writable: true,
      configurable: true
    })
  })

  it('renders nothing in self-hosted mode', async () => {
    vi.mocked(veridianApi.getMode).mockResolvedValue({
      mode: 'self-hosted',
      signin_url: '/console/signin'
    })
    const { container } = renderButtons()
    await waitFor(() => {
      expect(container.firstChild).toBeNull()
    })
  })

  it('renders nothing on API error (silent degradation)', async () => {
    vi.mocked(veridianApi.getMode).mockRejectedValue(new Error('boom'))
    const { container } = renderButtons()
    await waitFor(() => {
      expect(container.firstChild).toBeNull()
    })
  })

  it('renders nothing on staging hostname', async () => {
    setHostname('notifuse.staging.veridian.site')
    vi.mocked(veridianApi.getMode).mockResolvedValue({
      mode: 'veridian-managed',
      signin_url: '/console/signin',
      hub_url: 'https://hub.staging.veridian.site'
    })
    const { container } = renderButtons()
    // Wait for the query to settle, then ensure still empty.
    await waitFor(() => {
      // If the staging gate works, the container stays empty.
      expect(container.querySelector('[data-testid="veridian-oauth-buttons"]')).toBeNull()
    })
  })

  it('renders both OAuth buttons in veridian-managed mode on prod hostname', async () => {
    vi.mocked(veridianApi.getMode).mockResolvedValue({
      mode: 'veridian-managed',
      signin_url: '/console/signin',
      hub_url: 'https://app.veridian.site'
    })
    renderButtons()
    await waitFor(() => {
      expect(screen.getByRole('button', { name: /Continue with Google/i })).toBeInTheDocument()
      expect(screen.getByRole('button', { name: /Continue with Microsoft/i })).toBeInTheDocument()
    })
  })

  it('redirects to Hub /login with encoded next param when Google clicked', async () => {
    vi.mocked(veridianApi.getMode).mockResolvedValue({
      mode: 'veridian-managed',
      signin_url: '/console/signin',
      hub_url: 'https://app.veridian.site'
    })
    const hrefSetter = vi.fn()
    Object.defineProperty(window.location, 'href', {
      set: hrefSetter,
      get: () => 'https://notifuse.app.veridian.site/console/signin',
      configurable: true
    })
    renderButtons()
    const googleBtn = await screen.findByRole('button', { name: /Continue with Google/i })
    fireEvent.click(googleBtn)
    expect(hrefSetter).toHaveBeenCalledWith(
      'https://app.veridian.site/login?next=https%3A%2F%2Fnotifuse.app.veridian.site%2Fconsole%2Fsignin'
    )
  })

  it('redirects to Hub /login when Microsoft clicked', async () => {
    vi.mocked(veridianApi.getMode).mockResolvedValue({
      mode: 'veridian-managed',
      signin_url: '/console/signin',
      hub_url: 'https://app.veridian.site'
    })
    const hrefSetter = vi.fn()
    Object.defineProperty(window.location, 'href', {
      set: hrefSetter,
      get: () => 'https://notifuse.app.veridian.site/console/signin',
      configurable: true
    })
    renderButtons()
    const msBtn = await screen.findByRole('button', { name: /Continue with Microsoft/i })
    fireEvent.click(msBtn)
    expect(hrefSetter).toHaveBeenCalledTimes(1)
    const url = hrefSetter.mock.calls[0][0] as string
    expect(url).toMatch(/^https:\/\/app\.veridian\.site\/login\?next=/)
  })

  it('falls back to default Hub URL when hub_url not provided', async () => {
    vi.mocked(veridianApi.getMode).mockResolvedValue({
      mode: 'veridian-managed',
      signin_url: '/console/signin'
    })
    const hrefSetter = vi.fn()
    Object.defineProperty(window.location, 'href', {
      set: hrefSetter,
      get: () => 'https://notifuse.app.veridian.site/console/signin',
      configurable: true
    })
    renderButtons()
    const googleBtn = await screen.findByRole('button', { name: /Continue with Google/i })
    fireEvent.click(googleBtn)
    expect(hrefSetter).toHaveBeenCalledWith(
      expect.stringMatching(/^https:\/\/app\.veridian\.site\/login\?next=/)
    )
  })
})
