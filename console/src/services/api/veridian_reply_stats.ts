import { api } from './client'

// Veridian fork — client du KPI reply rate (taux de réponse cold outbound).
// Endpoint GET /api/veridian/messages.replyStats (auth JWT console + permission
// contacts:read). Source de vérité backend :
//   internal/http/veridian_reply_stats_handler.go
//   internal/service/veridian_reply_stats_service.go
//   internal/repository/veridian_contact_reply_postgres.go (CountRepliedSince)
//
// En cold outreach le taux de réponse est LE KPI #1 (conversion réelle, bien
// plus fiable qu'open/clic pollués par le MPP Apple et les gateways de sécu).
// Le signal "a répondu" vient du stop-on-reply (Lot 3, table veridian_contact_reply)
// — il vit dans une table SÉPARÉE de message_history, d'où l'endpoint dédié plutôt
// qu'une mesure du moteur analytics générique.

export interface VeridianReplyStatsRequest {
  workspace_id: string
  // Bornes ISO YYYY-MM-DD (alignées sur le dateRange [start, end] inclusif du
  // dashboard analytics). Le backend rend end inclusif (borne exclusive au
  // lendemain). Optionnelles : absentes = tout l'historique.
  start?: string
  end?: string
}

export interface VeridianReplyStatsResponse {
  // Nombre de contacts ayant répondu sur la fenêtre (1 contact = 1 ligne, donc
  // contacts UNIQUES ayant répondu). Le ratio replied/sent est calculé côté UI
  // avec le count_sent déjà chargé par le dashboard.
  replied: number
}

export const replyStatsApi = {
  get: (params: VeridianReplyStatsRequest): Promise<VeridianReplyStatsResponse> => {
    const searchParams = new URLSearchParams()
    searchParams.append('workspace_id', params.workspace_id)
    if (params.start) searchParams.append('start', params.start)
    if (params.end) searchParams.append('end', params.end)
    return api.get<VeridianReplyStatsResponse>(
      `/api/veridian/messages.replyStats?${searchParams.toString()}`
    )
  }
}
