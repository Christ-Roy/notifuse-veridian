import { describe, it, expect } from 'vitest'
import { render, screen, within } from '@testing-library/react'
import { i18n } from '@lingui/core'
import { I18nProvider } from '@lingui/react'

import { VeridianProspectionStock } from './veridian_prospection_stock'
import type { StockOverview, StockRow } from '../../services/api/veridian_stock'

i18n.loadAndActivate({ locale: 'en', messages: {} })

const base: StockRow = {
  class: 'x', label: 'X', enrolled: 0, reserve: 0, prospectable: 0, capacity_today: 0, days_of_coverage: null, enrolled_unsent: 0, reserve_unsent: 0,
  unsent: 0, capacity_nominal: 0, days_unsent: null, status: 'inactive', restock_need: 0, reply_rate_smoothed: 0.03
}

const overview = (classes: StockRow[], source: StockOverview['config_source'] = 'config'): StockOverview => ({
  workspace_id: 'ws', generated_at: '2026-10-29T10:00:00Z', target_days: 3, config_source: source, notes: [], classes,
  totals: { enrolled: 0, reserve: 0, prospectable: 0, enrolled_unsent: 0, reserve_unsent: 0, unsent: 0, capacity_today: 0, capacity_nominal: 0, days_of_coverage: null, days_unsent: null, restock_need: 0 }
})

const renderIt = (props: React.ComponentProps<typeof VeridianProspectionStock>) =>
  render(<I18nProvider i18n={i18n}><VeridianProspectionStock {...props} /></I18nProvider>)

describe('VeridianProspectionStock', () => {
  it('affiche les classes les plus urgentes en premier avec leur statut et le manque', () => {
    renderIt({ loading: false, error: false, overview: overview([
      { ...base, class: 'google', label: 'Google', status: 'ok', unsent: 230, capacity_nominal: 10, days_unsent: 23 },
      { ...base, class: 'orange', label: 'Orange', status: 'critique', unsent: 5, capacity_nominal: 10, days_unsent: 0.5, restock_need: 25, reply_rate_smoothed: 0.02 },
      { ...base, class: 'ovh', label: 'OVH', status: 'bas', unsent: 15, capacity_nominal: 10, days_unsent: 1.5, restock_need: 15, reply_rate_smoothed: 0.06 }
    ]) })
    const rows = within(screen.getByTestId('stock-by-provider')).getAllByRole('row')
    expect(rows[1]).toHaveTextContent('Orange')
    expect(rows[1]).toHaveTextContent('Critical')
    expect(rows[1]).toHaveTextContent('0.5 d')
    expect(rows[2]).toHaveTextContent('OVH')
    expect(rows[2]).toHaveTextContent('Low')
    expect(rows[3]).toHaveTextContent('Google')
    expect(rows[3]).toHaveTextContent('Covered')
    // Où chercher d'abord : OVH (6 %) avant Orange (2 %).
    expect(screen.getByText(/OVH 15, Orange 25/)).toBeInTheDocument()
  })

  it('une classe sans couverture calculable affiche un tiret, pas NaN', () => {
    renderIt({ loading: false, error: false, overview: overview([{ ...base, class: 'zoho', label: 'Zoho', status: 'sans_plafond', unsent: 4 }]) })
    const row = within(screen.getByTestId('stock-by-provider')).getAllByRole('row')[1]
    expect(row).toHaveTextContent('Zoho')
    expect(row).toHaveTextContent('No capacity')
    expect(row.textContent).not.toMatch(/NaN|undefined|Infinity/)
  })

  it('signale une lecture en échec et l’absence de définition des inscrits', () => {
    renderIt({ loading: false, error: true, overview: undefined })
    expect(screen.getByText('The stock could not be read.')).toBeInTheDocument()
    renderIt({ loading: false, error: false, overview: overview([], 'none') })
    expect(screen.getByText(/No list of enrolled contacts is defined/)).toBeInTheDocument()
  })

  it('stock vide : message, aucune ligne de données', () => {
    renderIt({ loading: false, error: false, overview: overview([]) })
    expect(screen.getByText('No contact in stock.')).toBeInTheDocument()
  })
})
