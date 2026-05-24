/**
 * Veridian — hook useVeridianPlan.
 *
 * Wrappe `veridianPlanApi.getWorkspacePlan` dans un TanStack Query.
 * Le fetch dégrade silencieusement à `null` si l'endpoint n'est pas
 * accessible (cf. note dans veridian_plan.ts). Les composants
 * consommateurs (badge, settings → plan) gèrent le null gracieusement.
 *
 * === Veridian patch 2026-05-24 — audit trial résidus §C/D ===
 * Avant : staleTime 5 min + refetchOnWindowFocus désactivé → après un
 * paiement Stripe (Hub → Notifuse update-plan), l'UI affichait encore
 * "Free — 15-day trial" pendant 5 min même si l'utilisateur revenait
 * sur l'onglet. Promesse Robert "le client paie = plus aucun bandeau"
 * violée pendant cette fenêtre.
 *
 * Après : staleTime 30s + refetchOnWindowFocus actif → l'utilisateur qui
 * revient sur l'onglet voit le bon plan immédiatement (refetch sync sur
 * focus), et de toute façon plus de 30s d'écart entre DB et UI. Combiné
 * avec l'invalidation du PaywallCache backend (cf. veridian_handler.go
 * handleUpdatePlan/Restore), le résidu trial post-paiement est éliminé.
 */

import { useQuery } from '@tanstack/react-query'
import { veridianPlanApi, type WorkspacePlanResponse } from '../services/api/veridian_plan'

export function useVeridianPlan(workspaceId: string | undefined) {
  return useQuery<WorkspacePlanResponse | null>({
    queryKey: ['veridian', 'workspace-plan', workspaceId],
    queryFn: () => (workspaceId ? veridianPlanApi.getWorkspacePlan(workspaceId) : Promise.resolve(null)),
    enabled: Boolean(workspaceId),
    staleTime: 30 * 1000,
    refetchOnWindowFocus: true,
    retry: false
  })
}
