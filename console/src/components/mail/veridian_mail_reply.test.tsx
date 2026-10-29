import { describe, it, expect, vi, beforeEach } from 'vitest'
import { screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'

vi.mock('../../router', () => ({ router: { navigate: vi.fn() } }))
vi.mock('../../services/api/veridian_mail', async (importOriginal) => {
  const actual = await importOriginal<typeof import('../../services/api/veridian_mail')>()
  return { ...actual, mailService: { ...actual.mailService, reply: vi.fn() } }
})

import { ApiError } from '../../services/api/client'
import { mailService } from '../../services/api/veridian_mail'
import { inboxFixture, MAILBOXES, WS } from './veridian_mail_fixtures'
import { VeridianMailReply } from './veridian_mail_reply'
import { renderMail } from './veridian_mail_test_utils'

const user = userEvent.setup({ delay: null })
const { reply } = inboxFixture()

const open = async (onSent = vi.fn()) => {
  renderMail(<VeridianMailReply workspaceId={WS} message={reply} mailboxes={MAILBOXES} onSent={onSent} />)
  await user.click(screen.getByRole('button', { name: /Reply/ }))
  return onSent
}

describe('répondre depuis la boîte', () => {
  beforeEach(() => vi.clearAllMocks())

  it('préremplit objet « Re: … », destinataire et profil qui a reçu', async () => {
    await open()
    expect(screen.getByTestId('reply-subject')).toHaveValue('Re: Compléments articulaires pour votre clinique')
    expect(screen.getByTestId('reply-target')).toHaveTextContent('jean.martin@clinique-martin.example')
    expect(screen.getByTitle(/ASD Nord/)).toBeInTheDocument()
  })

  it('rien ne part sans confirmation explicite, puis le profil, l’objet et le texte partent avec le message d’origine', async () => {
    vi.mocked(mailService.reply).mockResolvedValue({ message_id: 'ws_x', status: 'queued', note: 'en file : part quand les plafonds le permettent', thread_id: 'th-1' })
    const onSent = await open()
    await user.type(screen.getByTestId('reply-text'), 'Bonjour Docteur, jeudi 10h vous convient ?')
    await user.click(screen.getByRole('button', { name: /Review and send/ }))
    expect(mailService.reply).not.toHaveBeenCalled()
    const dialog = await screen.findByRole('dialog')
    expect(within(dialog).getByTestId('reply-confirm-text')).toHaveTextContent('This mail will leave with the same limits as the sequences.')
    await user.click(within(dialog).getByRole('button', { name: 'Send' }))
    await waitFor(() => expect(mailService.reply).toHaveBeenCalledTimes(1))
    expect(mailService.reply).toHaveBeenCalledWith({
      workspace_id: WS,
      in_reply_to_message: 'm-reply',
      profile_id: 'prof-nord',
      subject: 'Re: Compléments articulaires pour votre clinique',
      text: 'Bonjour Docteur, jeudi 10h vous convient ?'
    })
    const queued = await screen.findByTestId('reply-queued')
    expect(queued).toHaveTextContent('Queued')
    expect(queued).toHaveTextContent('en file : part quand les plafonds le permettent')
    expect(onSent).toHaveBeenCalledTimes(1)
  })

  it('annuler la confirmation n’envoie rien', async () => {
    await open()
    await user.type(screen.getByTestId('reply-text'), 'Bonjour')
    await user.click(screen.getByRole('button', { name: /Review and send/ }))
    const dialog = await screen.findByRole('dialog')
    await user.click(within(dialog).getByRole('button', { name: 'Cancel' }))
    expect(mailService.reply).not.toHaveBeenCalled()
  })

  it('refus 409 : la raison, le détail et l’heure de reprise s’affichent, le texte est conservé', async () => {
    vi.mocked(mailService.reply).mockRejectedValue(
      new ApiError('profil en pause', 409, {
        error: 'profil en pause',
        code: 'refused',
        refusal: { reason: 'le profil d’envoi est en pause', gate: 'profile_paused', detail: 'Pause posée par Robert', retry_at: '2026-10-30T08:00:00Z' }
      })
    )
    await open()
    await user.type(screen.getByTestId('reply-text'), 'Bonjour')
    await user.click(screen.getByRole('button', { name: /Review and send/ }))
    await user.click(within(await screen.findByRole('dialog')).getByRole('button', { name: 'Send' }))
    const refused = await screen.findByTestId('reply-refused')
    expect(refused).toHaveTextContent('Sending refused: le profil d’envoi est en pause')
    expect(refused).toHaveTextContent('Pause posée par Robert')
    expect(refused).toHaveTextContent('Retry possible from')
    expect(screen.getByTestId('reply-text')).toHaveValue('Bonjour')
    expect(screen.queryByTestId('reply-queued')).toBeNull()
  })

  it('« Envoyer » reste impossible sans texte', async () => {
    await open()
    expect(screen.getByRole('button', { name: /Review and send/ })).toBeDisabled()
    await user.type(screen.getByTestId('reply-text'), '   ')
    expect(screen.getByRole('button', { name: /Review and send/ })).toBeDisabled()
  })
})
