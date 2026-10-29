import { beforeEach, describe, expect, it, vi } from 'vitest'
import { screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'

vi.mock('../../router', () => ({ router: { navigate: vi.fn() } }))
const navigate = vi.hoisted(() => vi.fn())
vi.mock('@tanstack/react-router', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@tanstack/react-router')>()
  const { fakeLink } = await import('./veridian_mail_test_utils')
  return { ...actual, Link: fakeLink, useNavigate: () => navigate }
})
vi.mock('../../services/api/veridian_mail', async (importOriginal) => {
  const actual = await importOriginal<typeof import('../../services/api/veridian_mail')>()
  return { ...actual, mailService: { ...actual.mailService, mailboxes: vi.fn(), list: vi.fn(), threadSummaries: vi.fn(), thread: vi.fn() } }
})

import { Row } from 'antd'
import { ApiError } from '../../services/api/client'
import { mailService } from '../../services/api/veridian_mail'
import { VeridianContactMail } from './veridian_contact_mail'
import { inboxFixture, MAILBOXES, WS } from './veridian_mail_fixtures'
import { VeridianInboxTile } from './veridian_inbox_tile'
import { VeridianInboxMenuLabel } from './veridian_mail_menu'
import { veridianInboxMenuItem } from './veridian_mail_menu_item'
import { renderMail } from './veridian_mail_test_utils'

const user = userEvent.setup({ delay: null })
const fx = inboxFixture()

describe('entrée de menu « Boîte de réception »', () => {
  beforeEach(() => vi.clearAllMocks())

  it('badge = somme des réponses humaines à traiter de toutes les boîtes', async () => {
    vi.mocked(mailService.mailboxes).mockResolvedValue(MAILBOXES)
    renderMail(<VeridianInboxMenuLabel workspaceId={WS} />)
    const link = screen.getByRole('link')
    await waitFor(() => expect(link).toHaveAttribute('aria-label', 'Inbox, 4 replies to handle'))
    expect(link.getAttribute('href')).toBe('/console/workspace/ws-1/inbox')
    expect(screen.getByTestId('inbox-badge')).toHaveTextContent('4')
  })

  it('aucune réponse en attente : pas de badge ; boîte absente (404) : menu intact, pas de badge', async () => {
    vi.mocked(mailService.mailboxes).mockResolvedValue([{ ...MAILBOXES[0], count_unhandled: 0 }])
    const { unmount } = renderMail(<VeridianInboxMenuLabel workspaceId={WS} />)
    await waitFor(() => expect(mailService.mailboxes).toHaveBeenCalled())
    expect(screen.queryByTestId('inbox-badge')).toBeNull()
    unmount()
    vi.mocked(mailService.mailboxes).mockRejectedValue(new ApiError('nf', 404))
    renderMail(<VeridianInboxMenuLabel workspaceId={WS} />)
    await waitFor(() => expect(mailService.mailboxes).toHaveBeenCalledTimes(2))
    expect(screen.getByRole('link')).toHaveTextContent('Inbox')
    expect(screen.queryByTestId('inbox-badge')).toBeNull()
  })

  it('l’élément de menu porte la clé de sélection de la barre latérale', () => {
    expect(veridianInboxMenuItem(WS).key).toBe('inbox')
  })
})

describe('tuile du tableau de bord', () => {
  beforeEach(() => vi.clearAllMocks())

  it('« Replies to handle : N », dernière réponse, lien vers la boîte filtrée sur « à traiter »', async () => {
    vi.mocked(mailService.mailboxes).mockResolvedValue(MAILBOXES)
    vi.mocked(mailService.list).mockResolvedValue({ messages: [fx.summaries[0]], next_cursor: '' })
    renderMail(
      <Row>
        <VeridianInboxTile workspaceId={WS} />
      </Row>
    )
    const tile = await screen.findByTestId('tile-inbox')
    expect(tile).toHaveAttribute('aria-label', 'Replies to handle: 4. Open the inbox.')
    expect(tile.getAttribute('href')).toBe('/console/workspace/ws-1/inbox?nature=reponses&etat=a_traiter')
    expect(within(tile).getByText('4')).toBeInTheDocument()
    await waitFor(() => expect(screen.getByTestId('tile-inbox-last')).toHaveTextContent('jean.martin@clinique-martin.example'))
    expect(mailService.list).toHaveBeenCalledWith(expect.objectContaining({ kind: ['reply_human'], handled: false, limit: 1 }))
  })

  it('rien à traiter : le dit', async () => {
    vi.mocked(mailService.mailboxes).mockResolvedValue([{ ...MAILBOXES[0], count_unhandled: 0 }])
    vi.mocked(mailService.list).mockResolvedValue({ messages: [], next_cursor: '' })
    renderMail(
      <Row>
        <VeridianInboxTile workspaceId={WS} />
      </Row>
    )
    await waitFor(() => expect(screen.getByTestId('tile-inbox-last')).toHaveTextContent('No reply waiting'))
  })

  it('la boîte ne répond pas : la tuile ne s’affiche pas (pas de carte cassée)', async () => {
    vi.mocked(mailService.mailboxes).mockRejectedValue(new ApiError('nf', 404))
    renderMail(
      <Row>
        <VeridianInboxTile workspaceId={WS} />
      </Row>
    )
    await waitFor(() => expect(mailService.mailboxes).toHaveBeenCalled())
    expect(screen.queryByTestId('tile-inbox')).toBeNull()
  })
})

describe('fiche contact, section « Emails »', () => {
  beforeEach(() => vi.clearAllMocks())

  it('liste les fils du contact, lien « ouvrir dans la boîte », un fil se déplie en chronologie', async () => {
    vi.mocked(mailService.threadSummaries).mockResolvedValue([
      { thread_id: 'th-1', subject: 'Compléments articulaires pour votre clinique', contact_email: 'jean.martin@clinique-martin.example', first_at: fx.first.occurred_at, last_at: fx.reply.occurred_at, message_count: 3, last_kind: 'reply_human' },
      { thread_id: 'th-2', subject: 'Autre sujet', contact_email: 'jean.martin@clinique-martin.example', first_at: fx.first.occurred_at, last_at: fx.bounce.occurred_at, message_count: 2, last_kind: 'bounce_hard' }
    ])
    vi.mocked(mailService.thread).mockResolvedValue(fx.thread)
    renderMail(<VeridianContactMail workspaceId={WS} email="jean.martin@clinique-martin.example" />)
    await screen.findByTestId('contact-mail')
    expect(mailService.threadSummaries).toHaveBeenCalledWith(WS, 'jean.martin@clinique-martin.example', 20)
    expect(screen.getByText('2 conversations')).toBeInTheDocument()
    const open = screen.getByRole('link', { name: 'Open in the inbox' })
    expect(open.getAttribute('href')).toBe('/console/workspace/ws-1/inbox?nature=toutes&q=jean.martin%40clinique-martin.example')
    expect(screen.getByTestId('kind-badge-bounce_hard')).toBeInTheDocument()
    expect(mailService.thread).not.toHaveBeenCalled()
    await user.click(screen.getByText('Compléments articulaires pour votre clinique'))
    const steps = await screen.findAllByTestId('timeline-step')
    expect(steps).toHaveLength(3)
    expect(mailService.thread).toHaveBeenCalledWith(WS, 'th-1')
    await user.click(within(steps[0]).getByRole('button', { expanded: false }))
    await user.click(within(steps[0]).getByRole('button', { name: 'Open this message' }))
    expect(navigate).toHaveBeenCalledWith(expect.objectContaining({ to: '/console/workspace/$workspaceId/inbox', search: { message: 'm-first', nature: 'toutes' } }))
  })

  it('aucun courriel : état vide ; erreur : texte utile avec Réessayer', async () => {
    vi.mocked(mailService.threadSummaries).mockResolvedValue([])
    const { unmount } = renderMail(<VeridianContactMail workspaceId={WS} email="x@y.fr" />)
    expect(await screen.findByText('No mail exchanged with this contact yet.')).toBeInTheDocument()
    unmount()
    vi.mocked(mailService.threadSummaries).mockRejectedValue(new ApiError('x', 403))
    renderMail(<VeridianContactMail workspaceId={WS} email="x@y.fr" />)
    expect(await screen.findByText('Access denied')).toBeInTheDocument()
    expect(screen.getByRole('button', { name: 'Retry' })).toBeInTheDocument()
  })
})
