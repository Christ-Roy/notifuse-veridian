import { describe, it, expect } from 'vitest'
import type { StockOverview, StockRow, StockStatus } from '../../services/api/veridian_stock'
import { countByStatus, coverageDays, restockOrder, sortByUrgency, statusTone } from './veridian_stock_rules'

const row = (cls: string, status: StockStatus, daysUnsent: number | null, need = 0, reply = 0.03): StockRow => ({
  class: cls, label: cls, enrolled: 0, reserve: 0, prospectable: 0, capacity_today: 0, days_of_coverage: null, enrolled_unsent: 0,
  reserve_unsent: 0, unsent: 0, capacity_nominal: 0, days_unsent: daysUnsent, status, restock_need: need, reply_rate_smoothed: reply
})

describe('stock : règles pures', () => {
  it('trie le plus urgent d’abord : critique, bas, couvert, sans capacité, inactif', () => {
    const sorted = sortByUrgency([
      row('idle', 'inactive', null), row('ok', 'ok', 30), row('nocap', 'sans_plafond', null), row('low-b', 'bas', 2.5), row('low-a', 'bas', 1.5), row('crit', 'critique', 0.2)
    ])
    expect(sorted.map((r) => r.class)).toEqual(['crit', 'low-a', 'low-b', 'ok', 'nocap', 'idle'])
  })

  it('ne modifie pas le tableau reçu', () => {
    const input = [row('b', 'ok', 5), row('a', 'critique', 0)]
    sortByUrgency(input)
    expect(input.map((r) => r.class)).toEqual(['b', 'a'])
  })

  it('départage à égalité par le nom', () => {
    expect(sortByUrgency([row('b', 'ok', 3), row('a', 'ok', 3)]).map((r) => r.class)).toEqual(['a', 'b'])
  })

  it('classe à approvisionner : les plus rentables d’abord, puis le plus gros manque', () => {
    const order = restockOrder([row('a', 'bas', 1, 10, 0.01), row('b', 'bas', 1, 30, 0.07), row('c', 'bas', 1, 50, 0.07), row('d', 'ok', 9, 0, 0.2)])
    expect(order.map((r) => r.class)).toEqual(['c', 'b', 'a'])
  })

  it('compte par statut et donne la couverture de la tuile', () => {
    expect(countByStatus([row('a', 'critique', 0), row('b', 'critique', 0), row('c', 'ok', 9)], 'critique')).toBe(2)
    expect(coverageDays(undefined)).toBeNull()
    expect(coverageDays({ totals: { days_unsent: 4.2 } } as unknown as StockOverview)).toBe(4.2)
    expect(coverageDays({ totals: { days_unsent: null } } as unknown as StockOverview)).toBeNull()
  })

  it('teinte par statut', () => {
    expect(statusTone('ok')).toBe('good')
    expect(statusTone('bas')).toBe('warn')
    expect(statusTone('critique')).toBe('bad')
    expect(statusTone('sans_plafond')).toBe('muted')
    expect(statusTone('inactive')).toBe('muted')
  })
})
