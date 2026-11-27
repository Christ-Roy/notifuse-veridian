import { api } from './client'

// Clics par lien d'un envoi (reprise amont 35.0) : GET /api/veridian/liens.rapport
export interface LienClique {
  url: string
  clics: number
  cliqueurs: number
}

export interface LiensRapportResponse {
  lignes: LienClique[]
}

export const veridianLiensApi = {
  rapport: (workspaceId: string, broadcastId: string) =>
    api.get<LiensRapportResponse>(
      `/api/veridian/liens.rapport?workspace_id=${encodeURIComponent(workspaceId)}&broadcast_id=${encodeURIComponent(broadcastId)}`
    )
}
