import { describe, it, expect, vi } from 'vitest'
import { screen, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'

vi.mock('../../router', () => ({ router: { navigate: vi.fn() } }))

import { VeridianMailDsnCard } from './veridian_mail_dsn_card'
import { hardBounce, inboxFixture, softBounce, WS } from './veridian_mail_fixtures'
import { VeridianMailTimeline } from './veridian_mail_timeline'
import { renderMail } from './veridian_mail_test_utils'

const user = userEvent.setup({ delay: null })

describe('rapport de non-remise expliqué', () => {
  it('rebond définitif : raison en grand, conseil, code, fournisseur, adresse en échec', () => {
    const dsn = hardBounce().dsn!
    renderMail(<VeridianMailDsnCard dsn={dsn} />)
    expect(screen.getByText('The mail was not delivered')).toBeInTheDocument()
    expect(screen.getByTestId('dsn-reason')).toHaveTextContent('L’adresse n’existe pas chez le fournisseur du destinataire.')
    expect(screen.getByTestId('dsn-reason').style.fontSize).toBe('20px')
    expect(screen.getByTestId('dsn-advice')).toHaveTextContent('inutile d’insister')
    const card = screen.getByTestId('dsn-card')
    expect(within(card).getByText('5.1.1')).toBeInTheDocument()
    expect(within(card).getByText('Gmail')).toBeInTheDocument()
    expect(within(card).getByText('accueil@vetos-du-lac.example')).toBeInTheDocument()
    expect(within(card).getByText('Failed address')).toBeInTheDocument()
  })

  it('le diagnostic technique est replié, puis lisible au clic', async () => {
    renderMail(<VeridianMailDsnCard dsn={hardBounce().dsn!} />)
    expect(screen.queryByTestId('dsn-diagnostic')).toBeNull()
    await user.click(screen.getByText('Technical diagnostic'))
    expect(await screen.findByTestId('dsn-diagnostic')).toHaveTextContent('550-5.1.1 The email account that you tried to reach does not exist')
  })

  it('rebond temporaire : ton différent, titre « pas remis pour l’instant »', () => {
    renderMail(<VeridianMailDsnCard dsn={softBounce().dsn!} />)
    expect(screen.getByText('The mail could not be delivered for now')).toBeInTheDocument()
    expect(screen.getByTestId('dsn-reason')).toHaveTextContent('pleine')
  })

  it('rapport sans diagnostic ni conseil : pas de bloc vide', () => {
    renderMail(<VeridianMailDsnCard dsn={{ reason_fr: 'Remise retardée.', action: 'delayed', severity: 'soft' }} />)
    expect(screen.getByText('Delivery is delayed')).toBeInTheDocument()
    expect(screen.queryByTestId('dsn-advice')).toBeNull()
    expect(screen.queryByText('Technical diagnostic')).toBeNull()
  })

  it('le diagnostic est du texte échappé, jamais du HTML', async () => {
    renderMail(<VeridianMailDsnCard dsn={{ reason_fr: 'x', diagnostic: '<img src=x onerror=alert(1)> 550 refus' }} />)
    await user.click(screen.getByText('Technical diagnostic'))
    const pre = await screen.findByTestId('dsn-diagnostic')
    expect(pre.querySelector('img')).toBeNull()
    expect(pre.textContent).toContain('<img src=x')
  })
})

describe('chronologie du fil', () => {
  it('une puce par message, jalons du serveur, écarts « N j après », message courant en évidence', () => {
    const { thread, reply } = inboxFixture()
    renderMail(<VeridianMailTimeline workspaceId={WS} thread={thread} currentId={reply.id} />)
    const steps = screen.getAllByTestId('timeline-step')
    expect(steps).toHaveLength(3)
    expect(steps.map((s) => s.getAttribute('data-kind'))).toEqual(['sent', 'sent', 'reply_human'])
    expect(within(steps[0]).getByText('Premier mail')).toBeInTheDocument()
    expect(within(steps[1]).getByText('Relance 1')).toBeInTheDocument()
    expect(within(steps[2]).getByText('Réponse')).toBeInTheDocument()
    expect(within(steps[1]).getByText(/2 days after/)).toBeInTheDocument()
    expect(within(steps[2]).getByText(/1 day after/)).toBeInTheDocument()
    expect(within(steps[0]).queryByText(/after/)).toBeNull()
    expect(steps.map((s) => s.getAttribute('data-current'))).toEqual(['false', 'false', 'true'])
    expect(steps[2]).toHaveAttribute('aria-current', 'step')
    expect(screen.getAllByTestId('kind-dot-sent')).toHaveLength(2)
    expect(screen.getAllByTestId('kind-dot-reply_human')).toHaveLength(1)
  })

  it('une étape se déplie pour montrer le mail rendu dans l’iframe isolé', async () => {
    const { thread } = inboxFixture()
    renderMail(<VeridianMailTimeline workspaceId={WS} thread={thread} currentId="m-reply" />)
    expect(screen.queryByTestId('mail-body-frame')).toBeNull()
    const steps = screen.getAllByTestId('timeline-step')
    const toggle = within(steps[0]).getByRole('button', { expanded: false })
    await user.click(toggle)
    expect(toggle).toHaveAttribute('aria-expanded', 'true')
    const frame = within(steps[0]).getByTestId('mail-body-frame')
    expect(frame.getAttribute('sandbox')).not.toContain('allow-scripts')
    expect(frame.getAttribute('srcdoc')).toContain('Seriez-vous disponible')
    await user.click(toggle)
    expect(within(steps[0]).queryByTestId('mail-body-frame')).toBeNull()
  })

  it('expandCurrent : le message affiché est déplié d’emblée', () => {
    const { thread, reply } = inboxFixture()
    renderMail(<VeridianMailTimeline workspaceId={WS} thread={thread} currentId={reply.id} expandCurrent />)
    expect(screen.getAllByTestId('step-body')).toHaveLength(1)
    expect(within(screen.getAllByTestId('timeline-step')[2]).getByTestId('step-body')).toBeInTheDocument()
  })

  it('les rebonds montrent leur raison en français en ligne, sans déplier', () => {
    const { threads } = inboxFixture()
    renderMail(<VeridianMailTimeline workspaceId={WS} thread={threads['th-2']} currentId="m-bounce" />)
    const steps = screen.getAllByTestId('timeline-step')
    expect(within(steps[1]).getByText('Rebond définitif')).toBeInTheDocument()
    expect(within(steps[1]).getByTestId('step-reason')).toHaveTextContent('L’adresse n’existe pas')
    expect(steps[1].getAttribute('data-kind')).toBe('bounce_hard')
  })

  it('ouvrir une autre étape rend la main à la page', async () => {
    const { thread } = inboxFixture()
    const onOpen = vi.fn()
    renderMail(<VeridianMailTimeline workspaceId={WS} thread={thread} currentId="m-reply" onOpen={onOpen} />)
    const steps = screen.getAllByTestId('timeline-step')
    await user.click(within(steps[1]).getByRole('button', { expanded: false }))
    await user.click(within(steps[1]).getByRole('button', { name: 'Open this message' }))
    expect(onOpen).toHaveBeenCalledWith('m-relance')
    // le message courant n'a pas de bouton « ouvrir »
    await user.click(within(steps[2]).getByRole('button', { expanded: false }))
    expect(within(steps[2]).queryByRole('button', { name: 'Open this message' })).toBeNull()
  })

  it('pas de message : rien', () => {
    const { thread } = inboxFixture()
    const { container } = renderMail(<VeridianMailTimeline workspaceId={WS} thread={{ ...thread, messages: [] }} />)
    expect(container.querySelector('ol')).toBeNull()
  })
})
