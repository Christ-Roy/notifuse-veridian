/**
 * Veridian — client GET /api/veridian/hub-discovery/me.
 *
 * Best-effort lookup côté Hub via le backend Notifuse (HMAC server-side,
 * pas exposé au navigateur). Si Hub down ou disabled : hub_available=false,
 * exists=false, tenants=[]. L'UI dégrade silencieusement (cards masquées).
 *
 * Endpoint backend : internal/http/veridian_hub_discovery_handler.go.
 * JWT user obligatoire (RequireAuth) — pas d'enumération possible.
 */

import { api, ApiError } from './client'

export type CrossAppName = 'notifuse' | 'prospection' | 'analytics' | 'cms'

export interface HubDiscoveryTenant {
  app: string
  role: string
}

export interface HubDiscoveryResponse {
  hub_available: boolean
  exists: boolean
  tenants: HubDiscoveryTenant[]
}

export const veridianHubDiscoveryApi = {
  /**
   * Lookup cross-app via Hub. Renvoie `null` si endpoint indisponible (404
   * sur self-hosted sans HUB_API_SECRET, 401 si token expiré). Le composant
   * consommateur traite `null` comme "rien à afficher", sans erreur visible.
   */
  async lookupMe(): Promise<HubDiscoveryResponse | null> {
    try {
      return await api.get<HubDiscoveryResponse>('/api/veridian/hub-discovery/me')
    } catch (err) {
      if (err instanceof ApiError && (err.status === 404 || err.status === 401)) {
        return null
      }
      return null
    }
  }
}
