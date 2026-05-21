import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, act } from '@testing-library/react'
import { i18n } from '@lingui/core'
import { I18nProvider } from '@lingui/react'
import { App } from 'antd'
import { VeridianPaywallModal } from './veridian_paywall_modal'
import {
  VERIDIAN_PAYWALL_EVENT,
  VERIDIAN_HUB_SYNC_DEAD_EVENT
} from '../services/api/veridian_402_interceptor'

i18n.loadAndActivate({ locale: 'en', messages: {} })

const renderModal = () =>
  render(
    <I18nProvider i18n={i18n}>
      <App>
        <VeridianPaywallModal />
      </App>
    </I18nProvider>
  )

describe('VeridianPaywallModal', () => {
  beforeEach(() => {
    // Reset DOM between tests
    document.body.innerHTML = ''
  })

  it('mounts without crashing (event-driven, no visible UI before events)', () => {
    const { container } = renderModal()
    // Composant retourne null — il ne rend rien tant qu'aucun event reçu.
    // (Le wrapper Ant <App> ajoute sa propre div CSS-only, le composant
    // lui-même ne contribue à aucun DOM.)
    expect(container.querySelector('.ant-modal')).toBeNull()
    expect(container.querySelector('.ant-notification')).toBeNull()
  })

  it('subscribes to paywall events on mount and unsubscribes on unmount', () => {
    const addSpy = vi.spyOn(window, 'addEventListener')
    const removeSpy = vi.spyOn(window, 'removeEventListener')
    const { unmount } = renderModal()

    expect(addSpy).toHaveBeenCalledWith(VERIDIAN_PAYWALL_EVENT, expect.any(Function))
    expect(addSpy).toHaveBeenCalledWith(VERIDIAN_HUB_SYNC_DEAD_EVENT, expect.any(Function))

    unmount()
    expect(removeSpy).toHaveBeenCalledWith(VERIDIAN_PAYWALL_EVENT, expect.any(Function))
    expect(removeSpy).toHaveBeenCalledWith(VERIDIAN_HUB_SYNC_DEAD_EVENT, expect.any(Function))

    addSpy.mockRestore()
    removeSpy.mockRestore()
  })

  it('does not crash when receiving a paywall event', () => {
    renderModal()
    expect(() => {
      act(() => {
        window.dispatchEvent(
          new CustomEvent(VERIDIAN_PAYWALL_EVENT, {
            detail: {
              status: 402,
              error: 'Payment required',
              errorCode: 'tenant_suspended',
              tenantStatus: 'suspended'
            }
          })
        )
      })
    }).not.toThrow()
  })

  it('does not crash when receiving a hub_sync_dead event', () => {
    renderModal()
    expect(() => {
      act(() => {
        window.dispatchEvent(
          new CustomEvent(VERIDIAN_HUB_SYNC_DEAD_EVENT, {
            detail: { status: 503, errorCode: 'hub_sync_dead', retryAfter: 3600 }
          })
        )
      })
    }).not.toThrow()
  })
})
