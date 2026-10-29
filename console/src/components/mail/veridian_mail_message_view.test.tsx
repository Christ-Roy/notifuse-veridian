import { describe, it, expect, vi, beforeEach } from 'vitest'
import { screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'

vi.mock('../../router', () => ({ router: { navigate: vi.fn() } }))
vi.mock('@tanstack/react-router', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@tanstack/react-router')>()
  const { fakeLink } = await import('./veridian_mail_test_utils')
  return { ...actual, Link: fakeLink, useNavigate: () => vi.fn() }
})
vi.mock('./veridian_mail_download', async (importOriginal) => {
  const actual = await importOriginal<typeof import('./veridian_mail_download')>()
  return { ...actual, saveBlob: vi.fn() }
})
vi.mock('../../services/api/veridian_mail', async (importOriginal) => {
  const actual = await importOriginal<typeof import('../../services/api/veridian_mail')>()
  return { ...actual, mailService: { ...actual.mailService, update: vi.fn(), raw: vi.fn(), attachment: vi.fn(), reply: vi.fn() } }
})

import { mailService } from '../../services/api/veridian_mail'
import { saveBlob } from './veridian_mail_download'
import { hardBounce, inboxFixture, MAILBOXES, WS } from './veridian_mail_fixtures'
import { VeridianMailMessageView } from './veridian_mail_message_view'
import { renderMail } from './veridian_mail_test_utils'

const user = userEvent.setup({ delay: null })

const view = (message = inboxFixture().reply, onChange = vi.fn(), extra: Record<string, unknown> = {}) => {
  renderMail(<VeridianMailMessageView workspaceId={WS} message={message} mailboxes={MAILBOXES} onChange={onChange} {...extra} />)
  return onChange
}

describe('fiche d’un message', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    vi.mocked(mailService.attachment).mockRejectedValue(new Error('hors ligne'))
  })

  it('en-tête lisible : De / À, date exacte et relative, boîte et profil, origine de l’envoi', () => {
    view()
    const header = screen.getByTestId('mail-header')
    expect(within(header).getByText('From')).toBeInTheDocument()
    expect(within(header).getByText('Dr Jean Martin')).toBeInTheDocument()
    expect(within(header).getByText(/jean\.martin@clinique-martin\.example/)).toBeInTheDocument()
    expect(within(header).getByText('To')).toBeInTheDocument()
    expect(within(header).getByText('Received')).toBeInTheDocument()
    expect(within(header).getByTestId('header-exact-date').textContent).toMatch(/\d{4}/)
    expect(within(header).getByText('ASD Nord')).toBeInTheDocument()
    expect(within(header).getAllByText(/contact@asd-nord\.example/).length).toBeGreaterThanOrEqual(1)
    const origin = within(header).getByRole('link', { name: 'Original send in the sending log' })
    expect(origin.getAttribute('href')).toContain('/console/workspace/ws-1/logs')
    expect(origin.getAttribute('href')).toContain('id=sent-2')
    expect(within(header).getByRole('link', { name: 'Sequence' }).getAttribute('href')).toBe('/console/workspace/ws-1/automations')
    expect(within(header).getByText('seq-cliniques')).toBeInTheDocument()
    expect(within(header).getByText('j3-relance')).toBeInTheDocument()
    expect(within(header).getByText('matched by message reference')).toBeInTheDocument()
  })

  it('en-têtes complets repliés puis lisibles', async () => {
    view()
    expect(screen.queryByTestId('raw-headers')).toBeNull()
    await user.click(screen.getByText(/Full headers \(\d+\)/))
    const table = await screen.findByTestId('raw-headers')
    expect(within(table).getByText('Message-ID')).toBeInTheDocument()
    expect(within(table).getByText('<CAF3a9x2@mail.clinique-martin.example>')).toBeInTheDocument()
  })

  it('nature, objet, corps HTML dans un iframe sandbox, pièces jointes (images incorporées exclues)', () => {
    view()
    expect(screen.getByTestId('kind-badge-reply_human')).toHaveTextContent('Human reply')
    expect(screen.getByRole('heading', { level: 2 })).toHaveTextContent('Re: Compléments articulaires pour votre clinique')
    expect(screen.getByTestId('mail-body-frame').getAttribute('sandbox')).not.toContain('allow-scripts')
    const atts = screen.getAllByTestId('attachment')
    expect(atts).toHaveLength(3)
    expect(within(atts[0]).getByText('catalogue-2026-clinique-martin.pdf')).toBeInTheDocument()
    expect(within(atts[0]).getByText(/2,2\s*Mo|2\.2\s*MB/)).toBeInTheDocument()
    expect(within(atts[0]).getByRole('button', { name: 'Download catalogue-2026-clinique-martin.pdf' })).toBeInTheDocument()
    expect(atts[0].querySelector('[data-glyph="pdf"]')).not.toBeNull()
    expect(atts[2].querySelector('[data-glyph="excel"]')).not.toBeNull()
    expect(screen.queryByText('banniere.png')).toBeNull()
  })

  it('téléchargement d’une pièce jointe : lu avec le jeton (fetch), puis enregistré', async () => {
    vi.mocked(mailService.attachment).mockResolvedValue({ blob: new Blob(['PDF']), filename: 'catalogue.pdf', reconstructed: false, contentType: 'application/pdf' })
    view()
    await user.click(screen.getByRole('button', { name: 'Download catalogue-2026-clinique-martin.pdf' }))
    await waitFor(() => expect(saveBlob).toHaveBeenCalledTimes(1))
    expect(mailService.attachment).toHaveBeenCalledWith(WS, 'm-reply', 1, 'catalogue-2026-clinique-martin.pdf')
    expect(vi.mocked(saveBlob).mock.calls[0][1]).toBe('catalogue.pdf')
  })

  it('miniature d’une image jointe : lue à la demande, affichée dans le DOM de la console (jamais SVG)', async () => {
    vi.mocked(mailService.attachment).mockImplementation(async (_w, _id, index) => {
      if (index === 2) return { blob: new Blob(['PNG'], { type: 'image/png' }), filename: 'facade.png', reconstructed: false, contentType: 'image/png' }
      throw new Error('non demandé')
    })
    const create = vi.fn(() => 'blob:miniature')
    Object.defineProperty(URL, 'createObjectURL', { value: create, configurable: true })
    Object.defineProperty(URL, 'revokeObjectURL', { value: vi.fn(), configurable: true })
    view()
    const img = await screen.findByRole('img', { name: 'Preview of facade-clinique.png' })
    expect(img).toHaveAttribute('src', 'blob:miniature')
  })

  it('marquer traité : appelle mail.messages.update et remonte le message à jour', async () => {
    const { reply } = inboxFixture()
    const updated = { ...reply, handled_at: new Date().toISOString(), handled_by: 'humain:42' }
    vi.mocked(mailService.update).mockResolvedValue(updated)
    const onChange = view(reply)
    await user.click(screen.getByRole('button', { name: /Mark as handled/ }))
    expect(mailService.update).toHaveBeenCalledWith(WS, 'm-reply', { handled: true })
    await waitFor(() => expect(onChange).toHaveBeenCalledWith(updated))
  })

  it('déjà traité : mention « traité par », et le bouton remet à traiter', async () => {
    const { other } = inboxFixture()
    vi.mocked(mailService.update).mockResolvedValue({ ...other, handled_at: undefined })
    view(other)
    expect(screen.getByTestId('handled-note')).toHaveTextContent('Handled by a person')
    await user.click(screen.getByRole('button', { name: /Mark as to handle/ }))
    expect(mailService.update).toHaveBeenCalledWith(WS, 'm-human-2', { handled: false })
  })

  it('libellés : Entrée ajoute une puce (nettoyée), la croix en retire une', async () => {
    const { reply } = inboxFixture()
    vi.mocked(mailService.update).mockResolvedValue({ ...reply, labels: ['chaud', 'catalogue', 'rappeler'] })
    const onChange = view(reply)
    const input = within(screen.getByTestId('labels-editor')).getByRole('textbox', { name: 'Add a label' })
    await user.type(input, '  rappeler {Enter}')
    await waitFor(() => expect(mailService.update).toHaveBeenCalledWith(WS, 'm-reply', { labels: ['chaud', 'catalogue', 'rappeler'] }))
    await waitFor(() => expect(onChange).toHaveBeenCalled())
    vi.mocked(mailService.update).mockClear()
    await user.click(screen.getByRole('button', { name: 'Remove the label chaud' }))
    await waitFor(() => expect(mailService.update).toHaveBeenCalledWith(WS, 'm-reply', { labels: ['catalogue'] }))
  })

  it('message brut : téléchargé par fetch authentifié, avec la mention « reconstitué » pour un mail envoyé', async () => {
    vi.mocked(mailService.raw).mockResolvedValue({ blob: new Blob(['From: x']), filename: 'message-m-first.eml', reconstructed: true, contentType: '' })
    const { first } = inboxFixture()
    view(first)
    const btn = screen.getByRole('button', { name: /Download the \.eml \(reconstructed\)/ })
    await user.click(btn)
    await waitFor(() => expect(saveBlob).toHaveBeenCalledWith(expect.any(Blob), 'message-m-first.eml'))
    expect(mailService.raw).toHaveBeenCalledWith(WS, 'm-first')
  })

  it('mail reçu : bouton .eml sans « reconstitué »', () => {
    view()
    expect(screen.getByRole('button', { name: /Download the raw message \(\.eml\)/ })).toBeInTheDocument()
  })

  it('rebond : carte DSN expliquée, pas de bouton Répondre', () => {
    view(hardBounce())
    expect(screen.getByTestId('dsn-card')).toBeInTheDocument()
    expect(screen.getByTestId('dsn-reason')).toHaveTextContent('L’adresse n’existe pas')
    expect(screen.queryByRole('button', { name: /^(send )?Reply$/ })).toBeNull()
    expect(screen.getByTestId('kind-badge-bounce_hard')).toHaveTextContent('Permanent bounce')
  })

  it('réponse humaine : bouton Répondre présent ; mail envoyé : absent', () => {
    view()
    expect(screen.getByRole('button', { name: /^(send )?Reply$/ })).toBeInTheDocument()
  })

  it('mail envoyé reconstitué : mention, et corps indisponible expliqué', () => {
    const { first } = inboxFixture()
    view({ ...first, body_source: 'rerendered' })
    expect(screen.getByTestId('rerendered-note')).toHaveTextContent('Reconstructed')
    expect(screen.queryByRole('button', { name: /^(send )?Reply$/ })).toBeNull()
  })

  it('mobile : le retour à la liste est proposé', async () => {
    const onBack = vi.fn()
    view(inboxFixture().reply, vi.fn(), { onBack })
    await user.click(screen.getByRole('button', { name: /Back to the list/ }))
    expect(onBack).toHaveBeenCalled()
  })
})
