import { beforeEach, describe, expect, it, vi } from 'vitest'
import { render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { i18n } from '@lingui/core'
import { I18nProvider } from '@lingui/react'
import { App as AntApp } from 'antd'

i18n.loadAndActivate({ locale: 'en', messages: {} })

const user = userEvent.setup({ delay: null })
vi.setConfig({ testTimeout: 30000 })

vi.mock('../../services/api/veridian_email_profiles', async () => {
  const actual = await vi.importActual<typeof import('../../services/api/veridian_email_profiles')>(
    '../../services/api/veridian_email_profiles'
  )
  return { ...actual, emailProfilesCreateService: { create: vi.fn() } }
})

vi.mock('./veridian_profile_ops', () => ({
  testProfile: vi.fn(),
  setRotation: vi.fn(),
  setUsage: vi.fn()
}))

import { emailProfilesCreateService } from '../../services/api/veridian_email_profiles'
import { setRotation, setUsage, testProfile } from './veridian_profile_ops'
import {
  GMAIL_APP_PASSWORDS_URL,
  GMAIL_TWO_STEP_URL,
  VeridianProfileWizard
} from './veridian_profile_wizard'

const SECRET = 'abcd efgh ijkl mnop'
const SECRET_COMPACT = 'abcdefghijklmnop'

function renderWizard() {
  const onClose = vi.fn()
  const onChanged = vi.fn()
  render(
    <I18nProvider i18n={i18n}>
      <AntApp>
        <VeridianProfileWizard
          open
          workspaceId="ws-1"
          ownerEmail="robert.brunon@veridian.site"
          onClose={onClose}
          onChanged={onChanged}
        />
      </AntApp>
    </I18nProvider>
  )
  return { onClose, onChanged }
}

const everythingOnScreen = () =>
  document.body.textContent +
  Array.from(document.querySelectorAll('input')).map((i) => `${i.value}|${i.defaultValue}`).join('\n')

describe('assistant Ajouter un profil: les trois choix', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    vi.mocked(emailProfilesCreateService.create).mockResolvedValue({ integration_id: 'p-1', imap_integration_id: 'i-1' })
    vi.mocked(testProfile).mockResolvedValue({ success: true })
    vi.mocked(setRotation).mockResolvedValue(undefined)
    vi.mocked(setUsage).mockResolvedValue(undefined)
  })

  it('propose SMTP + IMAP, Gmail mot de passe d\'application et Gmail OAuth grisé « Bientôt »', async () => {
    renderWizard()
    expect(screen.getByTestId('choice-smtp-imap')).toBeInTheDocument()
    expect(screen.getByTestId('choice-gmail-app-password')).toBeInTheDocument()
    const oauth = screen.getByTestId('choice-gmail-oauth')
    expect(oauth).toHaveAttribute('aria-disabled', 'true')
    expect(oauth.textContent).toContain('Soon')
    await user.click(oauth)
    // aucun faux bouton: on reste sur le choix
    expect(screen.getByTestId('choice-smtp-imap')).toBeInTheDocument()
    expect(screen.queryByRole('button', { name: 'Create and test' })).not.toBeInTheDocument()
    expect(emailProfilesCreateService.create).not.toHaveBeenCalled()
  })

  describe('parcours Gmail avec mot de passe d\'application', () => {
    it('affiche le lien direct et les 3 étapes, plafond 30 par défaut', async () => {
      renderWizard()
      await user.click(screen.getByTestId('choice-gmail-app-password'))

      const link = screen.getByRole('link', { name: 'Open Google app passwords' })
      expect(link).toHaveAttribute('href', 'https://myaccount.google.com/apppasswords')
      expect(GMAIL_APP_PASSWORDS_URL).toBe('https://myaccount.google.com/apppasswords')
      expect(link).toHaveAttribute('target', '_blank')
      expect(link.getAttribute('rel')).toContain('noopener')
      expect(screen.getByRole('link', { name: 'Open 2-step verification' })).toHaveAttribute('href', GMAIL_TWO_STEP_URL)
      expect(screen.getByText(/Turn on 2-step verification/)).toBeInTheDocument()
      expect(screen.getByText(/create a password named “Notifuse”/)).toBeInTheDocument()
      expect(screen.getByText(/Copy the 16 characters/)).toBeInTheDocument()
      expect(screen.getByRole('spinbutton')).toHaveValue('30')
    })

    it('crée SMTP + IMAP en un appel, teste vers le propriétaire, propose la rotation, sans jamais réafficher le secret', async () => {
      const { onChanged, onClose } = renderWizard()
      await user.click(screen.getByTestId('choice-gmail-app-password'))
      await user.type(screen.getByLabelText('Gmail address'), 'Prenom.Nom@gmail.com')
      await user.type(screen.getByLabelText('Display name'), 'Prénom Nom')
      await user.type(screen.getByLabelText('App password (16 characters)'), SECRET)
      await user.click(screen.getByRole('button', { name: 'Create and test' }))

      await waitFor(() => expect(emailProfilesCreateService.create).toHaveBeenCalledTimes(1))
      expect(emailProfilesCreateService.create).toHaveBeenCalledWith({
        workspace_id: 'ws-1',
        type: 'gmail_app_password',
        name: 'Prenom.Nom@gmail.com',
        sender_email: 'Prenom.Nom@gmail.com',
        sender_name: 'Prénom Nom',
        app_password: SECRET_COMPACT,
        gmail_account_type: 'personal',
        profile_daily_cap: 30
      })
      expect(testProfile).toHaveBeenCalledWith('ws-1', 'p-1', 'robert.brunon@veridian.site')
      expect(await screen.findByText('Transport verified')).toBeInTheDocument()
      expect(onChanged).toHaveBeenCalled()

      // le secret n'est plus nulle part à l'écran
      expect(everythingOnScreen()).not.toContain(SECRET_COMPACT)
      expect(everythingOnScreen()).not.toContain(SECRET)

      await user.click(screen.getByRole('button', { name: 'Add to the commercial rotation' }))
      await waitFor(() => expect(setRotation).toHaveBeenCalledWith('ws-1', 'p-1', true))
      await waitFor(() => expect(onClose).toHaveBeenCalled())
    })

    it('peut réserver le profil au transactionnel', async () => {
      renderWizard()
      await user.click(screen.getByTestId('choice-gmail-app-password'))
      await user.type(screen.getByLabelText('Gmail address'), 'a@gmail.com')
      await user.type(screen.getByLabelText('App password (16 characters)'), SECRET)
      await user.click(screen.getByRole('button', { name: 'Create and test' }))
      await user.click(await screen.findByRole('button', { name: 'Reserve for transactional mail' }))
      await waitFor(() => expect(setUsage).toHaveBeenCalledWith('ws-1', 'p-1', 'transactional'))
      expect(setRotation).not.toHaveBeenCalled()
    })

    it('refuse un mot de passe qui n\'a pas 16 caractères, sans rien créer', async () => {
      renderWizard()
      await user.click(screen.getByTestId('choice-gmail-app-password'))
      await user.type(screen.getByLabelText('Gmail address'), 'a@gmail.com')
      await user.type(screen.getByLabelText('App password (16 characters)'), 'trop court')
      await user.click(screen.getByRole('button', { name: 'Create and test' }))
      expect(await screen.findByText('The app password has exactly 16 characters')).toBeInTheDocument()
      expect(emailProfilesCreateService.create).not.toHaveBeenCalled()
    })

    it('échec du test de transport: le profil existe, aucune proposition de rotation', async () => {
      vi.mocked(testProfile).mockResolvedValue({ success: false, error: 'Authentication failed' })
      renderWizard()
      await user.click(screen.getByTestId('choice-gmail-app-password'))
      await user.type(screen.getByLabelText('Gmail address'), 'a@gmail.com')
      await user.type(screen.getByLabelText('App password (16 characters)'), SECRET)
      await user.click(screen.getByRole('button', { name: 'Create and test' }))
      expect(await screen.findByText('Profile created, but the test failed')).toBeInTheDocument()
      expect(screen.getByText('Authentication failed')).toBeInTheDocument()
      expect(screen.queryByRole('button', { name: 'Add to the commercial rotation' })).not.toBeInTheDocument()
      expect(everythingOnScreen()).not.toContain(SECRET_COMPACT)
    })

    it('refus du serveur à la création: erreur affichée, aucun test lancé', async () => {
      vi.mocked(emailProfilesCreateService.create).mockRejectedValue(new Error('Gmail profile daily cap must not exceed 450'))
      renderWizard()
      await user.click(screen.getByTestId('choice-gmail-app-password'))
      await user.type(screen.getByLabelText('Gmail address'), 'a@gmail.com')
      await user.type(screen.getByLabelText('App password (16 characters)'), SECRET)
      await user.click(screen.getByRole('button', { name: 'Create and test' }))
      expect(await screen.findByText('Gmail profile daily cap must not exceed 450')).toBeInTheDocument()
      expect(testProfile).not.toHaveBeenCalled()
    })
  })

  describe('parcours SMTP + IMAP', () => {
    async function fillSmtp() {
      await user.click(screen.getByTestId('choice-smtp-imap'))
      await user.type(screen.getByLabelText('Sender address'), 'contact@exemple.fr')
      await user.type(screen.getByLabelText('Host'), 'mail.exemple.fr')
      await user.type(screen.getByLabelText('Login'), 'contact@exemple.fr')
      await user.type(screen.getByLabelText('Password'), 'S3cret!')
    }

    it('préremplit l\'IMAP de retour sur le même hôte et le même identifiant', async () => {
      renderWizard()
      await fillSmtp()
      await waitFor(() => expect(screen.getByLabelText('IMAP host')).toHaveValue('mail.exemple.fr'))
      await waitFor(() => expect(screen.getByLabelText('IMAP login')).toHaveValue('contact@exemple.fr'))
    })

    it('crée le profil avec sa boîte IMAP en un appel', async () => {
      renderWizard()
      await fillSmtp()
      await user.click(screen.getByRole('button', { name: 'Create and test' }))
      await waitFor(() => expect(emailProfilesCreateService.create).toHaveBeenCalledTimes(1))
      expect(emailProfilesCreateService.create).toHaveBeenCalledWith({
        workspace_id: 'ws-1',
        type: 'smtp_imap',
        name: 'contact@exemple.fr',
        sender_email: 'contact@exemple.fr',
        sender_name: '',
        smtp: { host: 'mail.exemple.fr', port: 587, use_tls: true, username: 'contact@exemple.fr', password: 'S3cret!' },
        imap: { host: 'mail.exemple.fr', port: 993, use_tls: true, username: 'contact@exemple.fr', password: 'S3cret!' }
      })
      expect(await screen.findByText('Transport verified')).toBeInTheDocument()
      expect(everythingOnScreen()).not.toContain('S3cret!')
    })

    it('IMAP décoché: pas de boîte dans la demande', async () => {
      renderWizard()
      await fillSmtp()
      await user.click(screen.getByRole('checkbox', { name: /Link a return inbox/ }))
      expect(screen.queryByTestId('imap-block')).not.toBeInTheDocument()
      await user.click(screen.getByRole('button', { name: 'Create and test' }))
      await waitFor(() => expect(emailProfilesCreateService.create).toHaveBeenCalledTimes(1))
      const request = vi.mocked(emailProfilesCreateService.create).mock.calls[0][0]
      expect(request.imap).toBeUndefined()
    })

    it('mot de passe IMAP distinct quand « même mot de passe » est décoché', async () => {
      renderWizard()
      await fillSmtp()
      await user.click(screen.getByRole('checkbox', { name: 'Same password as SMTP' }))
      await user.type(screen.getByLabelText('IMAP password'), 'autre-secret')
      await user.click(screen.getByRole('button', { name: 'Create and test' }))
      await waitFor(() => expect(emailProfilesCreateService.create).toHaveBeenCalledTimes(1))
      const request = vi.mocked(emailProfilesCreateService.create).mock.calls[0][0]
      expect(request.imap?.password).toBe('autre-secret')
      expect(request.smtp?.password).toBe('S3cret!')
    })
  })
})
