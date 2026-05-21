/**
 * Veridian — hook useVeridianPlan.
 *
 * Wrappe `veridianPlanApi.getWorkspacePlan` dans un TanStack Query.
 * Le fetch dégrade silencieusement à `null` si l'endpoint n'est pas
 * accessible (cf. note dans veridian_plan.ts). Les composants
 * consommateurs (badge, settings → plan) gèrent le null gracieusement.
 *
 * Refetch désactivé sur window focus + cache 5 min : le plan change
 * rarement, inutile de spammer l'API.
 */

import { useQuery } from '@tanstack/react-query'
import { veridianPlanApi, type WorkspacePlanResponse } from '../services/api/veridian_plan'

export function useVeridianPlan(workspaceId: string | undefined) {
  return useQuery<WorkspacePlanResponse | null>({
    queryKey: ['veridian', 'workspace-plan', workspaceId],
    queryFn: () => (workspaceId ? veridianPlanApi.getWorkspacePlan(workspaceId) : Promise.resolve(null)),
    enabled: Boolean(workspaceId),
    staleTime: 5 * 60 * 1000,
    retry: false
  })
}
