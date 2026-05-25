import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, waitFor } from '@testing-library/react'
import { i18n } from '@lingui/core'
import { I18nProvider } from '@lingui/react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { VeridianCrossAppCards } from './veridian_cross_app_cards'

i18n.loadAndActivate({ locale: 'en', messages: {} })

let mockAuth = {
  user: null as { id: string; email: string } | null,
  isAuthenticated: false,
  workspaces: [],
  loading: false,
  signin: vi.fn(),
  signout: vi.fn(),
  refreshWorkspaces: vi.fn()
}

vi.mock('../contexts/AuthContext', () => ({
  useAuth: () => mockAuth
}))

vi.mock('../services/api/veridian_hub_discovery', () => ({
  veridianHubDiscoveryApi: {
    lookupMe: vi.fn()
  }
}))

vi.mock('../services/api/veridian', () => ({
  veridianApi: {
    getMode: vi.fn()
  }
}))

import { veridianHubDiscoveryApi } from '../services/api/veridian_hub_discovery'
import { veridianApi } from '../services/api/veridian'

const renderCards = () => {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  return render(
    <QueryClientProvider client={client}>
      <I18nProvider i18n={i18n}>
        <VeridianCrossAppCards />
      </I18nProvider>
    </QueryClientProvider>
  )
}

describe('VeridianCrossAppCards', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    mockAuth = {
      user: { id: 'u1', email: 'a@b.c' },
      isAuthenticated: true,
      workspaces: [],
      loading: false,
      signin: vi.fn(),
      signout: vi.fn(),
      refreshWorkspaces: vi.fn()
    }
    vi.mocked(veridianApi.getMode).mockResolvedValue({
      mode: 'veridian-managed',
      signin_url: '/console/signin',
      hub_url: 'https://app.veridian.site'
    })
  })

  it('renders nothing when discovery returns null (Hub disabled/down)', async () => {
    vi.mocked(veridianHubDiscoveryApi.lookupMe).mockResolvedValue(null)
    const { container } = renderCards()
    await waitFor(() => {
      expect(container.querySelector('[data-testid="veridian-cross-app-cards"]')).toBeNull()
    })
  })

  it('renders nothing when hub_available=false', async () => {
    vi.mocked(veridianHubDiscoveryApi.lookupMe).mockResolvedValue({
      hub_available: false,
      exists: false,
      tenants: []
    })
    const { container } = renderCards()
    await waitFor(() => {
      expect(container.querySelector('[data-testid="veridian-cross-app-cards"]')).toBeNull()
    })
  })

  it('renders nothing when user has only notifuse tenant', async () => {
    vi.mocked(veridianHubDiscoveryApi.lookupMe).mockResolvedValue({
      hub_available: true,
      exists: true,
      tenants: [{ app: 'notifuse', role: 'owner' }]
    })
    const { container } = renderCards()
    await waitFor(() => {
      expect(container.querySelector('[data-testid="veridian-cross-app-cards"]')).toBeNull()
    })
  })

  it('renders a Prospection card when user has prospection tenant', async () => {
    vi.mocked(veridianHubDiscoveryApi.lookupMe).mockResolvedValue({
      hub_available: true,
      exists: true,
      tenants: [
        { app: 'notifuse', role: 'owner' },
        { app: 'prospection', role: 'owner' }
      ]
    })
    renderCards()
    await waitFor(() => {
      expect(screen.getByTestId('veridian-cross-app-card-prospection')).toBeInTheDocument()
    })
    expect(screen.getByText('Prospection')).toBeInTheDocument()
    // Notifuse card NEVER rendered (we're on Notifuse).
    expect(screen.queryByTestId('veridian-cross-app-card-notifuse')).toBeNull()
  })

  it('renders multiple cross-app cards when user has prospection + analytics', async () => {
    vi.mocked(veridianHubDiscoveryApi.lookupMe).mockResolvedValue({
      hub_available: true,
      exists: true,
      tenants: [
        { app: 'notifuse', role: 'owner' },
        { app: 'prospection', role: 'owner' },
        { app: 'analytics', role: 'member' }
      ]
    })
    renderCards()
    await waitFor(() => {
      expect(screen.getByTestId('veridian-cross-app-card-prospection')).toBeInTheDocument()
    })
    expect(screen.getByTestId('veridian-cross-app-card-analytics')).toBeInTheDocument()
  })

  it('does not call discovery endpoint when unauthenticated', async () => {
    mockAuth.isAuthenticated = false
    mockAuth.user = null
    vi.mocked(veridianHubDiscoveryApi.lookupMe).mockResolvedValue({
      hub_available: true,
      exists: true,
      tenants: [{ app: 'prospection', role: 'owner' }]
    })
    renderCards()
    // Hook disabled when not authenticated → queryFn must not fire.
    await new Promise((res) => setTimeout(res, 20))
    expect(veridianHubDiscoveryApi.lookupMe).not.toHaveBeenCalled()
  })
})
