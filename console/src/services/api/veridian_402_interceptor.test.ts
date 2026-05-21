import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import {
  installVeridianFetchInterceptor,
  VERIDIAN_PAYWALL_EVENT,
  VERIDIAN_HUB_SYNC_DEAD_EVENT,
  VERIDIAN_SOFT_DELETE_EVENT
} from './veridian_402_interceptor'

describe('veridian_402_interceptor', () => {
  let originalFetch: typeof window.fetch

  beforeEach(() => {
    originalFetch = window.fetch
    // Reset le flag idempotent pour réinstaller un wrap propre par test.
    window.__VERIDIAN_INTERCEPTOR_INSTALLED__ = false
  })

  afterEach(() => {
    window.fetch = originalFetch
    window.__VERIDIAN_INTERCEPTOR_INSTALLED__ = false
  })

  const mockFetch = (response: Partial<Response> & { headers?: Record<string, string> }) => {
    const headers = new Headers(response.headers || {})
    const body = response.body ?? ''
    const fakeResponse = new Response(typeof body === 'string' ? body : JSON.stringify(body), {
      status: response.status ?? 200,
      headers
    })
    window.fetch = vi.fn().mockResolvedValue(fakeResponse) as unknown as typeof window.fetch
  }

  it('does nothing on 200 OK without Veridian headers', async () => {
    mockFetch({ status: 200, body: JSON.stringify({ ok: true }) })
    installVeridianFetchInterceptor()
    const listener = vi.fn()
    window.addEventListener(VERIDIAN_PAYWALL_EVENT, listener)
    window.addEventListener(VERIDIAN_HUB_SYNC_DEAD_EVENT, listener)
    window.addEventListener(VERIDIAN_SOFT_DELETE_EVENT, listener)

    await window.fetch('/api/anything')
    // Laisse le micro-task d'inspection s'exécuter.
    await new Promise((r) => setTimeout(r, 0))

    expect(listener).not.toHaveBeenCalled()
  })

  it('dispatches paywall event on 402', async () => {
    mockFetch({
      status: 402,
      body: JSON.stringify({
        error: 'Payment required: suspended',
        error_code: 'tenant_suspended',
        tenant_status: 'suspended'
      })
    })
    installVeridianFetchInterceptor()

    const detail = await new Promise<unknown>((resolve) => {
      window.addEventListener(VERIDIAN_PAYWALL_EVENT, (e) => resolve((e as CustomEvent).detail), {
        once: true
      })
      window.fetch('/api/contacts.list')
    })

    expect(detail).toMatchObject({
      status: 402,
      errorCode: 'tenant_suspended',
      tenantStatus: 'suspended'
    })
  })

  it('dispatches hub_sync_dead event on 503 with proper error_code', async () => {
    mockFetch({
      status: 503,
      headers: { 'Retry-After': '3600' },
      body: JSON.stringify({ error: 'Hub silent', error_code: 'hub_sync_dead' })
    })
    installVeridianFetchInterceptor()

    const detail = await new Promise<unknown>((resolve) => {
      window.addEventListener(
        VERIDIAN_HUB_SYNC_DEAD_EVENT,
        (e) => resolve((e as CustomEvent).detail),
        { once: true }
      )
      window.fetch('/api/contacts.list')
    })

    expect(detail).toMatchObject({
      status: 503,
      errorCode: 'hub_sync_dead',
      retryAfter: 3600
    })
  })

  it('ignores 503 without error_code=hub_sync_dead', async () => {
    mockFetch({
      status: 503,
      body: JSON.stringify({ error: 'Generic outage' })
    })
    installVeridianFetchInterceptor()
    const listener = vi.fn()
    window.addEventListener(VERIDIAN_HUB_SYNC_DEAD_EVENT, listener)

    await window.fetch('/api/anything').catch(() => undefined)
    await new Promise((r) => setTimeout(r, 0))

    expect(listener).not.toHaveBeenCalled()
  })

  it('dispatches soft-delete event when X-Tenant-Soft-Deleted header is set', async () => {
    mockFetch({
      status: 200,
      headers: {
        'X-Tenant-Soft-Deleted': 'true',
        'X-Tenant-Deleted-At': '2026-05-21T10:00:00Z',
        'X-Tenant-Purge-At': '2026-06-20T10:00:00Z'
      },
      body: JSON.stringify({ contacts: [] })
    })
    installVeridianFetchInterceptor()

    const detail = await new Promise<unknown>((resolve) => {
      window.addEventListener(
        VERIDIAN_SOFT_DELETE_EVENT,
        (e) => resolve((e as CustomEvent).detail),
        { once: true }
      )
      window.fetch('/api/contacts.list')
    })

    expect(detail).toMatchObject({
      deletedAt: '2026-05-21T10:00:00Z',
      purgeAt: '2026-06-20T10:00:00Z'
    })
  })

  it('is idempotent — calling install twice does not double-wrap', async () => {
    const baseListener = vi.fn()
    mockFetch({ status: 200, body: '{}' })

    installVeridianFetchInterceptor()
    installVeridianFetchInterceptor()
    installVeridianFetchInterceptor()

    window.addEventListener(VERIDIAN_PAYWALL_EVENT, baseListener)

    await window.fetch('/api/x')
    await new Promise((r) => setTimeout(r, 0))

    expect(baseListener).not.toHaveBeenCalled()
  })
})
