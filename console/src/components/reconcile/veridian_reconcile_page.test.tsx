import { beforeEach, describe, expect, it, vi } from 'vitest'
import { render, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { i18n } from '@lingui/core'
import { I18nProvider } from '@lingui/react'
import { App as AntApp } from 'antd'

import { finding, report } from './veridian_reconcile_fixtures'

i18n.loadAndActivate({ locale: 'en', messages: {} })

const user = userEvent.setup({ delay: null })
vi.setConfig({ testTimeout: 30000 })

vi.mock('@tanstack/react-router', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@tanstack/react-router')>()
  return { ...actual, useParams: () => ({ workspaceId: 'ws-1' }) }
})

let perms: Record<string, { read: boolean; write: boolean }> = {}
vi.mock('../../contexts/AuthContext', () => ({
  useWorkspacePermissions: () => ({ permissions: perms, loading: false })
}))

vi.mock('../../services/api/veridian_reconcile', () => ({
  reconcileService: { run: vi.fn(), report: vi.fn(), resolve: vi.fn() }
}))

import { reconcileService } from '../../services/api/veridian_reconcile'
import { ReconcilePage } from './veridian_reconcile_page'

const renderPage = () =>
  render(
    <I18nProvider i18n={i18n}>
      <AntApp>
        <ReconcilePage />
      </AntApp>
    </I18nProvider>
  )

const confirm = async () => {
  const ok = await waitFor(() => {
    const btn = document.querySelector('.ant-popconfirm .ant-btn-primary') as HTMLElement | null
    expect(btn).not.toBeNull()
    return btn!
  })
  await user.click(ok)
}

describe('ReconcilePage', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    perms = {
      contacts: { read: true, write: true },
      message_history: { read: true, write: false },
      automations: { read: true, write: true }
    }
    vi.mocked(reconcileService.report).mockResolvedValue(report())
  })

  it('montre la phrase, ce qui était attendu et constaté, la cause et l’action en un clic', async () => {
    renderPage()
    const card = await screen.findByTestId('reconcile-finding')
    expect(within(card).getByText('800 contacts classés « freemail_fr » au lieu de « orange »')).toBeInTheDocument()
    expect(within(card).getByText('Expected')).toBeInTheDocument()
    expect(within(card).getByText('Observed')).toBeInTheDocument()
    expect(within(card).getByText(/scindée le 22\/10/)).toBeInTheDocument()
    expect(within(card).getByTestId('reconcile-apply')).toHaveTextContent('Reclasser 800 contacts en « orange »')
    expect(card).toHaveAttribute('data-severity', 'warning')
  })

  it('lance une vérification, affiche les trois états des contrôles et ne prend pas un non abouti pour un succès', async () => {
    vi.mocked(reconcileService.run).mockResolvedValue(
      report({
        ran: true,
        findings: [],
        summary: { open: 0, critical: 0, warning: 0, info: 0, not_reached: 1 },
        checks: [
          { check: 'classes', number: 6, title: 'La classe correspond au domaine', status: 'ok', count: 0, duration_ms: 3 },
          { check: 'compteurs', number: 4, title: 'Les compteurs sont identiques', status: 'ecarts', count: 2, duration_ms: 3 },
          { check: 'envoye_livre', number: 1, title: 'Envoyé égale livré', status: 'non_abouti', count: 0, duration_ms: 1, error: 'boîte unifiée pas encore migrée' }
        ]
      })
    )
    renderPage()
    await screen.findByTestId('reconcile-finding')
    await user.click(screen.getByTestId('reconcile-run'))
    const checks = await screen.findByTestId('reconcile-checks')
    expect(within(checks).getByText('Everything matches')).toBeInTheDocument()
    expect(within(checks).getByText('Discrepancies found')).toBeInTheDocument()
    expect(within(checks).getByText('Could not conclude')).toBeInTheDocument()
    expect(screen.getByTestId('reconcile-not-reached')).toBeInTheDocument()
    expect(screen.queryByText('No discrepancy: everything matches.')).not.toBeInTheDocument()
    expect(reconcileService.run).toHaveBeenCalledWith('ws-1')
  })

  it('applique la réparation après confirmation, affiche la vérification et recharge', async () => {
    vi.mocked(reconcileService.resolve).mockResolvedValue({
      finding_id: 'f1', action: 'apply', applied: true, result: { affected: 800 }, verified: true, remaining: null,
      message: 'Réparation appliquée et vérifiée : le contrôle rejoué ne trouve plus cet écart.'
    })
    renderPage()
    await user.click(await screen.findByTestId('reconcile-apply'))
    await confirm()
    await waitFor(() => expect(reconcileService.resolve).toHaveBeenCalledWith('ws-1', 'f1', 'apply'))
    expect(await screen.findByTestId('reconcile-result')).toHaveTextContent('Réparation appliquée et vérifiée')
    expect(reconcileService.report).toHaveBeenCalledTimes(2)
  })

  it('simule sans rien recharger', async () => {
    vi.mocked(reconcileService.resolve).mockResolvedValue({
      finding_id: 'f1', action: 'dry_run', applied: false, result: { would_apply: 'reclassify_contacts' }, verified: false, remaining: null,
      message: 'Simulation : rien n’a été modifié.'
    })
    renderPage()
    await user.click(await screen.findByTestId('reconcile-dry-run'))
    expect(await screen.findByTestId('reconcile-result')).toHaveTextContent('Simulation')
    expect(reconcileService.report).toHaveBeenCalledTimes(1)
  })

  it('écarte un écart pour 7 jours après confirmation', async () => {
    vi.mocked(reconcileService.resolve).mockResolvedValue({
      finding_id: 'f1', action: 'dismiss', applied: true, result: null, verified: false, remaining: null, message: 'Écart écarté.'
    })
    renderPage()
    await user.click(await screen.findByTestId('reconcile-dismiss'))
    await confirm()
    await waitFor(() => expect(reconcileService.resolve).toHaveBeenCalledWith('ws-1', 'f1', 'dismiss'))
  })

  it('sans droit d’écriture, les boutons de réparation sont inactifs', async () => {
    perms.automations = { read: true, write: false }
    renderPage()
    expect(await screen.findByTestId('reconcile-apply')).toBeDisabled()
    expect(screen.getByTestId('reconcile-dismiss')).toBeDisabled()
  })

  it('sans droit de lecture, l’accès est refusé et rien n’est chargé', async () => {
    perms = { contacts: { read: true, write: false } }
    renderPage()
    expect(await screen.findByText('Access denied')).toBeInTheDocument()
    expect(reconcileService.report).not.toHaveBeenCalled()
  })

  it('écart sans réparation automatique : le dit clairement', async () => {
    vi.mocked(reconcileService.report).mockResolvedValue(
      report({ findings: [finding({ id: 'f2', proposed_action: null, severity: 'critical' })] })
    )
    renderPage()
    const card = await screen.findByTestId('reconcile-finding')
    expect(card).toHaveAttribute('data-severity', 'critical')
    expect(within(card).getByText(/No automatic repair/)).toBeInTheDocument()
    expect(within(card).queryByTestId('reconcile-apply')).not.toBeInTheDocument()
  })

  it('aucun écart : message d’invitation à relancer', async () => {
    vi.mocked(reconcileService.report).mockResolvedValue(report({ findings: [], summary: { open: 0, critical: 0, warning: 0, info: 0, not_reached: 0 } }))
    renderPage()
    expect(await screen.findByText(/No open discrepancy/)).toBeInTheDocument()
  })
})
