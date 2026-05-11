import { api } from './client'

export type VeridianMode = 'veridian-managed' | 'self-hosted'

export interface VeridianModeResponse {
  mode: VeridianMode
  signin_url: string
  hub_url?: string
}

export const veridianApi = {
  /**
   * GET /api/veridian/mode — public endpoint qui indique si Notifuse est en
   * mode "Veridian-managed" (HUB_API_SECRET set, provisioning piloté par le
   * Hub) ou "self-hosted" (instance autonome).
   *
   * Utilise par la console UI pour decider :
   *   - mode managed : cacher Create Workspace, rediriger vers /console/signin
   *   - mode self-hosted : comportement upstream normal
   */
  async getMode(): Promise<VeridianModeResponse> {
    return api.get<VeridianModeResponse>('/api/veridian/mode')
  }
}
