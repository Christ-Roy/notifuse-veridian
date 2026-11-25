import { beforeEach, describe, expect, it, vi } from 'vitest'
import { render, screen, waitFor } from '@testing-library/react'
import { i18n } from '@lingui/core'
import { I18nProvider } from '@lingui/react'

i18n.loadAndActivate({ locale: 'en', messages: {} })

let currentSection = 'integrations'
let currentRole = 'admin'
const navigateMock = vi.fn()

vi.mock('@tanstack/react-router', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@tanstack/react-router')>()
  return {
    ...actual,
    useParams: () => ({ workspaceId: 'ws-1', section: currentSection }),
    useNavigate: () => navigateMock
  }
})
// Reference stable : un nouvel objet a chaque rendu relancerait l'effet de la page en boucle.
const authValue = {
  user: { id: 'u-1', email: 'x@example.com' },
  workspaces: [{ id: 'ws-1', name: 'Espace', integrations: [] }],
  refreshWorkspaces: vi.fn()
}
// jsdom n'a pas de media query : on force l'ecran large pour que le menu soit affiche.
vi.mock('antd', async (importOriginal) => {
  const actual = await importOriginal<typeof import('antd')>()
  return { ...actual, Grid: { ...actual.Grid, useBreakpoint: () => ({ md: true }) } }
})
vi.mock('../contexts/AuthContext', () => ({ useAuth: () => authValue }))
vi.mock('../services/api/workspace', () => ({
  workspaceService: {
    getMembers: vi.fn(async () => ({
      members: [{ user_id: 'u-1', workspace_id: 'ws-1', role: currentRole, email: 'x@example.com', type: 'user', permissions: {} }]
    }))
  }
}))
// Les sections lourdes sont remplacees par des sondes : on teste la decision de droits de la page.
vi.mock('../components/settings/Integrations', () => ({
  Integrations: ({ isOwner }: { isOwner: boolean }) => <div>{isOwner ? 'integrations-editables' : 'integrations-lecture'}</div>
}))
vi.mock('../components/settings/DeleteWorkspace', () => ({
  DeleteWorkspaceSection: () => <div>suppression-espace</div>
}))
vi.mock('../components/settings/WorkspaceMembers', () => ({ WorkspaceMembers: () => <div>equipe</div> }))
vi.mock('../components/settings/ApiAgentsSettings', () => ({ ApiAgentsSettings: () => <div>api</div> }))
vi.mock('../components/settings/GeneralSettings', () => ({ GeneralSettings: () => <div>general</div> }))
vi.mock('../components/settings/CustomFieldsConfiguration', () => ({ CustomFieldsConfiguration: () => <div>cf</div> }))
vi.mock('../components/settings/WebhooksSettings', () => ({ WebhooksSettings: () => <div>wh</div> }))
vi.mock('../components/settings/veridian_plan_settings', () => ({ VeridianPlanSettings: () => <div>plan</div> }))
vi.mock('../components/settings/veridian_mailbox_access', () => ({ VeridianMailboxAccess: () => <div>mb</div> }))
vi.mock('../components/settings/veridian_cold_outreach_settings', () => ({
  VeridianColdOutreachSettings: () => <div>cold</div>
}))

import { WorkspaceSettingsPage } from './WorkspaceSettingsPage'

const renderPage = () =>
  render(
    <I18nProvider i18n={i18n}>
      <WorkspaceSettingsPage />
    </I18nProvider>
  )

describe('WorkspaceSettingsPage : role admin', () => {
  beforeEach(() => {
    currentSection = 'integrations'
    currentRole = 'admin'
  })

  it('admin : integrations editables, mais pas de zone de danger dans le menu', async () => {
    renderPage()
    expect(await screen.findByText('integrations-editables')).toBeInTheDocument()
    expect(screen.queryByText('Danger Zone')).not.toBeInTheDocument()
  })

  it('admin qui force la section danger-zone : aucune suppression d espace', async () => {
    currentSection = 'danger-zone'
    renderPage()
    await waitFor(() => expect(screen.getByText('Team')).toBeInTheDocument())
    await new Promise((r) => setTimeout(r, 50))
    expect(screen.queryByText('suppression-espace')).not.toBeInTheDocument()
  })

  it('proprietaire : integrations editables ET zone de danger', async () => {
    currentRole = 'owner'
    renderPage()
    expect(await screen.findByText('integrations-editables')).toBeInTheDocument()
    expect(await screen.findByText('Danger Zone')).toBeInTheDocument()
  })

  it('proprietaire sur la section danger-zone : la suppression s affiche', async () => {
    currentRole = 'owner'
    currentSection = 'danger-zone'
    renderPage()
    expect(await screen.findByText('suppression-espace')).toBeInTheDocument()
  })

  it('simple membre : integrations en lecture, pas de zone de danger', async () => {
    currentRole = 'member'
    renderPage()
    expect(await screen.findByText('integrations-lecture')).toBeInTheDocument()
    expect(screen.queryByText('Danger Zone')).not.toBeInTheDocument()
  })
})
