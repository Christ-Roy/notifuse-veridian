import { describe, it, expect } from 'vitest'
import { render, screen } from '@testing-library/react'
import { i18n } from '@lingui/core'
import { I18nProvider } from '@lingui/react'
import { VeridianPlanSourceBadge } from './veridian_plan_source_badge'
import type { PlanSource } from '../../services/api/veridian_plan'

i18n.loadAndActivate({ locale: 'en', messages: {} })

const renderBadge = (planSource: PlanSource | null | undefined) =>
  render(
    <I18nProvider i18n={i18n}>
      <VeridianPlanSourceBadge planSource={planSource} />
    </I18nProvider>
  )

describe('VeridianPlanSourceBadge', () => {
  it('renders nothing for stripe plan_source', () => {
    const { container } = renderBadge('stripe')
    expect(container.firstChild).toBeNull()
  })

  it('renders nothing for empty plan_source', () => {
    const { container } = renderBadge('')
    expect(container.firstChild).toBeNull()
  })

  it('renders nothing for null', () => {
    const { container } = renderBadge(null)
    expect(container.firstChild).toBeNull()
  })

  it('renders lifetime_partner badge', () => {
    renderBadge('lifetime_partner')
    expect(screen.getByText(/Lifetime — access offered by Veridian/i)).toBeInTheDocument()
  })

  it('renders lifetime_site_vitrine badge', () => {
    renderBadge('lifetime_site_vitrine')
    expect(screen.getByText(/Lifetime — included with your Veridian website/i)).toBeInTheDocument()
  })

  it('renders internal badge', () => {
    renderBadge('internal')
    expect(screen.getByText(/Internal Veridian account/i)).toBeInTheDocument()
  })

  it('renders manual badge', () => {
    renderBadge('manual')
    expect(screen.getByText(/Manual admin plan/i)).toBeInTheDocument()
  })
})
