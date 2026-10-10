import { api } from './client'

// LOT 1 « pourquoi ça n'envoie pas » (10/10/2026). Miroir du contrat API :
//   GET  /api/veridian/queue.explain    (automations:read)
//   GET  /api/veridian/decisions.list   (automations:read)
//   POST /api/veridian/queue.recompute  (automations:write)
//   POST /api/automations.exitContact   (route existante)
// Ce fichier ne calcule rien : la raison d'une entrée et la trace gate par gate viennent
// du worker, la console les affiche.

export type QueueGroupBy = 'automation' | 'node' | 'reason' | 'profile' | 'class'

// Codes stables, partagés worker / API / CLI / UI.
export const QUEUE_REASON_CODES = [
  'not_examined',
  'window_closed',
  'capacity',
  'class_rate',
  'reputation_stopped',
  'excluded_class',
  'profile_paused',
  'no_profile_in_pool',
  'circuit_open',
  'anchor_wait',
  'quota_denied',
  'render_failed',
  'guard_retry',
  'automation_paused',
  'send_error',
  'deferred_legacy'
] as const
export type QueueReasonCode = (typeof QUEUE_REASON_CODES)[number]

export interface QueueGroup {
  automation_id: string
  automation_name: string
  node_id: string
  reason: string
  reason_detail: string
  profile_id: string
  profile_name: string
  class: string
  count: number
  never_examined: number
  oldest_created_at: string | null
  next_attempt_min: string | null
  next_attempt_max: string | null
  sample_entry_ids: string[]
}

export interface QueueOrphans {
  count: number
  by_node: Array<{ automation_id: string; automation_name: string; node_id: string; count: number }>
}

export type GateVerdict = 'pass' | 'block' | 'slowed' | 'skipped'
export type GateName = 'excluded' | 'reputation' | 'class_rate' | 'daily_cap' | 'sender_cap' | 'window'
export type CandidateOutcome = 'selected' | 'blocked' | 'excluded' | 'paused' | 'circuit_open' | 'skipped'

export interface TraceGate {
  gate: GateName | string
  verdict: GateVerdict | string
  value?: unknown
  limit?: unknown
  name?: string
  delay_s?: number
  detail?: string
}

export interface TraceCandidate {
  profile: string
  profile_name: string
  from: string
  outcome: CandidateOutcome | string
  gates: TraceGate[]
}

export interface DecisionTrace {
  class: string
  level: 'full' | 'reduced' | string
  anchor: { profile: string; available: boolean } | null
  candidates: TraceCandidate[]
  decision: { outcome: string; reason: string; detail: string; until: string | null; delay_s: number }
}

export type DecisionOutcome = 'sent' | 'deferred' | 'failed' | 'discarded' | 'exited' | 'recomputed'

export interface QueueDecision {
  id: string
  at: string
  entry_id: string
  message_id: string
  contact_email: string
  automation_id: string
  node_id: string
  outcome: DecisionOutcome | string
  reason: string
  detail: string
  until: string | null
  profile_id: string
  profile_name: string
  sampled: boolean
  trace: DecisionTrace | null
}

export interface QueueEntryDetail {
  id: string
  status: string
  automation_id: string
  automation_name: string
  node_id: string
  contact_email: string
  integration_id: string
  profile_name: string
  class: string
  created_at: string
  attempts: number
  max_attempts: number
  next_retry_at: string | null
  reason: string
  reason_detail: string
  deferred_at: string | null
  defer_until: string | null
  defer_count: number
  first_examined_at: string | null
  last_examined_at: string | null
  last_error: string
  last_decision: QueueDecision | null
}

export interface QueueExplain {
  workspace_id: string
  generated_at: string
  total: number
  groups: QueueGroup[]
  orphans: QueueOrphans
  entry: QueueEntryDetail | null
}

export interface QueueExplainParams {
  workspace_id: string
  group_by?: QueueGroupBy[]
  automation_id?: string
  node_id?: string
  reason?: string
  profile_id?: string
  class?: string
  status?: string
  entry_id?: string
}

export interface DecisionsParams {
  workspace_id: string
  automation_id?: string
  node_id?: string
  email?: string
  entry_id?: string
  reason?: string
  outcome?: string
  since?: string
  limit?: number
  cursor?: string
  trace?: boolean
}

export interface DecisionsList {
  decisions: QueueDecision[]
  next_cursor: string
  level: string
}

export interface RecomputeRequest {
  workspace_id: string
  automation_id?: string
  node_id?: string
  reason?: string
  profile_id?: string
  entry_ids?: string[]
  // 1..5000, obligatoire côté serveur
  limit: number
}

export interface ExitContactRequest {
  workspace_id: string
  automation_id: string
  email: string
  reason?: string
}

// Plafond d'un recalcul depuis la console (le serveur refuse au-delà de 5000).
export const RECOMPUTE_LIMIT = 5000

const FILTER_KEYS = ['automation_id', 'node_id', 'reason', 'profile_id', 'class', 'status', 'entry_id'] as const

export const queueExplainService = {
  explain: (params: QueueExplainParams): Promise<QueueExplain> => {
    const search = new URLSearchParams()
    search.append('workspace_id', params.workspace_id)
    if (params.group_by && params.group_by.length > 0) search.append('group_by', params.group_by.join(','))
    for (const key of FILTER_KEYS) {
      const value = params[key]
      if (value) search.append(key, value)
    }
    return api.get<QueueExplain>(`/api/veridian/queue.explain?${search.toString()}`)
  },

  recompute: (body: RecomputeRequest): Promise<{ recomputed: number }> => {
    const payload: RecomputeRequest = { ...body, limit: Math.min(Math.max(1, body.limit), RECOMPUTE_LIMIT) }
    return api.post<{ recomputed: number }>('/api/veridian/queue.recompute', payload)
  }
}

export const decisionsService = {
  list: (params: DecisionsParams): Promise<DecisionsList> => {
    const search = new URLSearchParams()
    search.append('workspace_id', params.workspace_id)
    for (const key of ['automation_id', 'node_id', 'email', 'entry_id', 'reason', 'outcome', 'since', 'cursor'] as const) {
      const value = params[key]
      if (value) search.append(key, value)
    }
    if (params.limit) search.append('limit', String(params.limit))
    if (params.trace) search.append('trace', '1')
    return api.get<DecisionsList>(`/api/veridian/decisions.list?${search.toString()}`)
  }
}

export const queueExitService = {
  exitContact: (body: ExitContactRequest): Promise<unknown> =>
    api.post<unknown>('/api/automations.exitContact', body)
}
