import { api } from './client'

// L1b (29/10/2026) : réconciliateur. Miroir de internal/veridian/reconcile :
//   POST /api/veridian/reconcile.run      passage des contrôles (lit, n'écrit que la table des écarts)
//   GET  /api/veridian/reconcile.report   écarts ouverts, sans rien exécuter
//   POST /api/veridian/reconcile.resolve  applique, simule ou écarte la réparation proposée
// Ce fichier ne calcule rien : la cause, la preuve et l'action viennent du serveur.

export type ReconcileSeverity = 'critical' | 'warning' | 'info'
export type ReconcileCheckStatus = 'ok' | 'ecarts' | 'non_abouti'

export interface ReconcileAction {
  kind: string
  // Texte du bouton, en français, rendu par le serveur
  label: string
  // Ce que la réparation change
  impact: string
  reversible: boolean
  params: Record<string, unknown>
  api?: { method: string; path: string; body: Record<string, unknown> }
}

export interface ReconcileFinding {
  id: string
  check: string
  severity: ReconcileSeverity
  subject_ref: string
  title: string
  expected: string
  observed: string
  explanation: string
  evidence: Record<string, unknown>
  proposed_action: ReconcileAction | null
  first_seen_at: string
  last_seen_at: string
  resolved_at: string | null
  resolved_by: string
  resolution: Record<string, unknown> | null
}

export interface ReconcileCheck {
  check: string
  number: number
  title: string
  status: ReconcileCheckStatus
  count: number
  error?: string
  duration_ms: number
}

export interface ReconcileDefinition {
  key: string
  label: string
  definition: string
  source: string
}

export interface ReconcileReport {
  generated_at: string
  ran: boolean
  checks: ReconcileCheck[]
  findings: ReconcileFinding[]
  summary: { open: number; critical: number; warning: number; info: number; not_reached: number }
  definitions?: ReconcileDefinition[]
}

export type ReconcileResolveAction = 'apply' | 'dry_run' | 'dismiss'

export interface ReconcileResolveResult {
  finding_id: string
  action: ReconcileResolveAction
  applied: boolean
  result: Record<string, unknown> | null
  // Le contrôle rejoué après l'action ne retrouve plus l'écart
  verified: boolean
  remaining: ReconcileFinding | null
  message: string
}

export const reconcileService = {
  run: (workspaceId: string, checks?: string[]): Promise<ReconcileReport> =>
    api.post<ReconcileReport>('/api/veridian/reconcile.run', { workspace_id: workspaceId, checks }),

  report: (workspaceId: string, includeResolved = false): Promise<ReconcileReport> => {
    const p = new URLSearchParams({ workspace_id: workspaceId })
    if (includeResolved) p.append('include_resolved', 'true')
    return api.get<ReconcileReport>(`/api/veridian/reconcile.report?${p.toString()}`)
  },

  resolve: (
    workspaceId: string,
    findingId: string,
    action: ReconcileResolveAction,
    note?: string
  ): Promise<ReconcileResolveResult> =>
    api.post<ReconcileResolveResult>('/api/veridian/reconcile.resolve', {
      workspace_id: workspaceId,
      finding_id: findingId,
      action,
      note
    })
}
