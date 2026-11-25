import { beforeEach, describe, expect, it, vi } from 'vitest'
import { render } from '@testing-library/react'
import { i18n } from '@lingui/core'
import { I18nProvider } from '@lingui/react'

const s = vi.hoisted(() => ({ ws: 'a-moi', user: { id: 'u1', email: 'a@x.test' } as { id: string; email: string } | null }))
vi.mock('@tanstack/react-router', () => ({
  Outlet: () => null,
  Link: ({ children }: { children?: React.ReactNode }) => <a>{children}</a>,
  useParams: () => ({ workspaceId: s.ws }),
  useMatches: () => [{ pathname: '/console/workspace/x' }],
  useNavigate: () => vi.fn()
}))
vi.mock('../contexts/AuthContext', () => ({
  useAuth: () => ({
    signout: vi.fn(),
    refreshWorkspaces: vi.fn(),
    user: s.user,
    workspaces: [{ id: 'a-moi', name: 'À moi', settings: {} }]
  })
}))
vi.mock('../services/api/auth', () => ({ isRootUser: () => true }))
vi.mock('../services/api/workspace', () => ({ workspaceService: { getMembers: vi.fn() } }))
vi.mock('../components/LanguageSwitcher', () => ({ LanguageSwitcher: () => null }))
vi.mock('../components/contacts/ContactsCsvUploadProvider', () => ({ ContactsCsvUploadProvider: ({ children }: { children?: React.ReactNode }) => <>{children}</> }))
vi.mock('../components/file_manager/context', () => ({ FileManagerProvider: ({ children }: { children?: React.ReactNode }) => <>{children}</> }))
vi.mock('../components/mail/veridian_mail_menu_item', () => ({ veridianInboxMenuItem: () => null }))
vi.mock('../components/veridian_organisation', () => ({ VeridianBrandProvider: ({ children }: { children?: React.ReactNode }) => <>{children}</> }))
vi.mock('../components/veridian_brand_header_link', () => ({ VeridianBrandHeaderLink: () => null }))
vi.mock('../components/veridian_connect_ai', () => ({ VeridianConnectAI: () => null }))
vi.mock('../components/veridian_brand_footer', () => ({ VeridianBrandFooter: () => null }))
vi.mock('../components/veridian_soft_delete_banner', () => ({ VeridianSoftDeleteBanner: () => null }))
vi.mock('../components/veridian_logo', () => ({ VeridianLogo: () => null }))
vi.mock('../components/veridian_a11y', () => ({ useVeridianDocumentTitle: () => {}, useVeridianAccessibleNames: () => {} }))

import { recallSpace } from '../utils/veridian_last_space'
import { WorkspaceLayout } from './WorkspaceLayout'

i18n.loadAndActivate({ locale: 'en', messages: {} })
const open = () =>
  render(
    <I18nProvider i18n={i18n}>
      <WorkspaceLayout />
    </I18nProvider>
  )

describe('WorkspaceLayout mémorise le dernier espace ouvert', () => {
  beforeEach(() => {
    localStorage.clear()
    s.ws = 'a-moi'
    s.user = { id: 'u1', email: 'a@x.test' }
    Object.defineProperty(window, 'matchMedia', {
      writable: true,
      configurable: true,
      value: (q: string) => ({ matches: false, media: q, addEventListener() {}, removeEventListener() {}, addListener() {}, removeListener() {}, dispatchEvent: () => false, onchange: null })
    })
  })

  it('ouvrir un espace de la liste le mémorise pour cette personne', () => {
    open()
    expect(recallSpace('u1')).toBe('a-moi')
  })

  it('un espace absent de la liste de la personne n’est pas mémorisé', () => {
    s.ws = 'pas-a-moi'
    open()
    expect(recallSpace('u1')).toBeUndefined()
  })
})
