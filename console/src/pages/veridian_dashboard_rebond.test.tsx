import { beforeEach, describe, expect, it, vi } from 'vitest'
import { render, screen } from '@testing-library/react'
import { i18n } from '@lingui/core'
import { I18nProvider } from '@lingui/react'

const s = vi.hoisted(() => ({ search: {} as Record<string, unknown>, navigate: vi.fn(), user: { id: 'u1', email: 'a@x.test' } }))
vi.mock('@tanstack/react-router', () => ({ useNavigate: () => s.navigate, useSearch: () => s.search }))
vi.mock('../contexts/AuthContext', () => ({
  useAuth: () => ({ user: s.user, workspaces: [{ id: 'a-moi', name: 'À moi', settings: {} }, { id: 'client', name: 'Client', settings: {} }] })
}))
vi.mock('../services/api/auth', () => ({ isRootUser: () => false }))
vi.mock('../layouts/MainLayout', () => ({
  MainLayout: ({ children }: { children: React.ReactNode }) => <div>{children}</div>,
  MainLayoutSidebar: ({ children }: { children: React.ReactNode }) => <div>{children}</div>
}))
vi.mock('../components/settings/SystemSettingsDrawer', () => ({ SystemSettingsDrawer: () => null }))

import { recallSpace, rememberSpace } from '../utils/veridian_last_space'
import { DashboardPage } from './DashboardPage'

i18n.loadAndActivate({ locale: 'en', messages: {} })
const open = () =>
  render(
    <I18nProvider i18n={i18n}>
      <DashboardPage />
    </I18nProvider>
  )

describe('retour du Hub sans espace demandé', () => {
  beforeEach(() => {
    localStorage.clear()
    s.navigate.mockClear()
    s.search = { rebond: 1 }
  })

  it('rien de choisi ici : la personne choisit, aucun espace ne s’ouvre à sa place', () => {
    open()
    expect(screen.getByText('Client')).toBeInTheDocument()
    expect(s.navigate).not.toHaveBeenCalled()
  })

  it('le dernier espace ouvert se rouvre', () => {
    rememberSpace('u1', 'a-moi')
    open()
    expect(s.navigate).toHaveBeenCalledWith({ to: '/console/workspace/$workspaceId', params: { workspaceId: 'a-moi' }, replace: true })
  })

  it('espace d’une autre personne, ou quitté depuis : on demande', () => {
    rememberSpace('u2', 'a-moi')
    open()
    rememberSpace('u1', 'parti')
    open()
    expect(s.navigate).not.toHaveBeenCalled()
  })

  it('arrivée voulue sur le choix d’espace (sans rebond) : on ne renvoie pas', () => {
    s.search = {}
    rememberSpace('u1', 'a-moi')
    open()
    expect(s.navigate).not.toHaveBeenCalled()
    expect(recallSpace('u1')).toBe('a-moi')
  })
})
