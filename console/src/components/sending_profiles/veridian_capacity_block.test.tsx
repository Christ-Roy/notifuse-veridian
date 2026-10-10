import { describe, expect, it, vi } from 'vitest'
import { render, screen, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { i18n } from '@lingui/core'
import { I18nProvider } from '@lingui/react'
import { App as AntApp } from 'antd'

import { VeridianProfileCard } from './veridian_profile_card'
import { planWithCapacity, profile } from './veridian_profile_test_fixtures'

i18n.loadAndActivate({ locale: 'en', messages: {} })
vi.setConfig({ testTimeout: 30000 })

const NOW = new Date('2026-10-07T19:28:00Z')

function renderCard(cap = {}, over = {}) {
  const p = profile('a', {}, planWithCapacity(cap, over))
  render(
    <I18nProvider i18n={i18n}>
      <AntApp>
        <VeridianProfileCard profile={p} all={[p, profile('b')]} isOwner onAction={vi.fn()} now={NOW} />
      </AntApp>
    </I18nProvider>
  )
}

describe('carte: capacité du jour', () => {
  it('résumé, barre empilée aux bonnes proportions, légende', () => {
    renderCard()
    expect(screen.getByTestId('capacity-summary')).toHaveTextContent('20 sent / 120 capacity today')
    expect(screen.getByTestId('capacity-summary')).toHaveTextContent('70 usable with the contacts due today')
    const pct = (id: string) => parseFloat((screen.getByTestId(id) as HTMLElement).style.width)
    expect(pct('capacity-sent')).toBeCloseTo((20 / 120) * 100, 1)
    expect(pct('capacity-remaining')).toBeCloseTo((50 / 120) * 100, 1)
    expect(pct('capacity-unusable')).toBeCloseTo((50 / 120) * 100, 1)
    expect(screen.getByTestId('capacity-bar')).toHaveAttribute(
      'aria-label',
      '20 sent, 50 still to send, 50 unusable for lack of contacts'
    )
    expect(screen.getByTestId('capacity-legend')).toHaveTextContent('Sent 20')
    expect(screen.getByTestId('capacity-legend')).toHaveTextContent('Unusable 50')
  })

  it('la classe limitante en clair, et celle où le stock manque', () => {
    renderCard()
    expect(
      within(screen.getByTestId('limiting-factor')).getByText('Sending rates of the providers (mostly OVH)')
    ).toBeInTheDocument()
    expect(within(screen.getByTestId('stock-limit')).getByText('Not enough contacts due for Google')).toBeInTheDocument()
  })

  it('la chauffe qui borne la somme', () => {
    renderCard({ limiting: 'warmup', limiting_class: undefined })
    expect(within(screen.getByTestId('limiting-factor')).getByText('Warmup, day 3/5')).toBeInTheDocument()
  })

  it('stock non mesuré: le dit, ne prétend rien', () => {
    renderCard({ stock_known: false, unusable: 0, useful: 120, remaining_useful: 100, stock_limiting_class: undefined })
    expect(screen.getByTestId('stock-unknown')).toHaveTextContent('not measured')
    expect(screen.getByTestId('capacity-summary')).not.toHaveTextContent('usable with')
  })

  it('fenêtre fermée: la capacité d un jour d ouverture type reste lisible', () => {
    renderCard(
      { limiting: 'window_closed', window_minutes_left: 0, full_day_configured: 229 },
      { window: { configured: true, open_now: false, source: 'profile', timezone: 'UTC' } }
    )
    expect(screen.getByTestId('typical-day')).toHaveTextContent('A normal open day: 229 sends')
    expect(
      within(screen.getByTestId('limiting-factor')).getByText('Sending window closed for the rest of the day')
    ).toBeInTheDocument()
  })

  it('capacité illimitée: pas de barre, mais le nombre envoyé', () => {
    renderCard({
      configured: null,
      remaining_configured: null,
      useful: null,
      remaining_useful: null,
      unusable: null,
      limiting: 'none'
    })
    expect(screen.queryByTestId('capacity-bar')).not.toBeInTheDocument()
    expect(screen.getByTestId('capacity-summary')).toHaveTextContent('20 sent, no capacity limit')
  })

  it('détail par fournisseur dépliable', async () => {
    renderCard()
    await userEvent.click(screen.getByText('Detail by provider'))
    const table = await screen.findByTestId('capacity-detail')
    expect(within(table).getByText('OVH')).toBeInTheDocument()
    expect(within(table).getByText('Excluded')).toBeInTheDocument()
    expect(within(table).getByText('Sending rate')).toBeInTheDocument()
  })
})
