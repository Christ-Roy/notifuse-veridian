import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { render, waitFor } from '@testing-library/react'
import { i18n } from '@lingui/core'
import { I18nProvider } from '@lingui/react'

i18n.loadAndActivate({ locale: 'en', messages: {} })

const navigate = vi.fn()
vi.mock('@tanstack/react-router', () => ({
  Outlet: () => null,
  useNavigate: () => navigate,
  useMatch: () => null
}))
vi.mock('../contexts/AuthContext', () => ({
  useAuth: () => ({ isAuthenticated: false, loading: false, workspaces: [] })
}))
vi.mock('../services/api/veridian', () => ({ veridianApi: { getMode: vi.fn() } }))

import { veridianApi } from '../services/api/veridian'
import { RootLayout } from './RootLayout'
import { clearBounceMarks, markExplicitLogout, markAutoBounceAttempt } from '../lib/veridian_auto_bounce'

const PAGE = 'https://notifuse.app.veridian.site/console/workspace/ws1/contacts'

describe('RootLayout — rebond automatique vers le Hub', () => {
  const originalLocation = window.location
  const replace = vi.fn()

  beforeEach(() => {
    vi.clearAllMocks()
    clearBounceMarks()
    window.IS_INSTALLED = true
    localStorage.removeItem('auth_token')
    Object.defineProperty(window, 'location', {
      value: { ...originalLocation, pathname: '/console/workspace/ws1/contacts', search: '', href: PAGE, replace },
      writable: true,
      configurable: true
    })
    vi.mocked(veridianApi.getMode).mockResolvedValue({
      mode: 'veridian-managed',
      signin_url: '/console/signin',
      hub_url: 'https://app.veridian.site'
    })
  })

  afterEach(() => {
    Object.defineProperty(window, 'location', { value: originalLocation, writable: true, configurable: true })
  })

  const mount = () =>
    render(
      <I18nProvider i18n={i18n}>
        <RootLayout />
      </I18nProvider>
    )

  it('sans session : repart vers le Hub avec la page demandée, une seule fois', async () => {
    mount()
    await waitFor(() => expect(replace).toHaveBeenCalledTimes(1))
    expect(replace).toHaveBeenCalledWith(
      'https://app.veridian.site/api/auth/bounce/prepare?next=' + encodeURIComponent(PAGE) + '&auto=1'
    )
    expect(navigate).not.toHaveBeenCalled()
    expect(document.cookie).toContain('veridian-auto-bounce=1')
  })

  it('au retour du Hub sans session : page de connexion, pas de second rebond', async () => {
    markAutoBounceAttempt()
    mount()
    await waitFor(() => expect(navigate).toHaveBeenCalled())
    expect(replace).not.toHaveBeenCalled()
    expect(navigate.mock.calls[0][0].to).toBe('/console/signin')
  })

  it('après une déconnexion explicite : page de connexion, jamais de reconnexion silencieuse', async () => {
    markExplicitLogout()
    mount()
    await waitFor(() => expect(navigate).toHaveBeenCalled())
    expect(replace).not.toHaveBeenCalled()
  })

  it('instance autonome : page de connexion habituelle', async () => {
    vi.mocked(veridianApi.getMode).mockResolvedValue({ mode: 'self-hosted', signin_url: '/console/signin' })
    mount()
    await waitFor(() => expect(navigate).toHaveBeenCalled())
    expect(replace).not.toHaveBeenCalled()
  })
})
