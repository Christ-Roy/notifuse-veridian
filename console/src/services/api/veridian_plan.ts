/**
 * Veridian — workspace plan API client.
 *
 * Wrappe l'endpoint backend `GET /api/tenants/{id}/limits` (HMAC, lot V37
 * `pricing-plans-implementation`) qui renvoie plan + limits + plan_source
 * pour un tenant.
 *
 * En pratique côté console UI, le user n'a pas la HMAC. Cet endpoint est
 * donc proxifié côté backend via un wrapper user-auth (ticket à dispatch
 * vers l'agent backend Notifuse). Tant que le wrapper n'existe pas, le
 * fetch retourne `null` et les composants consommateurs (badge plan_source)
 * se rendent de façon neutre.
 *
 * Convention `-1` = illimité (cf. domain.PlanLimits Go). Le pivot pricing
 * 2026-05-21 (CLAUDE.md) acte que tout est `-1` par défaut sauf
 * `FeatureWhiteLabel` qui reste Business+ uniquement.
 */

import { api, ApiError } from './client'

export type PlanSource =
  | 'stripe'
  | 'manual'
  | 'lifetime_partner'
  | 'lifetime_site_vitrine'
  | 'internal'
  | ''

export interface PlanLimits {
  MonthlyEmailQuota: number
  MaxContacts: number
  MaxSeats: number
  MaxOAuthAccounts: number
  MaxCustomDomains: number
  MaxActiveSequences: number
  FeatureABTesting: boolean
  FeatureBrandingRemoved: boolean
  FeatureWhiteLabel: boolean
  HistoryRetentionDays: number
}

export interface WorkspacePlanResponse {
  tenant_id: string
  plan: string
  plan_source: PlanSource
  status: string
  limits: PlanLimits
  generated_at: string
}

export const veridianPlanApi = {
  /**
   * Récupère plan + plan_source d'un workspace. Renvoie `null` si l'endpoint
   * n'est pas (encore) accessible — laisse l'UI dégrader gracieusement.
   *
   * Tente d'abord le wrapper user-auth `/api/veridian/me/workspace-plan`
   * (à câbler côté backend), puis fallback sur l'endpoint HMAC direct
   * `/api/tenants/{id}/limits` qui échouera en 401 côté navigateur normal
   * mais peut marcher en self-hosted ou via header propagé par le serveur.
   */
  async getWorkspacePlan(workspaceId: string): Promise<WorkspacePlanResponse | null> {
    try {
      // Wrapper user-auth (préféré) — futur endpoint backend à câbler.
      return await api.get<WorkspacePlanResponse>(
        `/api/veridian/me/workspace-plan?workspace_id=${encodeURIComponent(workspaceId)}`
      )
    } catch (err) {
      // Fallback silencieux : si l'endpoint user-auth n'existe pas (404)
      // ou si l'HMAC manque (401), on dégrade vers absence de badge.
      // Pas de log bruyant — c'est attendu tant que le wrapper n'est pas
      // déployé.
      if (err instanceof ApiError && (err.status === 404 || err.status === 401)) {
        return null
      }
      // Autres erreurs réseau : silencieux aussi pour éviter de polluer UX.
      return null
    }
  }
}
