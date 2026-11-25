/**
 * Veridian — plan d'un espace, lu par ses membres : GET /api/veridian/me/workspace-plan (internal/veridian/planview).
 * `null` = non lu (panne, droit) : l'écran le dit, il n'invente pas un plan (règle née le 25/11/2026 : la route n'existait pas et tout le monde lisait « Free »).
 */

import { api } from './client'

export type PlanSource =
  | 'stripe'
  | 'manual'
  | 'lifetime_partner'
  | 'lifetime_site_vitrine'
  | 'internal'
  | ''

export interface WorkspacePlanResponse {
  tenant_id: string
  plan: string
  plan_source: PlanSource
  status: string
  generated_at: string
}

export const veridianPlanApi = {
  async getWorkspacePlan(workspaceId: string): Promise<WorkspacePlanResponse | null> {
    try {
      return await api.get<WorkspacePlanResponse>(
        `/api/veridian/me/workspace-plan?workspace_id=${encodeURIComponent(workspaceId)}`
      )
    } catch {
      return null
    }
  }
}
