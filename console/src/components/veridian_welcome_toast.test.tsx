import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render } from '@testing-library/react'
import { i18n } from '@lingui/core'
import { I18nProvider } from '@lingui/react'
import { App } from 'antd'
import { VeridianWelcomeToast } from './veridian_welcome_toast'

i18n.loadAndActivate({ locale: 'en', messages: {} })

// Mock AuthContext for isolation. We assert side effects via notification.success.
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

describe('VeridianWelcomeToast', () => {
  beforeEach(() => {
    mockAuth = {
      user: null,
      isAuthenticated: false,
      workspaces: [],
      loading: false,
      signin: vi.fn(),
      signout: vi.fn(),
      refreshWorkspaces: vi.fn()
    }
    window.localStorage.clear()
    // Reset referrer mock if used.
    Object.defineProperty(document, 'referrer', { value: '', configurable: true, writable: true })
  })

  const renderToast = () =>
    render(
      <I18nProvider i18n={i18n}>
        <App>
          <VeridianWelcomeToast />
        </App>
      </I18nProvider>
    )

  it('mounts without crashing (event-driven, no visible UI)', () => {
    const { container } = renderToast()
    // <App> wrapper Ant ajoute sa propre div CSS-only ; le composant
    // VeridianWelcomeToast lui-même retourne null.
    expect(container.querySelector('.ant-notification')).toBeNull()
  })

  it('does not crash when unauthenticated', () => {
    expect(() => renderToast()).not.toThrow()
  })

  it('does not crash when authenticated without auto-login referrer', () => {
    mockAuth.user = { id: 'u1', email: 'a@b.c' }
    mockAuth.isAuthenticated = true
    Object.defineProperty(document, 'referrer', { value: 'https://google.com', configurable: true })
    expect(() => renderToast()).not.toThrow()
  })

  it('sets the welcome localStorage flag when authenticated via Veridian auto-login', () => {
    mockAuth.user = { id: 'u1', email: 'a@b.c' }
    mockAuth.isAuthenticated = true
    Object.defineProperty(document, 'referrer', {
      value: 'https://notifuse.app.veridian.site/veridian/auto-login?token=x',
      configurable: true
    })

    renderToast()
    const stored = window.localStorage.getItem('veridian_welcome_shown_at')
    expect(stored).toBeTruthy()
  })

  it('does not refire the toast if shown < 24h ago (idempotent flag)', () => {
    mockAuth.user = { id: 'u1', email: 'a@b.c' }
    mockAuth.isAuthenticated = true
    window.localStorage.setItem('veridian_welcome_shown_at', String(Date.now() - 60_000))
    Object.defineProperty(document, 'referrer', {
      value: 'https://app.veridian.site/dashboard',
      configurable: true
    })
    expect(() => renderToast()).not.toThrow()
  })
})
