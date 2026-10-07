import { describe, expect, it, vi } from 'vitest'
import { render, screen, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { i18n } from '@lingui/core'
import { I18nProvider } from '@lingui/react'
import { App as AntApp } from 'antd'
import type { ReactNode } from 'react'

import { VeridianProfileCard } from './veridian_profile_card'
import { planClass, profile } from './veridian_profile_test_fixtures'

i18n.loadAndActivate({ locale: 'en', messages: {} })

const wrap = (node: ReactNode) => (
  <I18nProvider i18n={i18n}>
    <AntApp>{node}</AntApp>
  </I18nProvider>
)

const NOW = new Date('2026-10-07T19:28:00Z')

function renderCard(p = profile('a'), all = [p, profile('b')], opts: { isOwner?: boolean; onAction?: () => void } = {}) {
  const onAction = opts.onAction ?? vi.fn()
  render(
    wrap(
      <VeridianProfileCard profile={p} all={all} isOwner={opts.isOwner ?? true} onAction={onAction} now={NOW} />
    )
  )
  return onAction
}

describe('carte de profil: identité', () => {
  it('nom, type, vérifié, expéditeur et rotation', () => {
    renderCard(profile('a', { name: 'nord-propre-1', type: 'gmail_app_password' }))
    expect(screen.getByText('nord-propre-1')).toBeInTheDocument()
    expect(screen.getByText('Gmail, app password')).toBeInTheDocument()
    expect(screen.getByText(/^Verified /)).toBeInTheDocument()
    expect(screen.getByText('In rotation')).toBeInTheDocument()
    expect(screen.getByText(/Robert Brunon <robert@a.example>/)).toBeInTheDocument()
  })

  it('type SMTP et Gmail OAuth', () => {
    renderCard(profile('o', { type: 'gmail_oauth' }))
    expect(screen.getByText('Gmail, OAuth')).toBeInTheDocument()
  })

  it('profil non vérifié: badge orange', () => {
    renderCard(profile('u', { verified: false, verified_at: null, in_rotation: false }, { blocked_by: ['unverified'] }))
    expect(screen.getByText('Not verified')).toBeInTheDocument()
    expect(screen.getByText('Not verified: send a test first')).toBeInTheDocument()
  })

  it('profil en pause', () => {
    renderCard(profile('p', { paused: true }, { paused: true, blocked_by: ['paused'] }))
    expect(screen.getAllByText('Paused').length).toBeGreaterThan(0)
    expect(screen.getByRole('button', { name: 'Resume' })).toBeInTheDocument()
  })
})

describe('carte de profil: aujourd\'hui et porte limitante', () => {
  it('envoyés sur plafond effectif, avec la chauffe en clair', () => {
    renderCard()
    const block = screen.getByTestId('today-block')
    expect(within(block).getByText('149 / 300 sent')).toBeInTheDocument()
    expect(within(screen.getByTestId('limiting-factor')).getByText('Warmup, day 3/5')).toBeInTheDocument()
  })

  it('plafond du profil', () => {
    renderCard(profile('a', {}, { limiting_gate: 'profile_cap', warmup: { active: false } }))
    expect(within(screen.getByTestId('limiting-factor')).getByText('Profile cap')).toBeInTheDocument()
  })

  it('fenêtre fermée: la réouverture, lue dans le fuseau de la fenêtre', () => {
    renderCard(
      profile('w', {}, {
        blocked_by: ['window_closed'],
        window: {
          configured: true,
          open_now: false,
          next_open_at: '2026-10-08T06:00:00Z',
          timezone: 'Europe/Paris',
          source: 'profile'
        }
      })
    )
    expect(screen.getByText('Window closed until Thursday 08:00')).toBeInTheDocument()
  })

  it('réservés en cours quand ils dépassent les envoyés', () => {
    renderCard(profile('r', {}, { sent_today: 10, reserved_today: 14 }))
    expect(screen.getByText(/4 reserved, in flight/)).toBeInTheDocument()
  })

  it('sans plafond: le dit, sans inventer de plafond', () => {
    renderCard(profile('n', {}, { daily_cap_today: null, limiting_gate: 'none', sent_today: 12, warmup: { active: false } }))
    expect(screen.getByText('12 sent, no daily cap')).toBeInTheDocument()
    expect(screen.getByText('No daily cap')).toBeInTheDocument()
  })

  it('aucune extrapolation par minute, par heure ou par jour', () => {
    const { container } = render(wrap(<VeridianProfileCard profile={profile('a')} all={[profile('a')]} isOwner onAction={vi.fn()} now={NOW} />))
    const text = container.textContent ?? ''
    expect(text).not.toMatch(/per minute|per hour|emails per|≈|\/h\b|\/min\b/i)
    // la cadence technique (native_rate_per_min = 6) n'est pas montrée sur la carte
    expect(text).not.toMatch(/\b6\b/)
  })
})

describe('carte de profil: réputation par fournisseur destinataire', () => {
  it('seulement les classes ralenties ou arrêtées, avec facteur et raison', () => {
    renderCard(
      profile('a', {}, {
        classes: [
          planClass('google'),
          planClass('security_gateway', { slowdown_factor: 2, slowdown_reason: 'hard_bounce_rate', slowdown_rate: 0.099, sent_7d: 101 }),
          planClass('microsoft', { stopped: true, slowdown_reason: 'bulk_policy_refusal' })
        ]
      })
    )
    const block = screen.getByTestId('reputation-block')
    expect(within(block).getByText('Anti-spam gateways: rate ÷2, 9.9% bounces')).toBeInTheDocument()
    expect(within(block).getByText('Microsoft: sending stopped, the provider refuses in bulk')).toBeInTheDocument()
    expect(within(block).queryByText(/^Google/)).not.toBeInTheDocument()
  })

  it('profil sain: le dit', () => {
    renderCard()
    expect(screen.getByText('No provider slowed or stopped')).toBeInTheDocument()
  })

  it('classes exclues', () => {
    renderCard(profile('a', {}, { excluded_classes: ['ionos', 'ovh'] }))
    expect(within(screen.getByTestId('excluded-classes')).getByText('IONOS / 1&1, OVH')).toBeInTheDocument()
  })
})

describe('carte de profil: boîte IMAP liée', () => {
  it('adresse, hôte, dossier et rôle', () => {
    renderCard(
      profile('a', {
        return_inbox: {
          integration_id: 'i1',
          name: 'Relais',
          host: 'imap.gmail.com',
          address: 'robert@gmail.com',
          folder: 'INBOX',
          linked_profiles: ['a']
        }
      })
    )
    const block = screen.getByTestId('inbox-block')
    expect(within(block).getByText('robert@gmail.com')).toBeInTheDocument()
    expect(block.textContent).toContain('imap.gmail.com')
    expect(block.textContent).toContain('Replies and bounces')
  })

  it('aucune boîte liée', () => {
    renderCard()
    expect(within(screen.getByTestId('inbox-block')).getByText('No linked inbox')).toBeInTheDocument()
  })
})

describe('carte de profil: exclusivité et actions', () => {
  it('un profil transactionnel n\'a ni rotation ni règle commerciale', () => {
    const t = profile('t', { usage: 'transactional', in_rotation: false }, {
      applicable: false,
      mode: 'transactional',
      sent_today: 7,
      daily_cap_today: null
    })
    renderCard(t, [t, profile('a')])
    expect(screen.queryByRole('button', { name: 'Add to rotation' })).not.toBeInTheDocument()
    expect(screen.queryByRole('button', { name: 'Remove from rotation' })).not.toBeInTheDocument()
    expect(screen.queryByTestId('reputation-block')).not.toBeInTheDocument()
    expect(screen.getByTestId('today-block').textContent).toContain('no daily cap and no commercial gate')
  })

  it('profil commercial hors rotation: Ajouter à la rotation actif', async () => {
    const p = profile('a', { in_rotation: false })
    const onAction = renderCard(p, [p, profile('b')])
    const button = screen.getByRole('button', { name: 'Add to rotation' })
    expect(button).toBeEnabled()
    await userEvent.click(button)
    expect(onAction).toHaveBeenCalledWith('rotation', p)
  })

  it('profil non vérifié: Ajouter à la rotation refusé', () => {
    const p = profile('a', { in_rotation: false, verified: false, verified_at: null })
    renderCard(p, [p, profile('b')])
    expect(screen.getByRole('button', { name: 'Add to rotation' })).toBeDisabled()
  })

  it('dernier profil de la rotation: Retirer refusé', () => {
    const p = profile('a')
    renderCard(p, [p])
    expect(screen.getByRole('button', { name: 'Remove from rotation' })).toBeDisabled()
  })

  it('Tester, Modifier et Supprimer (après confirmation) remontent l\'action', async () => {
    const p = profile('a')
    const onAction = renderCard(p, [p, profile('b')])
    await userEvent.click(screen.getByRole('button', { name: 'Test' }))
    await userEvent.click(screen.getByRole('button', { name: 'Edit' }))
    expect(onAction).toHaveBeenCalledWith('test', p)
    expect(onAction).toHaveBeenCalledWith('edit', p)

    await userEvent.click(screen.getByRole('button', { name: 'Delete' }))
    expect(onAction).not.toHaveBeenCalledWith('delete', p)
    const confirm = await screen.findAllByRole('button', { name: 'Delete' })
    await userEvent.click(confirm[confirm.length - 1])
    expect(onAction).toHaveBeenCalledWith('delete', p)
  })

  it('Mettre en pause demande confirmation', async () => {
    const p = profile('a')
    const onAction = renderCard(p, [p, profile('b')])
    await userEvent.click(screen.getByRole('button', { name: 'Pause' }))
    expect(onAction).not.toHaveBeenCalled()
    expect(await screen.findByText('Pause this profile?')).toBeInTheDocument()
    const buttons = screen.getAllByRole('button', { name: 'Pause' })
    await userEvent.click(buttons[buttons.length - 1])
    expect(onAction).toHaveBeenCalledWith('pause', p)
  })

  it('un membre sans droit de propriétaire ne voit aucune action', () => {
    renderCard(profile('a'), [profile('a')], { isOwner: false })
    expect(screen.queryByRole('button', { name: 'Edit' })).not.toBeInTheDocument()
    expect(screen.getByText('Only the workspace owner can change profiles')).toBeInTheDocument()
  })
})
