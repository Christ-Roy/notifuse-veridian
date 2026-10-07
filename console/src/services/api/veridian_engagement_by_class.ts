import { api } from './client'

// Veridian fork — client du KPI engagement PAR CLASSE de provider destinataire
// (dashboard cold). Endpoint GET /api/veridian/messages.engagementByClass (auth
// JWT console + permission contacts:read). Source de vérité backend :
//   internal/http/veridian_engagement_by_class_handler.go
//   internal/service/veridian_engagement_by_class_service.go
//   internal/repository/veridian_engagement_by_class_postgres.go
//
// La classe est celle PERSISTÉE sur message_history.veridian_provider_class
// (posée à l'envoi, MX résolu : ovh, ionos, security_gateway…). Chaque compteur
// est borné sur SA date : envois sur sent_at, rejets sur bounced_at, réponses
// humaines sur replied_at. Pas de livraison / ouverture / clic : mails texte
// brut et le relais ne renvoie aucun accusé de livraison.

export interface VeridianClassEngagement {
  sent: number
  bounced: number
  replied_human: number
}

export interface VeridianEngagementByClassRequest {
  workspace_id: string
  // Bornes ISO YYYY-MM-DD (alignées sur le dateRange [start, end] inclusif du
  // dashboard). end est rendu inclusif côté backend (borne exclusive au
  // lendemain). Optionnelles : absentes = tout l'historique.
  start?: string
  end?: string
}

export interface VeridianEngagementByClassResponse {
  // Une entrée par classe canonique + "unclassified" (messages sans classe
  // persistée), compteurs à 0 si vide, + le total agrégé.
  by_class: Record<string, VeridianClassEngagement>
  total: VeridianClassEngagement
}

export const engagementByClassApi = {
  get: (
    params: VeridianEngagementByClassRequest
  ): Promise<VeridianEngagementByClassResponse> => {
    const searchParams = new URLSearchParams()
    searchParams.append('workspace_id', params.workspace_id)
    if (params.start) searchParams.append('start', params.start)
    if (params.end) searchParams.append('end', params.end)
    return api.get<VeridianEngagementByClassResponse>(
      `/api/veridian/messages.engagementByClass?${searchParams.toString()}`
    )
  }
}
