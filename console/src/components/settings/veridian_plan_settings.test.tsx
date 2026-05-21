import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, waitFor } from '@testing-library/react'
import { i18n } from '@lingui/core'
import { I18nProvider } from '@lingui/react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { VeridianPlanSettings } from './veridian_plan_settings'

i18n.loadAndActivate({ locale: 'en', messages: {} })

vi.mock('../../services/api/veridian_plan', () => ({
  veridianPlanApi: {
    getWorkspacePlan: vi.fn()
  }
}))

import { veridianPlanApi } from '../../services/api/veridian_plan'

const renderSettings = (workspaceId: string) => {
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false } }
  })
  return render(
    <QueryClientProvider client={client}>
      <I18nProvider i18n={i18n}>
        <VeridianPlanSettings workspaceId={workspaceId} />
      </I18nProvider>
    </QueryClientProvider>
  )
}

describe('VeridianPlanSettings', () => {
  beforeEach(() => {
    vi.clearAllMocks()
  })

  it('renders the Free plan label when API returns null (endpoint unavailable)', async () => {
    vi.mocked(veridianPlanApi.getWorkspacePlan).mockResolvedValue(null)
    renderSettings('ws-1')

    await waitFor(() => {
      expect(screen.getByText(/Free — unlimited access during your 15-day trial/i)).toBeInTheDocument()
    })
  })

  it('renders the Pro plan label when API returns pro', async () => {
    vi.mocked(veridianPlanApi.getWorkspacePlan).mockResolvedValue({
      tenant_id: 'ws-1',
      plan: 'pro',
      plan_source: 'stripe',
      status: 'active',
      limits: {} as never,
      generated_at: ''
    })
    renderSettings('ws-1')

    await waitFor(() => {
      expect(screen.getByText('Pro')).toBeInTheDocument()
    })
  })

  it('shows the lifetime_partner badge when applicable', async () => {
    vi.mocked(veridianPlanApi.getWorkspacePlan).mockResolvedValue({
      tenant_id: 'ws-1',
      plan: 'enterprise',
      plan_source: 'lifetime_partner',
      status: 'active',
      limits: {} as never,
      generated_at: ''
    })
    renderSettings('ws-1')

    await waitFor(() => {
      expect(screen.getByText(/Lifetime — access offered by Veridian/i)).toBeInTheDocument()
    })
  })

  it('never displays an Upgrade CTA or grid comparison', async () => {
    vi.mocked(veridianPlanApi.getWorkspacePlan).mockResolvedValue(null)
    renderSettings('ws-1')

    await waitFor(() => {
      expect(screen.queryByText(/Upgrade/i)).not.toBeInTheDocument()
      expect(screen.queryByText(/Pro vs Free/i)).not.toBeInTheDocument()
    })
  })

  it('always shows the unlimited features reassurance copy', async () => {
    vi.mocked(veridianPlanApi.getWorkspacePlan).mockResolvedValue(null)
    renderSettings('ws-1')

    await waitFor(() => {
      expect(
        screen.getByText(/All features are unlimited on every plan/i)
      ).toBeInTheDocument()
    })
  })
})
