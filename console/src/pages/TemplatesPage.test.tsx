import { beforeEach, describe, expect, it, vi } from 'vitest'
import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { i18n } from '@lingui/core'
import { I18nProvider } from '@lingui/react'
import { App as AntApp } from 'antd'

import { TemplatesPage } from './TemplatesPage'
import { templatesApi } from '../services/api/template'

i18n.loadAndActivate({ locale: 'en', messages: {} })
vi.setConfig({ testTimeout: 30000 })

const searchState = vi.hoisted(() => ({}))
const navigateFn = vi.hoisted(() => () => undefined)
vi.mock('@tanstack/react-router', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@tanstack/react-router')>()
  return {
    ...actual,
    useParams: () => ({ workspaceId: 'ws-1' }),
    useSearch: () => searchState,
    useNavigate: () => navigateFn
  }
})
// Références stables : la page a un effet qui dépend de `workspaces`.
const authState = vi.hoisted(() => ({
  workspaces: [{ id: 'ws-1', name: 'WS', settings: { timezone: 'UTC' }, integrations: [] }],
  permissions: { templates: { read: true, write: true }, contacts: { write: true } }
}))
vi.mock('../contexts/AuthContext', () => ({
  useAuth: () => ({ workspaces: authState.workspaces }),
  useWorkspacePermissions: () => ({ permissions: authState.permissions })
}))
vi.mock('../services/api/template', () => ({ templatesApi: { list: vi.fn(), delete: vi.fn() } }))
vi.mock('../components/templates/CreateTemplateDrawer', () => ({
  CreateTemplateDrawer: ({ template, fromTemplate }: { template?: { id: string }; fromTemplate?: { id: string } }) => (
    <button type="button">
      {template ? `edit-${template.id}` : fromTemplate ? `clone-${fromTemplate.id}` : 'create-template'}
    </button>
  )
}))
vi.mock('../components/templates/TemplatePreviewDrawer', () => ({
  default: ({ children }: { children: React.ReactNode }) => <>{children}</>
}))
vi.mock('../components/templates', () => ({
  renderCategoryTag: (category: string) => <span>{category}</span>
}))
vi.mock('../components/templates/SendTemplateModal', () => ({ default: () => null }))

const tpl = (id: string, name: string) => ({
  id,
  name,
  category: 'marketing',
  created_at: '2026-01-01T00:00:00Z',
  email: { subject: `Sujet ${id}` }
})

const renderPage = async (templates: ReturnType<typeof tpl>[]) => {
  vi.mocked(templatesApi.list).mockResolvedValue({ templates } as never)
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  render(
    <QueryClientProvider client={client}>
      <I18nProvider i18n={i18n}>
        <AntApp>
          <TemplatesPage />
        </AntApp>
      </I18nProvider>
    </QueryClientProvider>
  )
  await screen.findByText(templates[0].name.split(' / ').pop() as string)
}

const bodyRows = () => document.querySelectorAll('tbody tr.ant-table-row, tbody tr[data-row-key]')

describe('TemplatesPage : dossiers', () => {
  beforeEach(() => vi.clearAllMocks())

  it('liste plate : mêmes lignes, mêmes actions, aucun dossier, pagination d origine', async () => {
    const many = Array.from({ length: 12 }, (_, i) => tpl(`t${i}`, `Modèle ${i}`))
    await renderPage(many)
    expect(document.querySelectorAll('button[aria-expanded]')).toHaveLength(0)
    expect(bodyRows()).toHaveLength(10)
    expect(document.querySelector('.ant-pagination')).not.toBeNull()
    expect(screen.getByRole('button', { name: 'edit-t0' })).toBeInTheDocument()
    expect(screen.getByRole('button', { name: 'clone-t0' })).toBeInTheDocument()
  })

  it('Dossier / Nom : un dossier de 2 modèles, nom court, actions conservées', async () => {
    await renderPage([tpl('a', '1 Cold / Mail 1'), tpl('b', '1 Cold / Mail 2'), tpl('c', 'Libre')])
    const folder = document.querySelector('button[aria-expanded]') as HTMLElement
    expect(folder).toHaveTextContent('1 Cold')
    expect(folder).toHaveTextContent('2 items')
    expect(bodyRows()).toHaveLength(4)
    expect(screen.getByText('Mail 1')).toBeInTheDocument()
    expect(screen.queryByText('1 Cold / Mail 1')).not.toBeInTheDocument()
    expect(screen.getByRole('button', { name: 'edit-a' })).toBeInTheDocument()
    expect(document.querySelector('.ant-pagination')).toBeNull()
  })

  it('un dossier se plie et se déplie au clavier', async () => {
    const user = userEvent.setup({ delay: null })
    await renderPage([tpl('a', 'D / A'), tpl('b', 'D / B')])
    const folder = document.querySelector('button[aria-expanded]') as HTMLElement
    folder.focus()
    await user.keyboard('{Enter}')
    expect(folder).toHaveAttribute('aria-expanded', 'false')
    expect(bodyRows()).toHaveLength(1)
    await user.keyboard(' ')
    expect(folder).toHaveAttribute('aria-expanded', 'true')
    expect(bodyRows()).toHaveLength(3)
  })
})
