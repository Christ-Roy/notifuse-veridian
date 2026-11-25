import { ApiError, api } from './client'
import { router } from '../../router'

// Accès de boîte (IMAP et SMTP d'un client mail) : miroir du contrat serveur `mail.access.*` et `mail.export`
// (fiches 84 et 91). Ce fichier ne calcule aucun verdict : l'état « fermé », les contrôles du test de connexion
// et leurs valeurs attendues viennent du serveur.
// Le mot de passe d'un accès n'est montré qu'à la création : il ne passe ni par localStorage, ni par une URL,
// ni par un journal (console.*). Aucune adresse ni aucun mot de passe n'entre dans un message d'erreur.

export type MailboxSecurity = 'ssl' | 'starttls' | 'none'
export type MailboxVia = 'imap' | 'smtp'
export type MailboxClosedReason = 'holder_gone' | 'address_removed' | 'workspace_closed'
export type MailboxFolder = 'inbox' | 'sent' | 'rejects' | 'trash'
export const MAILBOX_FOLDERS: readonly MailboxFolder[] = ['inbox', 'sent', 'rejects', 'trash']

export interface MailboxAccess {
  id: string
  workspace_id: string
  profile_id: string
  address: string
  label: string
  can_send: boolean
  daily_cap: number
  created_by: string
  created_at: string
  last_used_at?: string
  revoked_at?: string
  disabled_at?: string
  holder_user_id?: string
  holder_email?: string
  closed?: boolean
  closed_reason?: MailboxClosedReason | string
}

export interface MailboxEndpoint {
  host: string
  port: number
  /** Tel que le serveur l'écrit (« SSL/TLS », « starttls »...) : le lire par `securityCode`. */
  security: string
}

export interface MailboxConnection {
  imap?: MailboxEndpoint
  smtp?: MailboxEndpoint
}

export interface MailboxAttempt {
  at: string
  via: MailboxVia
  address: string
  ok: boolean
  reason?: 'bad_credentials' | 'rate_limited' | 'closed' | string
}

export interface MailboxAccessList {
  accesses: MailboxAccess[]
  connection: MailboxConnection
  attempts: MailboxAttempt[]
  attempts_since: string
}

export interface MailboxCreateRequest {
  workspace_id: string
  profile_id: string
  address?: string
  label: string
  can_send: boolean
  daily_cap: number
  shared: boolean
}

/** Réponse de création : `password` n'existe que dans cette réponse, il ne sera plus jamais renvoyé. */
export interface MailboxCreateResult {
  access: MailboxAccess
  username: string
  password: string
  connection: MailboxConnection
}

export interface MailboxProfileLink {
  url: string
  expires_at: string
}

export type MailboxCheckCode =
  | 'host'
  | 'port'
  | 'security'
  | 'username'
  | 'password_empty'
  | 'door'
  | 'credentials'
  | 'access_open'
  | 'can_send'
  | 'not_suspended'
  | 'profile_active'

export interface MailboxCheck {
  code: MailboxCheckCode | string
  ok: boolean
  expected?: string | number
}

export interface MailboxTestRequest {
  workspace_id: string
  address: string
  via: MailboxVia
  host: string
  port: number
  security: MailboxSecurity
  username: string
  password: string
}

export interface MailboxTestResult {
  ok: boolean
  checks: MailboxCheck[]
}

// ---------------------------------------------------------------- règles pures

/** Le mode annoncé par le serveur (« SSL/TLS », « ssl », « starttls », « off »), dans les codes de l'écran. */
export function securityCode(security: string | undefined): MailboxSecurity {
  const s = (security ?? '').trim().toLowerCase()
  if (s === 'starttls') return 'starttls'
  if (s === 'none' || s === 'off') return 'none'
  return 'ssl'
}

export type MailboxAccessState = 'active' | 'closed' | 'disabled' | 'revoked'

/** Révoqué (définitif) > fermé (titulaire parti : revient avec lui) > désactivé (par une personne) > actif. */
export function accessState(a: Pick<MailboxAccess, 'revoked_at' | 'disabled_at' | 'closed'>): MailboxAccessState {
  if (a.revoked_at) return 'revoked'
  if (a.closed) return 'closed'
  if (a.disabled_at) return 'disabled'
  return 'active'
}

export type MailboxErrorKind =
  | 'session'
  | 'forbidden'
  | 'owner_required'
  | 'too_many'
  | 'rate_limited'
  | 'invalid'
  | 'not_found'
  | 'unavailable'
  | 'unknown'

/** Classe une erreur du contrat ({error, code, message}) ; le code prime sur l'état HTTP. */
export function mailboxErrorKind(err: unknown): MailboxErrorKind {
  if (!(err instanceof ApiError)) return 'unknown'
  const code = (err.data as { code?: unknown } | null | undefined)?.code
  if (code === 'owner_required') return 'owner_required'
  if (code === 'too_many') return 'too_many'
  if (code === 'rate_limited') return 'rate_limited'
  if (code === 'unavailable') return 'unavailable'
  switch (err.status) {
    case 401:
      return 'session'
    case 403:
      return 'forbidden'
    case 404:
      return 'not_found'
    case 429:
      return 'rate_limited'
    case 400:
      return 'invalid'
    case 502:
    case 503:
    case 504:
      return 'unavailable'
    default:
      return 'unknown'
  }
}

// ---------------------------------------------------------------- appels

const BASE = '/api/veridian'

function query(params: Record<string, string>): string {
  return new URLSearchParams(params).toString()
}

function apiOrigin(): string {
  let origin = window.location.origin
  if (origin.includes('notifusedev.com')) origin = 'https://localapi.notifuse.com:4000'
  return window.API_ENDPOINT?.trim().replace(/\/+$/, '') || origin
}

export interface MailboxDownload {
  blob: Blob
  filename: string
}

function filenameFrom(header: string | null, fallback: string): string {
  const m = header ? /filename="?([^";]+)"?/i.exec(header) : null
  return m ? m[1].trim() : fallback
}

export const mailboxAccessService = {
  list: (workspaceId: string) =>
    api.get<MailboxAccessList>(`${BASE}/mail.access.list?${query({ workspace_id: workspaceId })}`),

  create: (req: MailboxCreateRequest) => api.post<MailboxCreateResult>(`${BASE}/mail.access.create`, req),

  setDisabled: (workspaceId: string, id: string, disabled: boolean) =>
    api.post<{ access: MailboxAccess }>(`${BASE}/mail.access.setDisabled`, {
      workspace_id: workspaceId,
      id,
      disabled
    }),

  revoke: (workspaceId: string, id: string) =>
    api.post<{ access: MailboxAccess }>(`${BASE}/mail.access.revoke`, { workspace_id: workspaceId, id }),

  /** Lien public d'un profil .mobileconfig : 15 minutes, sans mot de passe. */
  profileLink: (workspaceId: string, id: string) =>
    api.post<MailboxProfileLink>(`${BASE}/mail.access.profileLink`, { workspace_id: workspaceId, id }),

  test: (req: MailboxTestRequest) => api.post<MailboxTestResult>(`${BASE}/mail.access.test`, req),

  /** Export .mbox : fetch + blob avec l'en-tête Authorization (un lien direct ne pourrait pas le porter). */
  async exportMbox(workspaceId: string, profileId: string, folder: MailboxFolder): Promise<MailboxDownload> {
    const token = localStorage.getItem('auth_token')
    const qs = query({ workspace_id: workspaceId, profile_id: profileId, folder })
    const response = await fetch(`${apiOrigin()}${BASE}/mail.export?${qs}`, {
      headers: token ? { Authorization: `Bearer ${token}` } : {}
    })
    if (!response.ok) {
      const data = await response.json().catch(() => null)
      if (response.status === 401) {
        localStorage.removeItem('auth_token')
        void router.navigate({ to: '/console/signin' })
      }
      const message =
        (typeof data?.error === 'string' && data.error) ||
        (typeof data?.message === 'string' && data.message) ||
        'An error occurred'
      throw new ApiError(message, response.status, data)
    }
    return {
      blob: await response.blob(),
      filename: filenameFrom(response.headers.get('Content-Disposition'), `${folder}.mbox`)
    }
  }
}
