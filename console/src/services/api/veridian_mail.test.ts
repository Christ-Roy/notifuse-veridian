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
import {
  mailErrorKind,
  mailListQuery,
  mailQuery,
  mailService,
  refusalOf,
  runMailEventStream,
  SseParser,
  type MailStreamEvent
} from './veridian_mail'

const enc = new TextEncoder()

function streamOf(chunks: string[], hold = false): ReadableStream<Uint8Array> {
  return new ReadableStream<Uint8Array>({
    start(controller) {
      for (const c of chunks) controller.enqueue(enc.encode(c))
      if (!hold) controller.close()
    }
  })
}

const sse = (seq: number, extra: Record<string, unknown> = {}) =>
  `id: ${seq}\nevent: message.received\ndata: ${JSON.stringify({ id: `m${seq}`, seq, kind: 'reply_human', thread_id: 't1', ...extra })}\n\n`

describe('mailQuery / mailListQuery', () => {
  it('omet les valeurs vides et joint les listes par des virgules', () => {
    expect(mailQuery({ a: 'x', b: '', c: undefined, d: null, e: ['p', 'q'], f: [], g: false })).toBe('a=x&e=p%2Cq&g=false')
  })

  it('messages.list : natures, traité, recherche rognée, corps seulement si demandé', () => {
    const qs = new URLSearchParams(
      mailListQuery({
        workspace_id: 'ws',
        mailbox: 'prof-1',
        kind: ['bounce_hard', 'bounce_soft'],
        handled: false,
        q: '  devis ',
        body: true,
        limit: 30,
        cursor: 'abc'
      })
    )
    expect(qs.get('workspace_id')).toBe('ws')
    expect(qs.get('mailbox')).toBe('prof-1')
    expect(qs.get('kind')).toBe('bounce_hard,bounce_soft')
    expect(qs.get('handled')).toBe('false')
    expect(qs.get('q')).toBe('devis')
    expect(qs.get('body')).toBe('true')
    expect(qs.get('limit')).toBe('30')
    expect(qs.get('cursor')).toBe('abc')
    const bare = new URLSearchParams(mailListQuery({ workspace_id: 'ws', body: false, handled: undefined }))
    expect(bare.has('body')).toBe(false)
    expect(bare.has('handled')).toBe(false)
  })
})

describe('mailService (appels JSON)', () => {
  beforeEach(() => vi.clearAllMocks())

  it('mailboxes : GET et liste vide si absente', async () => {
    vi.mocked(api.get).mockResolvedValue({})
    expect(await mailService.mailboxes('ws')).toEqual([])
    expect(api.get).toHaveBeenCalledWith('/api/veridian/mail.mailboxes.list?workspace_id=ws')
  })

  it('list : rend le curseur et les messages', async () => {
    vi.mocked(api.get).mockResolvedValue({ messages: [{ id: 'm1' }], next_cursor: 'c2' })
    const res = await mailService.list({ workspace_id: 'ws', kind: ['reply_human'] })
    expect(res).toEqual({ messages: [{ id: 'm1' }], next_cursor: 'c2' })
    expect(api.get).toHaveBeenCalledWith('/api/veridian/mail.messages.list?workspace_id=ws&kind=reply_human')
  })

  it('get, thread, contactThreads, threadSummaries : bonnes routes et bons paramètres', async () => {
    vi.mocked(api.get).mockResolvedValueOnce({ message: { id: 'm1' } })
    expect(await mailService.get('ws', 'm1')).toEqual({ id: 'm1' })
    expect(api.get).toHaveBeenLastCalledWith('/api/veridian/mail.messages.get?workspace_id=ws&id=m1')

    vi.mocked(api.get).mockResolvedValueOnce({ thread: { thread_id: 't1' } })
    await mailService.thread('ws', 't1')
    expect(api.get).toHaveBeenLastCalledWith('/api/veridian/mail.threads.get?workspace_id=ws&thread_id=t1')

    vi.mocked(api.get).mockResolvedValueOnce({ threads: [{ thread_id: 't1' }] })
    expect(await mailService.contactThreads('ws', 'marie@exemple.fr')).toHaveLength(1)
    expect(api.get).toHaveBeenLastCalledWith('/api/veridian/mail.threads.get?workspace_id=ws&contact_email=marie%40exemple.fr')

    vi.mocked(api.get).mockResolvedValueOnce({})
    expect(await mailService.threadSummaries('ws', 'marie@exemple.fr', 5)).toEqual([])
    expect(api.get).toHaveBeenLastCalledWith('/api/veridian/mail.threads.list?workspace_id=ws&contact_email=marie%40exemple.fr&limit=5')
  })

  it('update : POST avec seulement les champs fournis', async () => {
    vi.mocked(api.post).mockResolvedValue({ message: { id: 'm1' } })
    await mailService.update('ws', 'm1', { handled: true })
    expect(api.post).toHaveBeenLastCalledWith('/api/veridian/mail.messages.update', { workspace_id: 'ws', id: 'm1', handled: true })
    await mailService.update('ws', 'm1', { labels: [] })
    expect(api.post).toHaveBeenLastCalledWith('/api/veridian/mail.messages.update', { workspace_id: 'ws', id: 'm1', labels: [] })
  })

  it('reply et send : POST sur leur route', async () => {
    vi.mocked(api.post).mockResolvedValue({ status: 'queued', message_id: 'x' })
    await mailService.reply({ workspace_id: 'ws', in_reply_to_message: 'm1', text: 'Bonjour' })
    expect(api.post).toHaveBeenLastCalledWith('/api/veridian/mail.messages.reply', { workspace_id: 'ws', in_reply_to_message: 'm1', text: 'Bonjour' })
    await mailService.send({ workspace_id: 'ws', to: 'a@b.fr', text: 'x', profile_id: 'p' })
    expect(vi.mocked(api.post).mock.calls.at(-1)?.[0]).toBe('/api/veridian/mail.messages.send')
  })
})

describe('erreurs', () => {
  it('mailErrorKind : 401/402/403/404/409 et le réseau', () => {
    expect(mailErrorKind(new ApiError('x', 401))).toBe('session')
    expect(mailErrorKind(new ApiError('x', 402))).toBe('payment')
    expect(mailErrorKind(new ApiError('x', 403))).toBe('forbidden')
    expect(mailErrorKind(new ApiError('x', 404))).toBe('not_found')
    expect(mailErrorKind(new ApiError('x', 409))).toBe('refused')
    expect(mailErrorKind(new ApiError('x', 400))).toBe('invalid')
    expect(mailErrorKind(new ApiError('x', 429))).toBe('rate')
    expect(mailErrorKind(new ApiError('x', 501))).toBe('unavailable')
    expect(mailErrorKind(new ApiError('x', 500))).toBe('unknown')
    expect(mailErrorKind(new TypeError('Failed to fetch'))).toBe('network')
    expect(mailErrorKind('boom')).toBe('unknown')
  })

  it('refusalOf : lit le détail du 409, sinon le message', () => {
    const refused = new ApiError('profil en pause', 409, {
      error: 'profil en pause',
      code: 'refused',
      refusal: { reason: 'le profil est en pause', gate: 'profile_paused', retry_at: '2026-10-30T08:00:00Z' }
    })
    expect(refusalOf(refused)).toEqual({ reason: 'le profil est en pause', gate: 'profile_paused', retry_at: '2026-10-30T08:00:00Z' })
    expect(refusalOf(new ApiError('refusé', 409, { error: 'refusé' }))).toEqual({ reason: 'refusé' })
    expect(refusalOf(new ApiError('nope', 500))).toBeNull()
    expect(refusalOf(new Error('x'))).toBeNull()
  })
})

describe('téléchargements authentifiés', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    localStorage.setItem('auth_token', 'jwt-123')
  })
  afterEach(() => vi.unstubAllGlobals())

  it('raw : envoie le jeton Bearer, lit le nom et le drapeau « reconstitué »', async () => {
    const fetchMock = vi.fn().mockResolvedValue(
      new Response('From: a@b.fr\r\n\r\ncorps', {
        status: 200,
        headers: {
          'Content-Disposition': 'attachment; filename=message-m1.eml',
          'X-Veridian-Reconstructed': 'true'
        }
      })
    )
    vi.stubGlobal('fetch', fetchMock)
    const file = await mailService.raw('ws', 'm1')
    const [url, init] = fetchMock.mock.calls[0]
    expect(String(url)).toContain('/api/veridian/mail.messages.raw?workspace_id=ws&id=m1')
    expect((init.headers as Record<string, string>).Authorization).toBe('Bearer jwt-123')
    expect(file.filename).toBe('message-m1.eml')
    expect(file.reconstructed).toBe(true)
    expect(await file.blob.text()).toContain('corps')
  })

  it('attachment : nom (y compris encodé), type annoncé', async () => {
    vi.stubGlobal(
      'fetch',
      vi.fn().mockResolvedValue(
        new Response('PNG', {
          status: 200,
          headers: {
            'Content-Disposition': "attachment; filename*=UTF-8''Devis%20%C3%A9t%C3%A9.pdf",
            'X-Attachment-Content-Type': 'application/pdf'
          }
        })
      )
    )
    const file = await mailService.attachment('ws', 'm1', 2, 'repli')
    expect(file.filename).toBe('Devis été.pdf')
    expect(file.contentType).toBe('application/pdf')
    expect(file.reconstructed).toBe(false)
  })

  it('404 : ApiError 404 ; 401 : jeton retiré et retour à la connexion', async () => {
    vi.stubGlobal('fetch', vi.fn().mockResolvedValue(new Response(JSON.stringify({ error: 'introuvable' }), { status: 404 })))
    await expect(mailService.raw('ws', 'm1')).rejects.toMatchObject({ status: 404, message: 'introuvable' })
    expect(localStorage.getItem('auth_token')).toBe('jwt-123')

    vi.stubGlobal('fetch', vi.fn().mockResolvedValue(new Response(JSON.stringify({ error: 'Unauthorized' }), { status: 401 })))
    await expect(mailService.attachment('ws', 'm1', 0)).rejects.toMatchObject({ status: 401 })
    expect(localStorage.getItem('auth_token')).toBeNull()
    expect(navigate).toHaveBeenCalledWith({ to: '/console/signin' })
  })
})

describe('SseParser', () => {
  it('assemble une trame coupée en plusieurs paquets, ignore les commentaires', () => {
    const p = new SseParser()
    expect(p.push(': flux ouvert\n\nretry: 3000\nid: 7\nev')).toEqual([])
    const frames = p.push('ent: message.received\ndata: {"a":1}\n\n')
    expect(frames).toEqual([{ event: 'message.received', id: '7', data: '{"a":1}' }])
    expect(p.lastId).toBe('7')
    expect(p.retryMs).toBe(3000)
  })

  it('CRLF, data sur plusieurs lignes, deux trames dans un paquet', () => {
    const p = new SseParser()
    const frames = p.push('id: 1\r\ndata: a\r\ndata: b\r\n\r\nid: 2\r\ndata: c\r\n\r\n')
    expect(frames.map((f) => f.data)).toEqual(['a\nb', 'c'])
    // un \r en bout de paquet n'est pas pris pour une fin de ligne complète
    const q = new SseParser()
    expect(q.push('data: x\r')).toEqual([])
    expect(q.push('\n\r\n')).toEqual([{ event: '', id: '', data: 'x' }])
  })
})

describe('runMailEventStream', () => {
  afterEach(() => vi.unstubAllGlobals())
  beforeEach(() => {
    vi.clearAllMocks()
    localStorage.setItem('auth_token', 'jwt-123')
  })

  const fast = { min: 1, max: 5 }

  it('livre les événements dans l’ordre, avec le jeton Bearer', async () => {
    const ctl = new AbortController()
    const got: MailStreamEvent[] = []
    const fetchMock = vi.fn().mockImplementation(() => {
      return Promise.resolve(new Response(streamOf([': ok\n\n', sse(1), sse(2, { kind: 'bounce_hard' })], true), { status: 200 }))
    })
    vi.stubGlobal('fetch', fetchMock)
    const done = runMailEventStream({
      workspaceId: 'ws',
      signal: ctl.signal,
      backoffMs: fast,
      onEvent: (e) => {
        got.push(e)
        if (got.length === 2) ctl.abort()
      }
    })
    await done
    expect(got.map((e) => [e.id, e.summary.id, e.name])).toEqual([
      [1, 'm1', 'message.received'],
      [2, 'm2', 'message.received']
    ])
    const [url, init] = fetchMock.mock.calls[0]
    expect(String(url)).toContain('/api/veridian/mail.events?workspace_id=ws&direction=in')
    expect((init.headers as Record<string, string>).Authorization).toBe('Bearer jwt-123')
    expect((init.headers as Record<string, string>)['Last-Event-ID']).toBeUndefined()
  })

  it('reprend à Last-Event-ID après une coupure, sans rien perdre ni rejouer', async () => {
    const ctl = new AbortController()
    const got: number[] = []
    const states: string[] = []
    let call = 0
    const fetchMock = vi.fn().mockImplementation(() => {
      call += 1
      if (call === 1) return Promise.resolve(new Response(streamOf([sse(5), sse(6)]), { status: 200 })) // le flux se ferme tout seul
      if (call === 2) return Promise.reject(new TypeError('Failed to fetch'))
      return Promise.resolve(new Response(streamOf([sse(7)], true), { status: 200 }))
    })
    vi.stubGlobal('fetch', fetchMock)
    await runMailEventStream({
      workspaceId: 'ws',
      signal: ctl.signal,
      lastEventId: '4',
      backoffMs: fast,
      onState: (s) => states.push(s),
      onEvent: (e) => {
        got.push(e.id)
        if (e.id === 7) ctl.abort()
      }
    })
    expect(got).toEqual([5, 6, 7])
    const sentIds = fetchMock.mock.calls.map((c) => (c[1].headers as Record<string, string>)['Last-Event-ID'])
    expect(sentIds).toEqual(['4', '6', '6'])
    expect(states).toContain('retry')
    expect(states.at(-1)).toBe('stopped')
  })

  it('« event: reconnect » du serveur : reconnexion immédiate à partir du dernier numéro', async () => {
    const ctl = new AbortController()
    let call = 0
    const fetchMock = vi.fn().mockImplementation(() => {
      call += 1
      if (call === 1) return Promise.resolve(new Response(streamOf([sse(1), 'event: reconnect\ndata: {}\n\n'], true), { status: 200 }))
      return Promise.resolve(new Response(streamOf([sse(2)], true), { status: 200 }))
    })
    vi.stubGlobal('fetch', fetchMock)
    const got: number[] = []
    await runMailEventStream({
      workspaceId: 'ws',
      signal: ctl.signal,
      backoffMs: fast,
      onEvent: (e) => {
        got.push(e.id)
        if (e.id === 2) ctl.abort()
      }
    })
    expect(got).toEqual([1, 2])
    expect((fetchMock.mock.calls[1][1].headers as Record<string, string>)['Last-Event-ID']).toBe('1')
  })

  it('401 : arrêt définitif (pas de boucle) et retour à la connexion', async () => {
    const ctl = new AbortController()
    const fetchMock = vi.fn().mockResolvedValue(new Response(JSON.stringify({ error: 'Unauthorized' }), { status: 401 }))
    vi.stubGlobal('fetch', fetchMock)
    const states: Array<[string, number | undefined]> = []
    await runMailEventStream({ workspaceId: 'ws', signal: ctl.signal, backoffMs: fast, onEvent: vi.fn(), onState: (s, i) => states.push([s, i?.status]) })
    expect(fetchMock).toHaveBeenCalledTimes(1)
    expect(states).toEqual([['stopped', 401]])
    expect(navigate).toHaveBeenCalledWith({ to: '/console/signin' })
  })

  it('403, 404 et 501 arrêtent aussi le flux ; 500 réessaie avec un délai croissant', async () => {
    for (const status of [403, 404, 501]) {
      const fetchMock = vi.fn().mockResolvedValue(new Response('{}', { status }))
      vi.stubGlobal('fetch', fetchMock)
      const states: string[] = []
      await runMailEventStream({ workspaceId: 'ws', signal: new AbortController().signal, backoffMs: fast, onEvent: vi.fn(), onState: (s) => states.push(s) })
      expect(fetchMock).toHaveBeenCalledTimes(1)
      expect(states).toEqual(['stopped'])
    }
    const ctl = new AbortController()
    const delays: number[] = []
    let n = 0
    vi.stubGlobal(
      'fetch',
      vi.fn().mockImplementation(() => {
        n += 1
        if (n >= 4) ctl.abort()
        return Promise.resolve(new Response('{}', { status: 500 }))
      })
    )
    await runMailEventStream({ workspaceId: 'ws', signal: ctl.signal, backoffMs: { min: 1, max: 4 }, onEvent: vi.fn(), onState: (s, i) => s === 'retry' && delays.push(i?.delayMs ?? 0) })
    expect(delays).toEqual([1, 2, 4])
  })

  it("l'annulation interrompt la lecture en cours et la promesse se termine", async () => {
    const ctl = new AbortController()
    vi.stubGlobal(
      'fetch',
      vi.fn().mockImplementation((_url: string, init: RequestInit) => {
        const stream = new ReadableStream<Uint8Array>({
          start(controller) {
            init.signal?.addEventListener('abort', () => controller.error(new DOMException('aborted', 'AbortError')))
          }
        })
        return Promise.resolve(new Response(stream, { status: 200 }))
      })
    )
    const states: string[] = []
    const done = runMailEventStream({ workspaceId: 'ws', signal: ctl.signal, backoffMs: fast, onEvent: vi.fn(), onState: (s) => states.push(s) })
    await new Promise((r) => setTimeout(r, 10))
    ctl.abort()
    await done
    expect(states[0]).toBe('open')
    expect(states.at(-1)).toBe('stopped')
  })
})
