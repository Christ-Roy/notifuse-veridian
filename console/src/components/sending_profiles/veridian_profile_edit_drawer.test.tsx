import { beforeEach, describe, expect, it, vi } from 'vitest'
import { render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { i18n } from '@lingui/core'
import { I18nProvider } from '@lingui/react'
import { App as AntApp } from 'antd'

import type { Integration, Workspace } from '../../services/api/types'
import { VeridianProfileEditDrawer } from './veridian_profile_edit_drawer'
import { profile } from './veridian_profile_test_fixtures'

i18n.loadAndActivate({ locale: 'en', messages: {} })

const user = userEvent.setup({ delay: null })
vi.setConfig({ testTimeout: 30000 })

vi.mock('./veridian_profile_ops', () => ({
  updateProfile: vi.fn().mockResolvedValue(undefined),
  setUsage: vi.fn().mockResolvedValue(undefined),
  saveInbox: vi.fn()
}))

import { setUsage, updateProfile } from './veridian_profile_ops'

const integration = (id: string): Integration => ({
  id,
  name: `Profil ${id}`,
  type: 'email',
  created_at: '',
  updated_at: '',
  email_provider: {
    kind: 'smtp',
    rate_limit_per_minute: 60,
    veridian_profile_daily_cap: 300,
    veridian_credentials_configured: true,
    veridian_transport_verified_at: '2026-10-04T07:00:00Z',
    smtp: { host: 'smtp.exemple.fr', port: 587, username: 'robert', use_tls: true, has_password: true },
    senders: [{ id: 's1', email: `robert@${id}.example`, name: 'Robert', is_default: true }]
  }
})

const workspace = {
  id: 'ws-1',
  name: 'Atelier',
  created_at: '',
  updated_at: '',
  settings: { timezone: 'UTC', email_tracking_enabled: false, default_language: 'fr', languages: ['fr'] },
  integrations: [integration('a')]
} as unknown as Workspace

function renderDrawer(p = profile('a')) {
  const onSaved = vi.fn()
  const onClose = vi.fn()
  render(
    <I18nProvider i18n={i18n}>
      <AntApp>
        <VeridianProfileEditDrawer open workspace={workspace} profile={p} onClose={onClose} onSaved={onSaved} />
      </AntApp>
    </I18nProvider>
  )
  return { onSaved, onClose }
}

describe('tiroir Modifier', () => {
  beforeEach(() => vi.clearAllMocks())

  it('ne réaffiche jamais le secret: le champ mot de passe est vide', async () => {
    renderDrawer()
    expect(await screen.findByLabelText('smtp-password')).toHaveValue('')
    expect(screen.getByLabelText('smtp-host')).toHaveValue('smtp.exemple.fr')
  })

  it('les réglages avancés sont repliés', async () => {
    renderDrawer()
    await screen.findByLabelText('profile-name')
    expect(screen.getByText('Advanced settings')).toBeInTheDocument()
    expect(screen.queryByTestId('profile-advanced')).not.toBeInTheDocument()
    await user.click(screen.getByText('Advanced settings'))
    expect(await screen.findByTestId('profile-advanced')).toBeInTheDocument()
    expect(screen.getByText('SMTP technical brake (messages/minute)')).toBeInTheDocument()
  })

  it("un profil non affecté s'ouvre en commercial et un enregistrement simple ne change pas l'usage", async () => {
    renderDrawer(profile('a', { usage: 'unassigned', in_rotation: false }))
    const commercial = await screen.findByRole('radio', { name: 'Commercial' })
    expect(commercial).toBeChecked()
    await user.click(screen.getByRole('button', { name: 'Save' }))
    await waitFor(() => expect(updateProfile).toHaveBeenCalledTimes(1))
    expect(setUsage).not.toHaveBeenCalled()
  })

  it("passer en transactionnel appelle l'usage exclusif après l'enregistrement", async () => {
    const { onSaved, onClose } = renderDrawer()
    await user.click(await screen.findByRole('radio', { name: 'Transactional' }))
    await user.click(screen.getByRole('button', { name: 'Save' }))
    await waitFor(() => expect(setUsage).toHaveBeenCalledWith('ws-1', 'a', 'transactional'))
    await waitFor(() => expect(onSaved).toHaveBeenCalled())
    expect(onClose).toHaveBeenCalled()
  })

  it('enregistre le profil entier: les réglages non touchés partent avec', async () => {
    renderDrawer()
    await user.clear(await screen.findByLabelText('profile-name'))
    await user.type(screen.getByLabelText('profile-name'), 'Nouveau nom')
    await user.click(screen.getByRole('button', { name: 'Save' }))
    await waitFor(() => expect(updateProfile).toHaveBeenCalledTimes(1))
    const [workspaceId, id, patch] = vi.mocked(updateProfile).mock.calls[0]
    expect(workspaceId).toBe('ws-1')
    expect(id).toBe('a')
    expect(patch.name).toBe('Nouveau nom')
    const provider = patch.provider?.(integration('a').email_provider as never)
    expect(provider?.veridian_profile_daily_cap).toBe(300)
    expect(provider?.senders[0].email).toBe('robert@a.example')
    // aucun mot de passe saisi: la requête n'en porte pas
    expect(provider?.smtp?.password || '').toBe('')
  })

  it('modifier le transport prévient que la vérification sera effacée', async () => {
    renderDrawer()
    await user.type(await screen.findByLabelText('smtp-login'), 'x')
    expect(await screen.findByText(/Changing the transport clears the verification/)).toBeInTheDocument()
  })

  it("refuse d'enregistrer sans adresse expéditrice", async () => {
    renderDrawer()
    await user.clear(await screen.findByLabelText('sender-email-0'))
    await user.click(screen.getByRole('button', { name: 'Save' }))
    expect(await screen.findByText('At least one sender address is required')).toBeInTheDocument()
    expect(updateProfile).not.toHaveBeenCalled()
  })
})
