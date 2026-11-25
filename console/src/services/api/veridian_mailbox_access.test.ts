import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'

const navigate = vi.hoisted(() => vi.fn())
vi.mock('../../router', () => ({ router: { navigate } }))
vi.mock('./client', () => {
  class ApiError extends Error {
    constructor(
      message: string,
      public status: number,
      public data?: unknown
    ) {
      super(message)
      this.name = 'ApiError'
    }
  }
  return { ApiError, api: { get: vi.fn(), post: vi.fn() } }
})

import { ApiError, api } from './client'
import { accessState, mailboxAccessService, mailboxErrorKind, securityCode } from './veridian_mailbox_access'

describe('accessState', () => {
  it('révoqué prime sur fermé, fermé sur désactivé, sinon actif', () => {
    expect(accessState({ revoked_at: '2026-11-25T10:00:00Z', closed: true, disabled_at: 'x' })).toBe('revoked')
    expect(accessState({ closed: true, disabled_at: 'x' })).toBe('closed')
    expect(accessState({ disabled_at: '2026-11-25T10:00:00Z' })).toBe('disabled')
    expect(accessState({})).toBe('active')
  })
})

describe('securityCode', () => {
  it('lit ce que le serveur écrit', () => {
    expect(securityCode('SSL/TLS')).toBe('ssl')
    expect(securityCode('implicit')).toBe('ssl')
    expect(securityCode(' STARTTLS ')).toBe('starttls')
    expect(securityCode('off')).toBe('none')
  })
})

describe('mailboxErrorKind', () => {
  it('409 unavailable n’est pas « trop d’accès »', () => {
    expect(mailboxErrorKind(new ApiError('x', 409, { code: 'unavailable' }))).toBe('unavailable')
    expect(mailboxErrorKind(new ApiError('x', 409))).toBe('unknown')
  })
  it('le code du contrat prime sur l’état HTTP', () => {
    expect(mailboxErrorKind(new ApiError('x', 403, { code: 'owner_required' }))).toBe('owner_required')
    expect(mailboxErrorKind(new ApiError('x', 403, { code: 'forbidden' }))).toBe('forbidden')
    expect(mailboxErrorKind(new ApiError('x', 409, { code: 'too_many' }))).toBe('too_many')
    expect(mailboxErrorKind(new ApiError('x', 429, { code: 'rate_limited' }))).toBe('rate_limited')
  })
  it('retombe sur l’état HTTP, et sur unknown pour une erreur étrangère', () => {
    expect(mailboxErrorKind(new ApiError('x', 401))).toBe('session')
    expect(mailboxErrorKind(new ApiError('x', 403))).toBe('forbidden')
    expect(mailboxErrorKind(new ApiError('x', 429))).toBe('rate_limited')
    expect(mailboxErrorKind(new ApiError('x', 503))).toBe('unavailable')
    expect(mailboxErrorKind(new Error('boom'))).toBe('unknown')
  })
})

describe('mailboxAccessService', () => {
  beforeEach(() => vi.clearAllMocks())
  afterEach(() => vi.unstubAllGlobals())

  it('appelle les routes du contrat avec les bons corps', async () => {
    vi.mocked(api.get).mockResolvedValue({})
    vi.mocked(api.post).mockResolvedValue({})
    await mailboxAccessService.list('ws 1')
    expect(api.get).toHaveBeenCalledWith('/api/veridian/mail.access.list?workspace_id=ws+1')
    await mailboxAccessService.setDisabled('ws1', 'a1', true)
    expect(api.post).toHaveBeenLastCalledWith('/api/veridian/mail.access.setDisabled', {
      workspace_id: 'ws1',
      id: 'a1',
      disabled: true
    })
    await mailboxAccessService.revoke('ws1', 'a1')
    expect(api.post).toHaveBeenLastCalledWith('/api/veridian/mail.access.revoke', { workspace_id: 'ws1', id: 'a1' })
    await mailboxAccessService.profileLink('ws1', 'a1')
    expect(api.post).toHaveBeenLastCalledWith('/api/veridian/mail.access.profileLink', {
      workspace_id: 'ws1',
      id: 'a1'
    })
  })

  it('exporte par fetch avec l’en-tête Authorization, nom de fichier pris de Content-Disposition', async () => {
    localStorage.setItem('auth_token', 'tok')
    const fetchMock = vi.fn().mockResolvedValue(
      new Response('From x\n', { status: 200, headers: { 'Content-Disposition': 'attachment; filename="inbox.mbox"' } })
    )
    vi.stubGlobal('fetch', fetchMock)
    const file = await mailboxAccessService.exportMbox('ws1', 'p1', 'inbox')
    const [url, init] = fetchMock.mock.calls[0]
    expect(String(url)).toContain('/api/veridian/mail.export?workspace_id=ws1&profile_id=p1&folder=inbox')
    expect((init as RequestInit).headers).toEqual({ Authorization: 'Bearer tok' })
    expect(file.filename).toBe('inbox.mbox')
    expect(await file.blob.text()).toBe('From x\n')
  })

  it('un export refusé lève une ApiError avec le code du contrat', async () => {
    vi.stubGlobal(
      'fetch',
      vi.fn().mockResolvedValue(
        new Response(JSON.stringify({ error: 'no', code: 'forbidden', message: 'no' }), { status: 403 })
      )
    )
    await expect(mailboxAccessService.exportMbox('ws1', 'p1', 'sent')).rejects.toMatchObject({ status: 403 })
  })
})
