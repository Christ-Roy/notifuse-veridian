import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, waitFor } from '@testing-library/react'
import { i18n } from '@lingui/core'
import { I18nProvider } from '@lingui/react'
import { App } from 'antd'
import { VeridianEngagementByClass, formatRate } from './veridian_engagement_by_class'
import { engagementByClassApi } from '../../services/api/veridian_engagement_by_class'
import type { Workspace } from '../../services/api/types'

i18n.loadAndActivate({ locale: 'en', messages: {} })

vi.mock('../../services/api/veridian_engagement_by_class', () => ({
  engagementByClassApi: { get: vi.fn() }
}))

const workspace = { id: 'ws-test', name: 'Test', settings: {} } as unknown as Workspace

const empty = { sent: 0, bounced: 0, replied_human: 0 }

const renderTable = () =>
  render(
    <I18nProvider i18n={i18n}>
      <App>
        <VeridianEngagementByClass workspace={workspace} timeRange={['2026-09-24', '2026-10-08']} />
      </App>
    </I18nProvider>
  )

describe('VeridianEngagementByClass', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    ;(engagementByClassApi.get as ReturnType<typeof vi.fn>).mockResolvedValue({
      by_class: {
        ovh: { sent: 235, bounced: 14, replied_human: 4 },
        corporate_selfhost: { sent: 233, bounced: 10, replied_human: 2 },
        security_gateway: { sent: 109, bounced: 9, replied_human: 0 },
        ionos: { sent: 70, bounced: 5, replied_human: 1 },
        google: { sent: 1, bounced: 0, replied_human: 0 },
        unclassified: { sent: 10, bounced: 0, replied_human: 0 },
        microsoft: empty
      },
      total: { sent: 658, bounced: 38, replied_human: 7 }
    })
  })

  it('shows one row per real provider class with their counts, sorted by sends', async () => {
    renderTable()
    await waitFor(() => expect(screen.getByText('OVH')).toBeInTheDocument())
    expect(screen.getByText('IONOS / 1&1')).toBeInTheDocument()
    expect(screen.getByText('Anti-spam gateway (Vade, Mailinblack, Proofpoint…)')).toBeInTheDocument()
    expect(screen.getByText('Corporate self-hosted')).toBeInTheDocument()
    expect(screen.getByText('Unclassified')).toBeInTheDocument()
    expect(screen.getByText('235')).toBeInTheDocument()
    // Classe vide masquee.
    expect(screen.queryByText('Microsoft (Outlook / Microsoft 365)')).toBeNull()
    // Tri par envois decroissants : OVH (235) avant IONOS (70).
    const cells = screen.getAllByRole('row').map((r) => r.textContent ?? '')
    const ovh = cells.findIndex((c) => c.startsWith('OVH'))
    const ionos = cells.findIndex((c) => c.startsWith('IONOS'))
    expect(ovh).toBeGreaterThan(0)
    expect(ovh).toBeLessThan(ionos)
  })

  it('keeps only Sent, Bounce and Human replies columns (no Delivered/Open/Click)', async () => {
    renderTable()
    await waitFor(() => expect(screen.getByText('OVH')).toBeInTheDocument())
    expect(screen.getByText('Provider class')).toBeInTheDocument()
    expect(screen.getByText('Sent')).toBeInTheDocument()
    expect(screen.getByText('Bounce')).toBeInTheDocument()
    expect(screen.getByText('Human replies')).toBeInTheDocument()
    expect(screen.queryByText('Delivered')).toBeNull()
    expect(screen.queryByText('Open')).toBeNull()
    expect(screen.queryByText('Click')).toBeNull()
  })

  it('formats rates (dash when nothing was sent)', () => {
    expect(formatRate(0, 0)).toBe('—')
    expect(formatRate(14, 235)).toBe('6.0%')
    expect(formatRate(0, 10)).toBe('0%')
    expect(formatRate(30, 100)).toBe('30%')
  })
})
