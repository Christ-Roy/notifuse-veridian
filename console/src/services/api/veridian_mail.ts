import { ApiError, api } from './client'
import { router } from '../../router'

// Boîte unifiée (lot L1, jalon 3). Miroir du contrat :
//   internal/veridian/mailbox/model.go (formes JSON)
//   openapi/paths/veridian-mail.yaml    (routes /api/veridian/mail.*)
// Ce fichier ne calcule rien de métier : natures, jalons du fil, raison des rebonds viennent du serveur.
// Aucun contenu de mail ni adresse n'entre dans un journal (console.*) ni dans une télémétrie.

export type MailKind = 'sent' | 'reply_human' | 'reply_auto' | 'bounce_hard' | 'bounce_soft' | 'dsn' | 'other'
export type MailDirection = 'in' | 'out'
export type MailBodySource = 'received' | 'captured' | 'rerendered' | 'unavailable'

export const MAIL_KINDS: readonly MailKind[] = [
  'sent',
  'reply_human',
  'reply_auto',
  'bounce_hard',
  'bounce_soft',
  'dsn',
  'other'
]

export interface MailAttachment {
  index: number
  name: string
  content_type: string
  size: number
  sha256?: string
  inline?: boolean
  content_id?: string
  available: boolean
}

export interface MailDSN {
  code?: string
  severity?: 'hard' | 'soft' | 'unknown' | string
  action?: string
  diagnostic?: string
  recipient?: string
  provider?: string
  reason_fr: string
  advice_fr?: string
  category?: string
  original_message_id?: string
}

export interface MailLink {
  sent_message_id?: string
  automation_id?: string
  node_id?: string
  broadcast_id?: string
  match_type?: 'message_id' | 'sender' | 'subject' | string
}

export interface MailMessage {
  id: string
  seq: number
  direction: MailDirection
  kind: MailKind
  workspace_id?: string
  profile_id?: string
  mailbox_ref?: { integration_id: string; folder: string; uid_validity: number; uid: number }
  message_id?: string
  in_reply_to?: string
  references?: string[]
  thread_id: string
  contact_email: string
  from: string
  from_name?: string
  to: string[]
  cc?: string[]
  subject: string
  sent_at?: string
  received_at?: string
  occurred_at: string
  headers?: Record<string, string[]>
  body_text?: string
  body_html_sanitized?: string
  body_source?: MailBodySource
  size_bytes?: number
  truncated?: boolean
  attachments?: MailAttachment[]
  dsn?: MailDSN
  link?: MailLink
  handled_at?: string
  handled_by?: string
  labels?: string[]
  created_at?: string
  updated_at?: string
}

export interface MailSummary {
  id: string
  seq: number
  direction: MailDirection
  kind: MailKind
  profile_id?: string
  thread_id: string
  contact_email: string
  from: string
  to: string[]
  subject: string
  preview?: string
  occurred_at: string
  sent_at?: string
  received_at?: string
  has_attachments: boolean
  attachment_count: number
  dsn_reason_fr?: string
  handled_at?: string
  handled_by?: string
  labels?: string[]
}

export interface MailMilestone {
  id: string
  label: string
  kind: MailKind
  at: string
}

export interface MailThread {
  thread_id: string
  subject: string
  contact_email: string
  first_at: string
  last_at: string
  message_count: number
  last_kind?: MailKind
  messages?: MailMessage[]
  milestones?: MailMilestone[]
}

export interface MailboxSync {
  uid_validity: number
  last_uid: number
  last_run_at?: string
  last_ok_at?: string
  last_error?: string
  total_ingested: number
}

export interface Mailbox {
  id: string
  address: string
  profile_id?: string
  profile_name?: string
  integration_id?: string
  folder?: string
  count_in: number
  count_out: number
  count_unhandled: number
  last_message_at?: string
  sync?: MailboxSync
}

export interface MailListParams {
  workspace_id: string
  mailbox?: string
  contact_email?: string
  thread_id?: string
  kind?: MailKind[]
  direction?: MailDirection
  handled?: boolean
  q?: string
  /** true : cherche aussi dans les corps (1500 derniers messages) */
  body?: boolean
  since?: string
  until?: string
  limit?: number
  cursor?: string
}

export interface MailList {
  messages: MailSummary[]
  next_cursor: string
}

export interface MailSendRequest {
  workspace_id: string
  profile_id?: string
  to?: string
  cc?: string[]
  subject?: string
  text?: string
  html?: string
  in_reply_to_message?: string
}

export interface MailSendResult {
  message_id: string
  queue_entry_id?: string
  status: string
  thread_id?: string
  note?: string
}

/** Détail d'un refus 409 (porte de la politique d'envoi). */
export interface MailRefusal {
  reason: string
  gate?: string
  detail?: string
  retry_at?: string
}

export interface MailDownload {
  blob: Blob
  filename: string
  /** Contenu reconstitué (mail envoyé) : en-tête X-Veridian-Reconstructed. */
  reconstructed: boolean
  /** Type annoncé par le serveur pour une pièce jointe (X-Attachment-Content-Type). */
  contentType: string
}

export type MailEventName = 'message.received' | 'message.sent'

export interface MailStreamEvent {
  name: MailEventName | string
  id: number
  summary: MailSummary
}

const BASE = '/api/veridian'

/** Query string sans les valeurs vides ni indéfinies. */
export function mailQuery(params: Record<string, string | number | boolean | string[] | undefined | null>): string {
  const search = new URLSearchParams()
  for (const [key, value] of Object.entries(params)) {
    if (value === undefined || value === null || value === '') continue
    if (Array.isArray(value)) {
      if (value.length > 0) search.append(key, value.join(','))
      continue
    }
    search.append(key, String(value))
  }
  return search.toString()
}

export function mailListQuery(p: MailListParams): string {
  return mailQuery({
    workspace_id: p.workspace_id,
    mailbox: p.mailbox,
    contact_email: p.contact_email,
    thread_id: p.thread_id,
    kind: p.kind,
    direction: p.direction,
    handled: p.handled,
    q: p.q?.trim(),
    body: p.body ? true : undefined,
    since: p.since,
    until: p.until,
    limit: p.limit,
    cursor: p.cursor
  })
}

// --- requêtes binaires et flux : hors du client JSON, avec le même jeton et la même politique 401 ---

function apiOrigin(): string {
  let origin = window.location.origin
  if (origin.includes('notifusedev.com')) origin = 'https://localapi.notifuse.com:4000'
  return window.API_ENDPOINT?.trim().replace(/\/+$/, '') || origin
}

function authHeaders(extra?: Record<string, string>): Record<string, string> {
  const token = localStorage.getItem('auth_token')
  return { ...(token ? { Authorization: `Bearer ${token}` } : {}), ...extra }
}

async function toApiError(response: Response): Promise<ApiError> {
  const data = await response.json().catch(() => null)
  if (response.status === 401) {
    localStorage.removeItem('auth_token')
    void router.navigate({ to: '/console/signin' })
  }
  const message =
    (typeof data?.error === 'string' && data.error) ||
    (typeof data?.message === 'string' && data.message) ||
    'An error occurred'
  return new ApiError(message, response.status, data)
}

function filenameFromDisposition(header: string | null, fallback: string): string {
  if (!header) return fallback
  const star = /filename\*=(?:UTF-8'')?([^;]+)/i.exec(header)
  if (star) {
    try {
      return decodeURIComponent(star[1].trim().replace(/^"|"$/g, ''))
    } catch {
      /* repli sur filename= */
    }
  }
  const plain = /filename="?([^";]+)"?/i.exec(header)
  return plain ? plain[1].trim() : fallback
}

async function download(path: string, fallbackName: string, signal?: AbortSignal): Promise<MailDownload> {
  const response = await fetch(`${apiOrigin()}${path}`, { headers: authHeaders(), signal })
  if (!response.ok) throw await toApiError(response)
  return {
    blob: await response.blob(),
    filename: filenameFromDisposition(response.headers.get('Content-Disposition'), fallbackName),
    reconstructed: response.headers.get('X-Veridian-Reconstructed') === 'true',
    contentType: response.headers.get('X-Attachment-Content-Type') || ''
  }
}

/** Détail du refus quand l'erreur est un 409 « refused » ; null sinon. */
export function refusalOf(err: unknown): MailRefusal | null {
  if (!(err instanceof ApiError) || err.status !== 409) return null
  const data = err.data as { refusal?: MailRefusal; error?: string } | undefined
  if (data?.refusal && typeof data.refusal.reason === 'string') return data.refusal
  return { reason: typeof data?.error === 'string' && data.error ? data.error : err.message }
}

/** Famille d'erreur, pour choisir le texte français affiché (jamais « Error »). */
export type MailErrorKind =
  | 'session'
  | 'payment'
  | 'forbidden'
  | 'not_found'
  | 'refused'
  | 'invalid'
  | 'rate'
  | 'unavailable'
  | 'network'
  | 'unknown'

export function mailErrorKind(err: unknown): MailErrorKind {
  if (err instanceof ApiError) {
    switch (err.status) {
      case 401:
        return 'session'
      case 402:
        return 'payment'
      case 403:
        return 'forbidden'
      case 404:
        return 'not_found'
      case 409:
        return 'refused'
      case 400:
        return 'invalid'
      case 429:
        return 'rate'
      case 501:
      case 502:
      case 503:
      case 504:
        return 'unavailable'
      default:
        return 'unknown'
    }
  }
  if (err instanceof TypeError || (err instanceof Error && err.name === 'AbortError')) return 'network'
  return 'unknown'
}

// --- flux d'événements (SSE par fetch : EventSource ne sait pas envoyer l'en-tête Authorization) ---

export interface SseFrame {
  event: string
  id: string
  data: string
}

/** Découpe incrémental d'un flux text/event-stream. `push` rend les trames complètes. */
export class SseParser {
  private buffer = ''
  private cur: SseFrame = { event: '', id: '', data: '' }
  private hasData = false
  /** Dernier `id:` vu : sert à la reprise (Last-Event-ID). */
  lastId = ''
  /** Dernier `retry:` annoncé par le serveur (ms), 0 si aucun. */
  retryMs = 0

  push(chunk: string): SseFrame[] {
    this.buffer += chunk
    const out: SseFrame[] = []
    let idx: number
    while ((idx = this.buffer.search(/\r\n|\n|\r/)) >= 0) {
      // un \r en bout de tampon peut précéder un \n du paquet suivant : on attend la suite
      if (this.buffer[idx] === '\r' && idx === this.buffer.length - 1) break
      const eol = this.buffer.startsWith('\r\n', idx) ? 2 : 1
      const line = this.buffer.slice(0, idx)
      this.buffer = this.buffer.slice(idx + eol)
      if (line === '') {
        if (this.hasData || this.cur.event) out.push({ ...this.cur })
        this.cur = { event: '', id: '', data: '' }
        this.hasData = false
        continue
      }
      if (line.startsWith(':')) continue
      const colon = line.indexOf(':')
      const field = colon < 0 ? line : line.slice(0, colon)
      let value = colon < 0 ? '' : line.slice(colon + 1)
      if (value.startsWith(' ')) value = value.slice(1)
      if (field === 'event') this.cur.event = value
      else if (field === 'data') {
        this.cur.data = this.hasData ? `${this.cur.data}\n${value}` : value
        this.hasData = true
      } else if (field === 'id') {
        if (!value.includes('\0')) {
          this.cur.id = value
          this.lastId = value
        }
      } else if (field === 'retry') {
        const n = Number(value)
        if (Number.isFinite(n) && n > 0) this.retryMs = n
      }
    }
    return out
  }
}

export interface MailStreamOptions {
  workspaceId: string
  /** Reprise : numéro d'ordre du dernier événement reçu (Last-Event-ID). */
  lastEventId?: string
  direction?: 'in' | 'out' | 'any'
  kinds?: MailKind[]
  signal: AbortSignal
  onEvent: (event: MailStreamEvent) => void
  /** Appelé à chaque changement : 'open' (flux ouvert), 'retry' (reconnexion prévue), 'stopped' (arrêt définitif). */
  onState?: (state: 'open' | 'retry' | 'stopped', info?: { status?: number; delayMs?: number }) => void
  /** Délais de reconnexion (ms), surchargeable en test. */
  backoffMs?: { min: number; max: number }
}

/**
 * Suit les nouveaux messages. Reconnecte tout seul (retard croissant), reprend à `Last-Event-ID`
 * (rien n'est perdu entre deux connexions), s'arrête sur annulation ou sur 401/402/403/404/501.
 * Rend une promesse qui se termine à l'arrêt.
 */
export async function runMailEventStream(opts: MailStreamOptions): Promise<void> {
  const min = opts.backoffMs?.min ?? 1000
  const max = opts.backoffMs?.max ?? 30000
  let lastId = opts.lastEventId ?? ''
  let failures = 0
  const wait = (ms: number) =>
    new Promise<void>((resolve) => {
      const timer = setTimeout(done, ms)
      function done() {
        opts.signal.removeEventListener('abort', done)
        clearTimeout(timer)
        resolve()
      }
      opts.signal.addEventListener('abort', done, { once: true })
    })

  while (!opts.signal.aborted) {
    let retryAfter = 0
    try {
      const query = mailQuery({
        workspace_id: opts.workspaceId,
        direction: opts.direction ?? 'in',
        kind: opts.kinds
      })
      const response = await fetch(`${apiOrigin()}${BASE}/mail.events?${query}`, {
        headers: authHeaders({ Accept: 'text/event-stream', ...(lastId ? { 'Last-Event-ID': lastId } : {}) }),
        signal: opts.signal
      })
      if (!response.ok) {
        const err = await toApiError(response)
        if ([401, 402, 403, 404, 400, 501].includes(err.status)) {
          opts.onState?.('stopped', { status: err.status })
          return
        }
        throw err
      }
      if (!response.body) throw new TypeError('flux indisponible')
      opts.onState?.('open')
      failures = 0
      const parser = new SseParser()
      const reader = response.body.getReader()
      const decoder = new TextDecoder()
      let reconnectAsked = false
      // l'annulation ferme la lecture même si le navigateur ne propage pas l'abandon au corps
      const stopReading = () => void reader.cancel().catch(() => undefined)
      opts.signal.addEventListener('abort', stopReading, { once: true })
      for (;;) {
        const { value, done } = await reader.read()
        if (done) break
        for (const frame of parser.push(decoder.decode(value, { stream: true }))) {
          if (frame.id) lastId = frame.id
          if (frame.event === 'reconnect') {
            reconnectAsked = true
            continue
          }
          if (!frame.data) continue
          try {
            const summary = JSON.parse(frame.data) as MailSummary
            opts.onEvent({ name: frame.event || 'message', id: Number(frame.id) || summary.seq, summary })
          } catch {
            /* trame illisible : ignorée, jamais journalisée (le corps peut porter du contenu) */
          }
        }
        if (reconnectAsked) break
      }
      opts.signal.removeEventListener('abort', stopReading)
      if (parser.retryMs) retryAfter = parser.retryMs
      if (reconnectAsked) {
        failures = 0
        continue // le serveur demande lui-même la reconnexion (toutes les 5 min)
      }
    } catch {
      if (opts.signal.aborted) break
      failures += 1
    }
    if (opts.signal.aborted) break
    const delay = Math.min(max, Math.max(min, retryAfter) * 2 ** Math.max(0, failures - 1))
    opts.onState?.('retry', { delayMs: delay })
    await wait(delay)
  }
  opts.onState?.('stopped')
}

// --- appels JSON ---

export const mailService = {
  mailboxes: async (workspaceId: string): Promise<Mailbox[]> => {
    const res = await api.get<{ mailboxes: Mailbox[] }>(`${BASE}/mail.mailboxes.list?${mailQuery({ workspace_id: workspaceId })}`)
    return res.mailboxes ?? []
  },

  list: async (params: MailListParams): Promise<MailList> => {
    const res = await api.get<{ messages: MailSummary[]; next_cursor?: string }>(`${BASE}/mail.messages.list?${mailListQuery(params)}`)
    return { messages: res.messages ?? [], next_cursor: res.next_cursor ?? '' }
  },

  get: async (workspaceId: string, id: string): Promise<MailMessage> => {
    const res = await api.get<{ message: MailMessage }>(`${BASE}/mail.messages.get?${mailQuery({ workspace_id: workspaceId, id })}`)
    return res.message
  },

  /** Un fil complet (messages dans l'ordre chronologique, jalons en français). */
  thread: async (workspaceId: string, threadId: string): Promise<MailThread> => {
    const res = await api.get<{ thread: MailThread }>(`${BASE}/mail.threads.get?${mailQuery({ workspace_id: workspaceId, thread_id: threadId })}`)
    return res.thread
  },

  /** Tous les fils d'un contact, complets (corps compris) : à réserver aux petits volumes. */
  contactThreads: async (workspaceId: string, contactEmail: string): Promise<MailThread[]> => {
    const res = await api.get<{ threads?: MailThread[] }>(
      `${BASE}/mail.threads.get?${mailQuery({ workspace_id: workspaceId, contact_email: contactEmail })}`
    )
    return res.threads ?? []
  },

  /** Résumés des fils d'un contact (sans corps), le plus récent d'abord. */
  threadSummaries: async (workspaceId: string, contactEmail: string, limit = 20): Promise<MailThread[]> => {
    const res = await api.get<{ threads?: MailThread[] }>(
      `${BASE}/mail.threads.list?${mailQuery({ workspace_id: workspaceId, contact_email: contactEmail, limit })}`
    )
    return res.threads ?? []
  },

  update: async (
    workspaceId: string,
    id: string,
    patch: { handled?: boolean; labels?: string[] }
  ): Promise<MailMessage> => {
    const res = await api.post<{ message: MailMessage }>(`${BASE}/mail.messages.update`, {
      workspace_id: workspaceId,
      id,
      ...(patch.handled !== undefined ? { handled: patch.handled } : {}),
      ...(patch.labels !== undefined ? { labels: patch.labels } : {})
    })
    return res.message
  },

  send: (body: MailSendRequest): Promise<MailSendResult> => api.post<MailSendResult>(`${BASE}/mail.messages.send`, body),

  /** Répond à un mail reçu, dans son fil. Un refus de politique arrive en 409 : voir refusalOf. */
  reply: (body: MailSendRequest & { in_reply_to_message: string }): Promise<MailSendResult> =>
    api.post<MailSendResult>(`${BASE}/mail.messages.reply`, body),

  /** Message brut RFC 822 (un mail envoyé est reconstitué : `reconstructed`). */
  raw: (workspaceId: string, id: string, signal?: AbortSignal): Promise<MailDownload> =>
    download(`${BASE}/mail.messages.raw?${mailQuery({ workspace_id: workspaceId, id })}`, `message-${id}.eml`, signal),

  attachment: (workspaceId: string, id: string, index: number, fallbackName = 'piece-jointe', signal?: AbortSignal): Promise<MailDownload> =>
    download(`${BASE}/mail.messages.attachment?${mailQuery({ workspace_id: workspaceId, id, index })}`, fallbackName, signal),

  stream: runMailEventStream
}
