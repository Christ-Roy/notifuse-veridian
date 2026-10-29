import { api } from './client'

// L3 (29/10/2026) : stock par classe de fournisseur destinataire.
// Miroir de internal/veridian/policy/stock.go (GET /api/veridian/stock.overview, droits
// contacts:read et message_history:read). Les colonnes inscrits, réserve, plafond du jour et jours
// de couverture sont celles du script stock-par-classe.py ; le reste (jamais contactés, couverture
// sur une journée entière, statut, besoin de réapprovisionnement) vient du même appel.

export type StockStatus = 'ok' | 'bas' | 'critique' | 'sans_plafond' | 'inactive'

export interface StockRow {
  class: string
  label: string
  enrolled: number
  reserve: number
  prospectable: number
  capacity_today: number
  // Prospectables / plafond du jour, arrondi au dixième ; null sans plafond
  days_of_coverage: number | null
  enrolled_unsent: number
  reserve_unsent: number
  // Contacts jamais contactés (inscrits + réserve)
  unsent: number
  // Ce que la classe envoie sur une journée d'envoi entière
  capacity_nominal: number
  days_unsent: number | null
  status: StockStatus
  // Contacts à aller chercher pour tenir la couverture visée
  restock_need: number
  reply_rate_smoothed: number
}

export interface StockTotals {
  enrolled: number
  reserve: number
  prospectable: number
  enrolled_unsent: number
  reserve_unsent: number
  unsent: number
  capacity_today: number
  capacity_nominal: number
  days_of_coverage: number | null
  days_unsent: number | null
  restock_need: number
}

export interface StockOverview {
  workspace_id: string
  generated_at: string
  target_days: number
  config_source: 'request' | 'config' | 'automations' | 'none'
  classes: StockRow[]
  totals: StockTotals
  notes: string[]
}

export const stockOverviewService = {
  get: (workspaceId: string, days?: number): Promise<StockOverview> => {
    const params = new URLSearchParams()
    params.append('workspace_id', workspaceId)
    if (days) params.append('days', String(days))
    return api.get<StockOverview>(`/api/veridian/stock.overview?${params.toString()}`)
  }
}
