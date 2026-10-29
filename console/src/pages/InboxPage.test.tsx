import React, { useSyncExternalStore } from 'react'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { act, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'

import { ApiError } from '../services/api/client'
import { inboxFixture, MAILBOXES, WS } from '../components/mail/veridian_mail_fixtures'
import { fakeLink, renderMail, setViewport } from '../components/mail/veridian_mail_test_utils'

const user = userEvent.setup({ delay: null })

// mini routeur : l'URL (paramètres de recherche) vit dans un store, `navigate` la modifie
const store = vi.hoisted(() => {
  let search: Record<string, unknown> = {}
  const subs = new Set<() => void>()
  return {
    get: () => search,
    set: (s: Record<string, unknown>) => {
      search = s
      subs.forEach((f) => f())
    },
    subscribe: (f: () => void) => {
      subs.add(f)
      return () => subs.delete(f)
    }
  }
})
vi.mock('@tanstack/react-router', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@tanstack/react-router')>()
  const { fakeLink: link } = await import('../components/mail/veridian_mail_test_utils')
  return {
    ...actual,
    Link: link,
    useParams: () => ({ workspaceId: 'ws-1' }),
    useSearch: () => React.useSyncExternalStore(store.subscribe, store.get),
    useNavigate: () => (opts: { search: (prev: Record<string, unknown>) => Record<string, unknown> }) => {
      const next = opts.search(store.get())
      store.set(Object.fromEntries(Object.entries(next).filter(([, v]) => v !== undefined)))
    }
  }
})
vi.mock('../router', () => ({ router: { navigate: vi.fn() } }))

let perms: { message_history: { read: boolean; write: boolean } } = { message_history: { read: true, write: true } }
vi.mock('../contexts/AuthContext', () => ({
  useWorkspacePermissions: () => ({ permissions: perms, loading: false }),
  useAuth: () => ({ workspaces: [{ id: 'ws-1', settings: { timezone: 'Europe/Paris' } }], user: { email: 'robert@exemple.fr' } })
}))

vi.mock('../services/api/veridian_mail', async (importOriginal) => {
  const actual = await importOriginal<typeof import('../services/api/veridian_mail')>()
  return {
    ...actual,
    mailService: {
      mailboxes: vi.fn(),
      list: vi.fn(),
      get: vi.fn(),
      thread: vi.fn(),
      update: vi.fn(),
      reply: vi.fn(),
      raw: vi.fn(),
      attachment: vi.fn().mockRejectedValue(new Error('hors ligne')),
      stream: vi.fn()
    }
  }
})

import { mailService, type MailStreamOptions } from '../services/api/veridian_mail'
import { InboxPage } from './InboxPage'

void fakeLink
void useSyncExternalStore

const fx = inboxFixture()
const byId = new Map(fx.messages.map((m) => [m.id, m]))

function setup(opts: { width?: number } = {}) {
  setViewport(opts.width ?? 1280)
  store.set({})
  vi.mocked(mailService.mailboxes).mockResolvedValue(MAILBOXES)
  vi.mocked(mailService.list).mockImplementation(async (p) => {
    const kinds = p.kind
    const items = fx.summaries.filter((s) => (!kinds || kinds.includes(s.kind)) && (p.handled === undefined || !!s.handled_at === p.handled))
    return { messages: items, next_cursor: '' }
  })
  vi.mocked(mailService.get).mockImplementation(async (_w, id) => {
    const m = byId.get(id)
    if (!m) throw new ApiError('introuvable', 404)
    return m
  })
  vi.mocked(mailService.thread).mockImplementation(async (_w, id) => fx.threads[id] ?? { thread_id: id, subject: 's', contact_email: 'c', first_at: '', last_at: '', message_count: 1 })
  vi.mocked(mailService.stream).mockImplementation(() => new Promise(() => undefined))
}

describe('page Boîte de réception', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    perms = { message_history: { read: true, write: true } }
    setup()
  })

  it('bureau : réponses humaines par défaut, premier message ouvert dans la fiche, fil dessous', async () => {
    renderMail(<InboxPage />)
    const rows = await screen.findAllByTestId('mail-row')
    expect(rows).toHaveLength(2) // m-reply et m-human-2 (les seules reply_human)
    expect(mailService.list).toHaveBeenCalledWith(expect.objectContaining({ workspace_id: WS, kind: ['reply_human'], limit: 30 }))
    const view = await screen.findByTestId('message-view')
    expect(within(view).getByRole('heading', { level: 2 })).toHaveTextContent('Compléments articulaires')
    expect(within(view).getByTestId('mail-body-frame').getAttribute('sandbox')).not.toContain('allow-scripts')
    expect(await screen.findAllByTestId('timeline-step')).toHaveLength(3)
    expect(screen.getByRole('tab', { name: /Conversation \(3\)/ })).toBeInTheDocument()
  })

  it('une ligne : pastille de nature, contact, objet, aperçu, date relative, coche traité', async () => {
    renderMail(<InboxPage />)
    const rows = await screen.findAllByTestId('mail-row')
    const first = rows[0]
    expect(within(first).getByText('jean.martin@clinique-martin.example')).toBeInTheDocument()
    expect(within(first).getByText('Re: Compléments articulaires pour votre clinique')).toBeInTheDocument()
    expect(within(first).getByTestId('kind-dot-reply_human')).toBeInTheDocument()
    expect(within(first).getByRole('img', { name: 'Received' })).toBeInTheDocument()
    expect(within(first).getByRole('img', { name: 'Has attachments' })).toBeInTheDocument()
    expect(within(first).getByRole('button', { name: /Mark as handled/ })).toHaveAttribute('aria-pressed', 'false')
    const second = rows[1]
    expect(within(second).getByRole('button', { name: /Mark as to handle/ })).toHaveAttribute('aria-pressed', 'true')
  })

  it('filtre de nature : « Bounces » interroge les deux natures de rebond et met l’URL à jour', async () => {
    renderMail(<InboxPage />)
    await screen.findAllByTestId('mail-row')
    await user.click(screen.getByRole('radio', { name: 'Bounces' }))
    await waitFor(() => expect(mailService.list).toHaveBeenCalledWith(expect.objectContaining({ kind: ['bounce_hard', 'bounce_soft'] })))
    expect(store.get().nature).toBe('rebonds')
    const rows = await screen.findAllByTestId('mail-row')
    expect(rows.map((r) => r.getAttribute('data-id')).sort()).toEqual(['m-bounce', 'm-soft'])
  })

  it('« To handle » demande les messages non traités', async () => {
    renderMail(<InboxPage />)
    await screen.findAllByTestId('mail-row')
    await user.click(screen.getByText('To handle'))
    await waitFor(() => expect(mailService.list).toHaveBeenCalledWith(expect.objectContaining({ handled: false })))
    expect(store.get().etat).toBe('a_traiter')
  })

  it('URL partageable : ?message=…&boite=… ouvre ce message et filtre sur cette boîte', async () => {
    store.set({ message: 'm-bounce', boite: 'prof-sud', nature: 'rebonds' })
    renderMail(<InboxPage />)
    const view = await screen.findByTestId('message-view')
    await waitFor(() => expect(within(view).getByTestId('dsn-card')).toBeInTheDocument())
    expect(mailService.get).toHaveBeenCalledWith(WS, 'm-bounce')
    expect(mailService.list).toHaveBeenCalledWith(expect.objectContaining({ mailbox: 'prof-sud', kind: ['bounce_hard', 'bounce_soft'] }))
    expect(screen.getByRole('combobox', { name: 'Mailbox' })).toBeInTheDocument()
  })

  it('un clic sur une ligne ouvre le message et le met dans l’URL', async () => {
    renderMail(<InboxPage />)
    const rows = await screen.findAllByTestId('mail-row')
    await user.click(within(rows[1]).getAllByRole('button')[0])
    expect(store.get().message).toBe('m-human-2')
    await waitFor(() => expect(mailService.get).toHaveBeenCalledWith(WS, 'm-human-2'))
  })

  it('marquer traité depuis la liste : appel API, ligne mise à jour sans rechargement', async () => {
    vi.mocked(mailService.update).mockResolvedValue({ ...fx.reply, handled_at: new Date().toISOString(), handled_by: 'humain:42' })
    renderMail(<InboxPage />)
    const rows = await screen.findAllByTestId('mail-row')
    await user.click(within(rows[0]).getByRole('button', { name: /Mark as handled/ }))
    expect(mailService.update).toHaveBeenCalledWith(WS, 'm-reply', { handled: true })
    await waitFor(() => expect(within(screen.getAllByTestId('mail-row')[0]).getByRole('button', { name: /Mark as to handle/ })).toHaveAttribute('aria-pressed', 'true'))
  })

  it('clavier : j descend, k remonte dans la liste', async () => {
    renderMail(<InboxPage />)
    await screen.findAllByTestId('mail-row')
    await user.keyboard('j')
    await waitFor(() => expect(store.get().message).toBe('m-human-2'))
    await user.keyboard('k')
    await waitFor(() => expect(store.get().message).toBe('m-reply'))
  })

  it('pagination par curseur : « Load more » envoie le curseur et ajoute la suite', async () => {
    vi.mocked(mailService.list)
      .mockResolvedValueOnce({ messages: [fx.summaries[0]], next_cursor: 'CUR2' })
      .mockResolvedValueOnce({ messages: [fx.summaries[4]], next_cursor: '' })
    renderMail(<InboxPage />)
    await screen.findAllByTestId('mail-row')
    await user.click(screen.getByRole('button', { name: 'Load more' }))
    await waitFor(() => expect(mailService.list).toHaveBeenLastCalledWith(expect.objectContaining({ cursor: 'CUR2' })))
    await waitFor(() => expect(screen.getAllByTestId('mail-row')).toHaveLength(2))
    expect(screen.queryByRole('button', { name: 'Load more' })).toBeNull()
    expect(screen.getByText('End of the list')).toBeInTheDocument()
  })

  it('liste vide avec filtres : texte utile et bouton pour les effacer', async () => {
    vi.mocked(mailService.list).mockResolvedValue({ messages: [], next_cursor: '' })
    store.set({ nature: 'autres' })
    renderMail(<InboxPage />)
    expect(await screen.findByTestId('mail-list-empty')).toHaveTextContent('No message matches these filters.')
    await user.click(screen.getByRole('button', { name: 'Clear the filters' }))
    expect(store.get().nature).toBeUndefined()
  })

  it('erreur serveur : message français et bouton Réessayer, pas de « Error »', async () => {
    vi.mocked(mailService.list).mockRejectedValue(new ApiError('boom', 503))
    renderMail(<InboxPage />)
    const err = await screen.findByTestId('mail-list-error')
    expect(err).toHaveTextContent('The inbox is temporarily unavailable')
    expect(err.textContent).not.toMatch(/^Error/)
    vi.mocked(mailService.list).mockResolvedValue({ messages: [fx.summaries[0]], next_cursor: '' })
    await user.click(within(err).getByRole('button', { name: 'Retry' }))
    expect(await screen.findAllByTestId('mail-row')).toHaveLength(1)
  })

  it('message introuvable (404) : explication et retour possible', async () => {
    store.set({ message: 'inexistant' })
    renderMail(<InboxPage />)
    expect(await screen.findByText('Message not found')).toBeInTheDocument()
  })

  it('sans droit de lecture du journal : accès refusé, aucune requête de courrier', async () => {
    perms = { message_history: { read: false, write: false } }
    renderMail(<InboxPage />)
    expect(await screen.findByText('Access denied')).toBeInTheDocument()
    expect(mailService.list).not.toHaveBeenCalled()
    expect(mailService.stream).not.toHaveBeenCalled()
  })

  it('flux : un nouveau message rafraîchit la liste ; le flux est fermé au démontage', async () => {
    let opts: MailStreamOptions | undefined
    vi.mocked(mailService.stream).mockImplementation((o) => {
      opts = o
      return new Promise(() => undefined)
    })
    const { unmount } = renderMail(<InboxPage />)
    await screen.findAllByTestId('mail-row')
    await waitFor(() => expect(opts).toBeDefined())
    expect(opts?.workspaceId).toBe(WS)
    expect(opts?.direction).toBe('any')
    const calls = vi.mocked(mailService.list).mock.calls.length
    await act(async () => {
      opts?.onEvent({ name: 'message.received', id: 999, summary: { ...fx.summaries[0], id: 'm-new', thread_id: 'th-9' } })
    })
    await waitFor(() => expect(vi.mocked(mailService.list).mock.calls.length).toBeGreaterThan(calls))
    expect(opts?.signal.aborted).toBe(false)
    unmount()
    expect(opts?.signal.aborted).toBe(true)
  })

  it('« Conversation » : panneau à part, message courant déplié, un autre étape ouvre son message', async () => {
    renderMail(<InboxPage />)
    await screen.findByTestId('message-view')
    await user.click(screen.getByRole('tab', { name: /Conversation/ }))
    expect(store.get().onglet).toBe('conversation')
    const steps = await screen.findAllByTestId('timeline-step')
    expect(steps).toHaveLength(3)
    expect(within(steps[2]).getByTestId('step-body')).toBeInTheDocument()
  })
})

describe('page Boîte de réception, mobile (390 px)', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    perms = { message_history: { read: true, write: true } }
    setup({ width: 390 })
  })

  it('la liste d’abord, puis la fiche en plein écran avec retour', async () => {
    renderMail(<InboxPage />)
    const rows = await screen.findAllByTestId('mail-row')
    expect(screen.queryByTestId('message-view')).toBeNull()
    await user.click(within(rows[0]).getAllByRole('button')[0])
    const view = await screen.findByTestId('message-view')
    expect(screen.queryByTestId('mail-list')).toBeNull()
    await user.click(within(view).getByRole('button', { name: /Back to the list/ }))
    expect(await screen.findAllByTestId('mail-row')).toHaveLength(2)
    expect(screen.queryByTestId('message-view')).toBeNull()
    expect(store.get().message).toBeUndefined()
  })
})
