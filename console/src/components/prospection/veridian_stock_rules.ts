// L3 : règles pures de la vue de stock. Aucune lecture réseau, aucun texte affiché (les libellés
// vivent dans les composants, via Lingui) : tout ce fichier se teste sans rendu.

import type { StockOverview, StockRow, StockStatus } from '../../services/api/veridian_stock'

// Urgence d'un statut : plus petit = plus urgent. Une classe sans plafond n'est pas une urgence de
// stock (c'est un réglage), elle passe après celles qui manquent de contacts.
const URGENCY: Record<StockStatus, number> = { critique: 0, bas: 1, ok: 2, sans_plafond: 3, inactive: 4 }

// Les classes les plus urgentes d'abord, puis la couverture croissante, puis le nom.
export function sortByUrgency(rows: StockRow[]): StockRow[] {
  return [...rows].sort((a, b) => {
    const byStatus = URGENCY[a.status] - URGENCY[b.status]
    if (byStatus !== 0) return byStatus
    const da = a.days_unsent ?? Number.POSITIVE_INFINITY
    const db = b.days_unsent ?? Number.POSITIVE_INFINITY
    if (da !== db) return da - db
    return a.class.localeCompare(b.class)
  })
}

export function countByStatus(rows: StockRow[], status: StockStatus): number {
  return rows.filter((r) => r.status === status).length
}

// Classes à approvisionner, les plus rentables d'abord (taux de réponse lissé), puis le manque.
export function restockOrder(rows: StockRow[]): StockRow[] {
  return rows
    .filter((r) => r.restock_need > 0)
    .sort((a, b) => b.reply_rate_smoothed - a.reply_rate_smoothed || b.restock_need - a.restock_need || a.class.localeCompare(b.class))
}

export type StockTone = 'good' | 'warn' | 'bad' | 'muted'

export function statusTone(status: StockStatus): StockTone {
  switch (status) {
    case 'ok':
      return 'good'
    case 'bas':
      return 'warn'
    case 'critique':
      return 'bad'
    default:
      return 'muted'
  }
}

// Valeur de la tuile : la couverture sur une journée entière, tous fournisseurs confondus.
export function coverageDays(overview?: StockOverview): number | null {
  const d = overview?.totals.days_unsent
  return d === undefined ? null : d
}
