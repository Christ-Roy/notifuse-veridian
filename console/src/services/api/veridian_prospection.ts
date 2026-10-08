import { api } from './client'

// Lot 5 (08/10/2026) : agrégats du tableau de bord de prospection.
// Miroir de internal/domain/veridian_prospection_stats.go (GET /api/veridian/prospection.stats).
// Les envois par jour et par heure, les rejets et les plaintes viennent du moteur
// analytics (schéma message_history), les plafonds et la réputation de
// emailProfiles.overview : ce fichier ne porte que ce qui n'existait nulle part.

export interface ProspectionStage {
  stage: number
  // Délai cumulé depuis l'entrée dans la séquence : J0, J+4, J+10
  label: string
  node_ids: string[]
  // Contacts à qui ce mail est parti (ils peuvent être plus loin dans la séquence)
  sent: number
  // Mail en file d'envoi, pas encore parti
  queued: number
  // Ont reçu le mail précédent et attendent l'échéance de celui-ci
  waiting: number
  // Sortis après avoir reçu ce mail
  exited_after: number
}

export interface ProspectionExits {
  replied: number
  rejected: number
  unsubscribed: number
  excluded: number
  other: number
  total: number
  before_first_mail: number
}

export interface ProspectionSequence {
  automation_id: string
  name: string
  status: string
  list_id: string
  enrolled: number
  completed: number
  failed: number
  stages: ProspectionStage[]
  exits: ProspectionExits
  replies_human: number
  replies_auto: number
  // Contacts distincts de la séquence à qui un mail est parti dans la fenêtre
  sent_contacts: number
  // Réponses humaines / contacts joints de la fenêtre ; null sans envoi
  reply_rate_human: number | null
}

export interface ProspectionSegment {
  list_id: string
  name: string
  sequence_ids: string[]
  active: number
  bounced: number
  unsubscribed: number
  complained: number
  // Stock restant : contacts actifs à qui aucun mail n'est encore parti
  never_contacted: number
  replies_human: number
  replies_auto: number
  sent_contacts: number
  reply_rate_human: number | null
}

export interface ProspectionStats {
  generated_at: string
  since: string | null
  until: string | null
  sequences: ProspectionSequence[]
  segments: ProspectionSegment[]
  totals: { stock_remaining: number; queued_first_mail: number }
}

export interface ProspectionStatsRequest {
  workspace_id: string
  // AAAA-MM-JJ, jour de fin inclus. Absents : tout l'historique.
  start?: string
  end?: string
}

export const prospectionStatsService = {
  get: (params: ProspectionStatsRequest): Promise<ProspectionStats> => {
    const searchParams = new URLSearchParams()
    searchParams.append('workspace_id', params.workspace_id)
    if (params.start) searchParams.append('start', params.start)
    if (params.end) searchParams.append('end', params.end)
    return api.get<ProspectionStats>(`/api/veridian/prospection.stats?${searchParams.toString()}`)
  }
}
