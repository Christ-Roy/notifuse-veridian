import { describe, it, expect, vi, beforeEach } from 'vitest'
import { screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'

vi.mock('../../services/api/contacts', async (importOriginal) => {
  const actual = await importOriginal<typeof import('../../services/api/contacts')>()
  return { ...actual, contactsApi: { ...actual.contactsApi, list: vi.fn() } }
})
vi.mock('../../services/api/messages_history', async (importOriginal) => {
  const actual = await importOriginal<typeof import('../../services/api/messages_history')>()
  return { ...actual, listMessages: vi.fn().mockResolvedValue({ messages: [], next_cursor: '' }) }
})
vi.mock('../../services/api/contact_timeline', async (importOriginal) => {
  const actual = await importOriginal<typeof import('../../services/api/contact_timeline')>()
  return { ...actual, contactTimelineApi: { ...actual.contactTimelineApi, list: vi.fn().mockResolvedValue({ timeline: [], next_cursor: '' }) } }
})
vi.mock('../../services/api/list', () => ({ listsApi: { list: vi.fn().mockResolvedValue({ lists: [] }) } }))
vi.mock('../../services/api/segment', async (importOriginal) => {
  const actual = await importOriginal<typeof import('../../services/api/segment')>()
  return { ...actual, listSegments: vi.fn().mockResolvedValue({ segments: [] }) }
})

import { AuthContext } from '../../contexts/AuthContext'
import { contactsApi } from '../../services/api/contacts'
import { MailContactButton } from './veridian_mail_contact_button'
import { WS } from './veridian_mail_fixtures'
import { renderMail } from './veridian_mail_test_utils'

const user = userEvent.setup({ delay: null })
const withWorkspace = (ui: React.ReactElement) => (
  // Seul l'espace courant compte pour le bouton ; le reste du contexte n'est pas lu.
  <AuthContext.Provider value={{ workspaces: [{ id: WS, name: 'Ilum Photos', settings: {} }] } as never}>{ui}</AuthContext.Provider>
)

// 27/11/2026, Robert : depuis un message, voir le profil du contact et ses coordonnées.
describe('Voir le contact depuis un message', () => {
  beforeEach(() => vi.clearAllMocks())

  it('ouvre la fiche du contact rattaché au message, avec ses coordonnées', async () => {
    vi.mocked(contactsApi.list).mockResolvedValue({
      contacts: [{ email: 'info@lamasialescasotes.com', first_name: 'Núria', last_name: 'Puig', phone: '+34 600 000 000', contact_lists: [] }]
    } as never)
    renderMail(withWorkspace(<MailContactButton workspaceId={WS} email="info@lamasialescasotes.com" />))
    await user.click(screen.getByRole('button', { name: /View contact/ }))
    await waitFor(() => expect(contactsApi.list).toHaveBeenCalledWith(expect.objectContaining({ workspace_id: WS, email: 'info@lamasialescasotes.com' })))
    expect(await screen.findByText('+34 600 000 000')).toBeInTheDocument()
    expect(screen.getAllByText(/info@lamasialescasotes\.com/).length).toBeGreaterThan(0)
  })

  it('une fiche de prospect : champs nommés en tête, champs vides repliés', async () => {
    vi.mocked(contactsApi.list).mockResolvedValue({
      contacts: [{ email: 'contacto@lugar.test', custom_string_4: 'La Masia les Casotes', custom_number_1: 299, contact_lists: [] }]
    } as never)
    const labels = { custom_string_4: 'Entreprise', custom_number_1: 'Score' }
    renderMail(
      <AuthContext.Provider value={{ workspaces: [{ id: WS, name: 'Ilum Photos', settings: { custom_field_labels: labels } }] } as never}>
        <MailContactButton workspaceId={WS} email="contacto@lugar.test" />
      </AuthContext.Provider>
    )
    await user.click(screen.getByRole('button', { name: /View contact/ }))
    expect(await screen.findByText('La Masia les Casotes')).toBeInTheDocument()
    expect(screen.getByText('Entreprise')).toBeInTheDocument()
    expect(screen.getByTestId('unset-standard-fields')).toHaveTextContent(/Empty fields \(\d+\)/)
    // Le téléphone vide n'occupe plus une ligne « non défini » : il attend dans la section repliée.
    expect(screen.queryByText('Phone')).toBeNull()
    await user.click(screen.getByTestId('unset-standard-fields'))
    expect(await screen.findByText('Phone')).toBeInTheDocument()
  })

  it('aucun bouton sans contact rattaché ni hors session', () => {
    renderMail(withWorkspace(<MailContactButton workspaceId={WS} email="" />))
    renderMail(<MailContactButton workspaceId={WS} email="a@b.fr" />)
    expect(screen.queryByTestId('mail-contact-button')).toBeNull()
  })
})
