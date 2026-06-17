import { api } from './client'

// Veridian fork — client du KPI engagement PAR CLASSE de provider destinataire
// (dashboard cold). Endpoint GET /api/veridian/messages.engagementByClass (auth
// JWT console + permission contacts:read). Source de vérité backend :
//   internal/http/veridian_engagement_by_class_handler.go
//   internal/service/veridian_engagement_by_class_service.go
//   internal/repository/veridian_engagement_by_class_postgres.go
//
// La classe n'est PAS une dimension de message_history (décision Lot 4) → le
// backend agrège par DOMAINE puis mappe domaine → classe en Go. La
// classification est par SUFFIXE (pas MX) : les classes MX (ovh/ionos/…)
// tombent en `corporate` ici (dégradation gracieuse assumée, cf. CLAUDE.md).
//
// But : repérer une classe qui se dégrade (bounce rate Microsoft qui monte =
// signal d'arrêt AVANT de griller le domaine) — le tableau de bord du warm-up.

export interface VeridianClassEngagement {
  sent: number
  delivered: number
  bounced: number
  opened: number
  clicked: number
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
  // Une entrée par classe canonique (les 11 classes sont toujours présentes,
  // compteurs à 0 si vide) + le total agrégé.
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
