import { beforeEach, describe, expect, it, vi } from 'vitest'
import { render, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { i18n } from '@lingui/core'
import { I18nProvider } from '@lingui/react'
import { App as AntApp } from 'antd'

import { decision, entryDetail, explainFixture } from '../components/send_queue/veridian_queue_test_fixtures'

i18n.loadAndActivate({ locale: 'en', messages: {} })

const user = userEvent.setup({ delay: null })
vi.setConfig({ testTimeout: 30000 })

vi.mock('@tanstack/react-router', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@tanstack/react-router')>()
  return { ...actual, useParams: () => ({ workspaceId: 'ws-1' }) }
})

let perms = { automations: { read: true, write: true } }
vi.mock('../contexts/AuthContext', () => ({
  useWorkspacePermissions: () => ({ permissions: perms, loading: false })
}))

vi.mock('../services/api/veridian_queue_explain', async () => {
  const actual = await vi.importActual<typeof import('../services/api/veridian_queue_explain')>(
    '../services/api/veridian_queue_explain'
  )
  return {
    ...actual,
    queueExplainService: { explain: vi.fn(), recompute: vi.fn() },
    decisionsService: { list: vi.fn() },
    queueExitService: { exitContact: vi.fn() }
  }
})

import {
  decisionsService,
  queueExitService,
  queueExplainService
} from '../services/api/veridian_queue_explain'
import { SendQueuePage } from './SendQueuePage'

const renderPage = () =>
  render(
    <I18nProvider i18n={i18n}>
      <AntApp>
        <SendQueuePage />
      </AntApp>
    </I18nProvider>
  )

const confirmPopconfirm = async () => {
  const ok = await waitFor(() => {
    const btn = document.querySelector('.ant-popconfirm .ant-btn-primary') as HTMLElement | null
    expect(btn).not.toBeNull()
    return btn!
  })
  await user.click(ok)
}

describe('SendQueuePage', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    perms = { automations: { read: true, write: true } }
    vi.mocked(queueExplainService.explain).mockImplementation(async (params) =>
      params.entry_id ? explainFixture({ groups: [], entry: entryDetail() }) : explainFixture()
    )
    vi.mocked(queueExplainService.recompute).mockResolvedValue({ recomputed: 42 })
    vi.mocked(decisionsService.list).mockResolvedValue({ decisions: [decision()], next_cursor: '', level: 'transitions' })
    vi.mocked(queueExitService.exitContact).mockResolvedValue({})
  })

  it('plein : le total, une ligne par raison traduite avec son compteur et ses dates', async () => {
    renderPage()
    const reasons = await screen.findByTestId('queue-reasons')
    const rows = within(reasons).getAllByRole('row').slice(1)
    expect(rows).toHaveLength(3)
    // trié par compteur décroissant
    expect(within(rows[0]).getByText('Sending window closed')).toBeInTheDocument()
    expect(within(rows[0]).getByText('1,200')).toBeInTheDocument()
    expect(within(rows[1]).getByText('Not examined yet')).toBeInTheDocument()
    expect(within(rows[1]).getAllByText('574').length).toBeGreaterThan(0)
    expect(within(rows[2]).getByText('Daily capacity reached')).toBeInTheDocument()
    expect(screen.getAllByText('2,074').length).toBeGreaterThan(0)
    expect(queueExplainService.explain).toHaveBeenCalledWith({ workspace_id: 'ws-1' })
  })

  it("tableau groupé : l'automation, le noeud, le profil et l'échantillon d'entrées", async () => {
    renderPage()
    await screen.findByTestId('queue-reasons')
    expect(await screen.findByText('E-commerce a devenir')).toBeInTheDocument()
    expect(screen.getAllByText('Node j0a').length).toBeGreaterThan(0)
    expect(screen.getAllByText('Relais nord').length).toBeGreaterThan(0)
    expect(screen.getByText('Warm-up cap')).toBeInTheDocument()
    expect(screen.getByRole('button', { name: 'entry-aa' })).toBeInTheDocument()
    expect(screen.getByRole('button', { name: 'entry-bb' })).toBeInTheDocument()
  })

  it('un clic sur une entrée ouvre le tiroir : état, horodatages, trace gate par gate', async () => {
    renderPage()
    await user.click(await screen.findByRole('button', { name: 'entry-aa' }))
    await waitFor(() =>
      expect(queueExplainService.explain).toHaveBeenCalledWith({ workspace_id: 'ws-1', entry_id: 'entry-aaaa1111' })
    )
    const drawer = await waitFor(() => {
      const d = document.querySelector('.ant-drawer-content') as HTMLElement | null
      expect(d).not.toBeNull()
      return d!
    })
    expect(await within(drawer).findByText('contact@example.test')).toBeInTheDocument()
    expect(within(drawer).getByText('Last decision')).toBeInTheDocument()
    expect(within(drawer).getAllByTestId('queue-candidate')).toHaveLength(2)
    expect(within(drawer).getByText('Reputation fuse')).toBeInTheDocument()
  })

  it('bandeau orphelins avec le détail par noeud, seulement si count > 0', async () => {
    vi.mocked(queueExplainService.explain).mockResolvedValue(
      explainFixture({
        orphans: {
          count: 89,
          by_node: [{ automation_id: 'auto-1', automation_name: 'E-commerce a devenir', node_id: 'j0a', count: 89 }]
        }
      })
    )
    renderPage()
    const banner = await screen.findByTestId('queue-orphans')
    expect(within(banner).getByText(/Orphan contacts: 89/)).toBeInTheDocument()
    expect(within(banner).getByText(/E-commerce a devenir, Node j0a: 89/)).toBeInTheDocument()
  })

  it('pas de bandeau orphelins quand il n y en a pas', async () => {
    renderPage()
    await screen.findByTestId('queue-reasons')
    expect(screen.queryByTestId('queue-orphans')).toBeNull()
  })

  it('vide : message clair, ni tableau ni erreur', async () => {
    vi.mocked(queueExplainService.explain).mockResolvedValue(explainFixture({ total: 0, groups: [] }))
    renderPage()
    expect(await screen.findByText('The send queue is empty')).toBeInTheDocument()
    expect(screen.queryByTestId('queue-reasons')).toBeNull()
  })

  it('erreur : message et bouton Réessayer qui relit', async () => {
    vi.mocked(queueExplainService.explain).mockRejectedValueOnce(new Error('boom'))
    renderPage()
    expect(await screen.findByText('boom')).toBeInTheDocument()
    await user.click(screen.getByRole('button', { name: 'Retry' }))
    expect(await screen.findByTestId('queue-reasons')).toBeInTheDocument()
    expect(screen.queryByText('boom')).toBeNull()
  })

  it('écriture : Recalculer est borné au groupe (automation + noeud + raison), limite 5000, avec confirmation', async () => {
    renderPage()
    await screen.findByTestId('queue-reasons')
    const rows = await screen.findAllByRole('row')
    const reasonRow = rows.find((r) => within(r).queryByText('Sending window closed') && within(r).queryByRole('button', { name: 'Recompute' }))!
    await user.click(within(reasonRow).getByRole('button', { name: 'Recompute' }))
    // la confirmation précède l'appel
    expect(queueExplainService.recompute).not.toHaveBeenCalled()
    await confirmPopconfirm()
    await waitFor(() =>
      expect(queueExplainService.recompute).toHaveBeenCalledWith({
        workspace_id: 'ws-1',
        automation_id: 'auto-1',
        node_id: 'j0a',
        reason: 'window_closed',
        profile_id: undefined,
        limit: 5000
      })
    )
    expect(await screen.findByText('Entries recomputed: 42')).toBeInTheDocument()
  })

  it('lecture seule : aucun bouton Recalculer ni Sortir ce contact', async () => {
    perms = { automations: { read: true, write: false } }
    renderPage()
    await screen.findByTestId('queue-reasons')
    expect(screen.queryByRole('button', { name: 'Recompute' })).toBeNull()
    await user.click(await screen.findByRole('button', { name: 'entry-aa' }))
    const drawer = await waitFor(() => document.querySelector('.ant-drawer-content') as HTMLElement)
    await within(drawer).findByText('contact@example.test')
    expect(within(drawer).queryByRole('button', { name: 'Remove this contact' })).toBeNull()
    expect(within(drawer).queryByRole('button', { name: 'Recompute' })).toBeNull()
  })

  it('sortir ce contact : route existante exitContact, après confirmation', async () => {
    renderPage()
    await user.click(await screen.findByRole('button', { name: 'entry-aa' }))
    const drawer = await waitFor(() => document.querySelector('.ant-drawer-content') as HTMLElement)
    await user.click(await within(drawer).findByRole('button', { name: 'Remove this contact' }))
    expect(queueExitService.exitContact).not.toHaveBeenCalled()
    await confirmPopconfirm()
    await waitFor(() =>
      expect(queueExitService.exitContact).toHaveBeenCalledWith({
        workspace_id: 'ws-1',
        automation_id: 'auto-1',
        email: 'contact@example.test'
      })
    )
  })

  it('sans droit de lecture sur les automations : accès refusé, aucun appel réseau', async () => {
    perms = { automations: { read: false, write: false } }
    renderPage()
    expect(await screen.findByText('Access denied')).toBeInTheDocument()
    expect(queueExplainService.explain).not.toHaveBeenCalled()
  })

  it('filtrer sur une raison relit le serveur avec ce filtre', async () => {
    renderPage()
    const reasons = await screen.findByTestId('queue-reasons')
    await user.click(within(reasons).getByRole('button', { name: 'Daily capacity reached' }))
    await waitFor(() =>
      expect(queueExplainService.explain).toHaveBeenLastCalledWith({ workspace_id: 'ws-1', reason: 'capacity' })
    )
  })

  it('journal des décisions : charge decisions.list avec la trace, trace dépliable', async () => {
    renderPage()
    await user.click(await screen.findByRole('tab', { name: 'Decision log' }))
    expect(await screen.findByText('contact@example.test')).toBeInTheDocument()
    expect(decisionsService.list).toHaveBeenCalledWith(
      expect.objectContaining({ workspace_id: 'ws-1', trace: true, limit: 50 })
    )
    await user.click(screen.getByRole('button', { name: /Expand row/i }))
    expect(await screen.findAllByTestId('queue-candidate')).toHaveLength(2)
  })

  it('journal des décisions : pagination par next_cursor', async () => {
    vi.mocked(decisionsService.list)
      .mockResolvedValueOnce({ decisions: [decision({ id: 'd1' })], next_cursor: 'CUR1', level: 'transitions' })
      .mockResolvedValueOnce({
        decisions: [decision({ id: 'd2', contact_email: 'second@example.test' })],
        next_cursor: '',
        level: 'transitions'
      })
    renderPage()
    await user.click(await screen.findByRole('tab', { name: 'Decision log' }))
    await user.click(await screen.findByRole('button', { name: 'Load more' }))
    expect(await screen.findByText('second@example.test')).toBeInTheDocument()
    expect(vi.mocked(decisionsService.list).mock.calls[1][0]).toMatchObject({ cursor: 'CUR1' })
    expect(screen.queryByRole('button', { name: 'Load more' })).toBeNull()
  })

  it('journal des décisions : vide', async () => {
    vi.mocked(decisionsService.list).mockResolvedValue({ decisions: [], next_cursor: '', level: 'transitions' })
    renderPage()
    await user.click(await screen.findByRole('tab', { name: 'Decision log' }))
    expect(await screen.findByText('No decision matches these filters')).toBeInTheDocument()
  })
})
