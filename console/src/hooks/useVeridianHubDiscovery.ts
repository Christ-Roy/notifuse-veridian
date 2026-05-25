/**
 * Hook React Query autour de GET /api/veridian/hub-discovery/me.
 *
 * Stale 5 min : la composition cross-app d'un compte change rarement
 * (provisioning manuel ou via Hub). Pas de refetch on focus pour ne pas
 * spammer le Hub à chaque retour d'onglet.
 *
 * Retry false : si Hub down → mode dégradé immédiat (cards masquées),
 * pas de spinner ni d'attente côté UI.
 */

import { useQuery } from '@tanstack/react-query'
import {
  veridianHubDiscoveryApi,
  type HubDiscoveryResponse
} from '../services/api/veridian_hub_discovery'

export function useVeridianHubDiscovery(enabled: boolean = true) {
  return useQuery<HubDiscoveryResponse | null>({
    queryKey: ['veridian', 'hub-discovery', 'me'],
    queryFn: () => veridianHubDiscoveryApi.lookupMe(),
    enabled,
    staleTime: 5 * 60 * 1000,
    refetchOnWindowFocus: false,
    retry: false
  })
}
