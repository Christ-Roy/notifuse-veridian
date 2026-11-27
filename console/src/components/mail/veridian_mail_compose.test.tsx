import { describe, it, expect, vi, beforeEach } from 'vitest'
import { screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'

vi.mock('../../router', () => ({ router: { navigate: vi.fn() } }))
vi.mock('../../services/api/veridian_mail', async (importOriginal) => {
  const actual = await importOriginal<typeof import('../../services/api/veridian_mail')>()
  return { ...actual, mailService: { ...actual.mailService, send: vi.fn() } }
})

import { ApiError } from '../../services/api/client'
import { mailService } from '../../services/api/veridian_mail'
import { MAILBOXES, WS } from './veridian_mail_fixtures'
import { VeridianMailCompose } from './veridian_mail_compose'
import { isRecipient, normalizeInboxSearch } from './veridian_mail_rules'
import { renderMail } from './veridian_mail_test_utils'

const user = userEvent.setup({ delay: null })
const one = MAILBOXES.filter((m) => m.profile_id).slice(0, 1)
const open = (to = 'ana@client.example') => {
  const onClose = vi.fn()
  renderMail(<VeridianMailCompose workspaceId={WS} to={to} mailboxes={one} onClose={onClose} />)
  return onClose
}
const fill = async () => {
  await user.type(screen.getByTestId('compose-subject'), 'Bonjour')
  await user.type(screen.getByTestId('compose-text'), 'Un mot.')
  await user.click(screen.getByRole('button', { name: /Review and send/ }))
  return screen.findByText('Send this message?').then(() => screen.getAllByRole('dialog').at(-1) as HTMLElement)
}

describe('nouveau message', () => {
  beforeEach(() => vi.clearAllMocks())

  it('propose le destinataire ; rien ne part sans texte ni sans confirmation', async () => {
    vi.mocked(mailService.send).mockResolvedValue({ message_id: 'x', status: 'sent', note: 'envoyé' })
    open()
    expect(screen.getByTestId('compose-to')).toHaveValue('ana@client.example')
    expect(screen.getByRole('button', { name: /Review and send/ })).toBeDisabled()
    const dialog = await fill()
    expect(mailService.send).not.toHaveBeenCalled()
    await user.click(within(dialog).getByRole('button', { name: 'Send' }))
    await waitFor(() => expect(mailService.send).toHaveBeenCalledTimes(1))
    expect(mailService.send).toHaveBeenCalledWith({ workspace_id: WS, profile_id: one[0].profile_id, to: 'ana@client.example', subject: 'Bonjour', text: 'Un mot.' })
    const outcome = await screen.findByTestId('compose-outcome')
    expect(outcome).toHaveAttribute('data-outcome', 'sent')
    expect(outcome).toHaveTextContent('Sent')
    expect(outcome).not.toHaveTextContent(/queue|limits/i)
  })

  it('le relais refuse : la raison du relais est affichée, pas « en file »', async () => {
    vi.mocked(mailService.send).mockResolvedValue({ message_id: 'x', status: 'failed', error: '550 5.1.1 user unknown' })
    open()
    const dialog = await fill()
    expect(within(dialog).getByText('This mail leaves right away, like from any mailbox.')).toBeInTheDocument()
    await user.click(within(dialog).getByRole('button', { name: 'Send' }))
    const outcome = await screen.findByTestId('compose-outcome')
    expect(outcome).toHaveAttribute('data-outcome', 'failed')
    expect(outcome).toHaveTextContent('550 5.1.1 user unknown')
    expect(outcome).toHaveTextContent('The message was not sent')
    expect(outcome).not.toHaveTextContent(/relay refused/i)
  })

  it('refus 409 : raison affichée, texte conservé', async () => {
    vi.mocked(mailService.send).mockRejectedValue(new ApiError('désinscrit', 409, { error: 'désinscrit', code: 'policy_refused', reason: 'unsubscribed' }))
    open()
    const dialog = await fill()
    await user.click(within(dialog).getByRole('button', { name: 'Send' }))
    expect(await screen.findByRole('alert')).toBeInTheDocument()
    expect(screen.getByTestId('compose-text')).toHaveValue('Un mot.')
  })

  it('adresse douteuse : le bouton reste fermé', async () => {
    open('ana')
    await user.type(screen.getByTestId('compose-subject'), 'B')
    await user.type(screen.getByTestId('compose-text'), 'T')
    expect(screen.getByRole('button', { name: /Review and send/ })).toBeDisabled()
  })
})

describe('?ecrire=', () => {
  it('une adresse ouvre, le reste disparaît', () => {
    expect(normalizeInboxSearch({ ecrire: ' ana@client.example ' }).ecrire).toBe('ana@client.example')
    for (const bad of ['', 'ana', 'a@b', 'a b@c.fr', 'ana@client.example,x@y.fr', 'ana@client.example\nbcc:x@y.fr', 3]) {
      expect(normalizeInboxSearch({ ecrire: bad }).ecrire).toBeUndefined()
    }
    expect(isRecipient('a'.repeat(310) + '@client.example')).toBe(false)
  })
})
