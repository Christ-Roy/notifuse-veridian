import { beforeEach, describe, expect, it, vi } from 'vitest'
import { render, screen, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { i18n } from '@lingui/core'
import { I18nProvider } from '@lingui/react'
import { App as AntApp } from 'antd'

import { ListsPage } from './ListsPage'
import { listsApi } from '../services/api/list'

i18n.loadAndActivate({ locale: 'en', messages: {} })
vi.setConfig({ testTimeout: 30000 })

vi.mock('@tanstack/react-router', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@tanstack/react-router')>()
  return { ...actual, useParams: () => ({ workspaceId: 'ws-1' }) }
})
vi.mock('../contexts/AuthContext', () => ({
  useAuth: () => ({ workspaces: [{ id: 'ws-1', name: 'WS', settings: { timezone: 'UTC' } }] }),
  useWorkspacePermissions: () => ({ permissions: { lists: { read: true, write: true } } })
}))
vi.mock('../services/api/list', () => ({ listsApi: { list: vi.fn(), delete: vi.fn() } }))
vi.mock('../services/api/template', () => ({ templatesApi: { get: vi.fn() } }))
vi.mock('../components/lists/ListDrawer', () => ({
  CreateListDrawer: ({ list }: { list?: { id: string } }) => (
    <button type="button">{list ? `edit-${list.id}` : 'create-list'}</button>
  )
}))
vi.mock('../components/lists/ImportContactsToListButton', () => ({
  ImportContactsToListButton: ({ list }: { list: { id: string } }) => (
    <button type="button">import-{list.id}</button>
  )
}))
vi.mock('../components/lists/ListStats', () => ({ ListStats: () => <div /> }))
vi.mock('../components/templates/TemplatePreviewDrawer', () => ({ default: () => null }))
vi.mock('../components/templates/CreateTemplateDrawer', () => ({ CreateTemplateDrawer: () => null }))

const list = (id: string, name: string) => ({
  id,
  name,
  description: `desc ${id}`,
  is_public: false,
  is_double_optin: false
})

const renderPage = async (lists: ReturnType<typeof list>[]) => {
  vi.mocked(listsApi.list).mockResolvedValue({ lists } as never)
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  render(
    <QueryClientProvider client={client}>
      <I18nProvider i18n={i18n}>
        <AntApp>
          <ListsPage />
        </AntApp>
      </I18nProvider>
    </QueryClientProvider>
  )
  await screen.findByText(lists[0].name.split(' / ').pop() as string)
}

const cardTitles = () =>
  Array.from(document.querySelectorAll('.ant-card-head-title')).map((el) => el.textContent)

describe('ListsPage : dossiers', () => {
  beforeEach(() => vi.clearAllMocks())

  it('liste plate : mêmes cartes, même ordre, aucun bouton de dossier, mêmes actions', async () => {
    await renderPage([list('b', 'Zèbre'), list('a', 'Alpha'), list('c', 'Contacts de test')])
    expect(cardTitles()).toEqual(['Zèbre', 'Alpha', 'Contacts de test'])
    expect(document.querySelectorAll('[aria-expanded]')).toHaveLength(0)
    expect(screen.getByRole('button', { name: 'edit-a' })).toBeInTheDocument()
    expect(screen.getByRole('button', { name: 'import-b' })).toBeInTheDocument()
  })

  it('Dossier / Nom : un dossier de 2 listes, noms courts, actions conservées', async () => {
    await renderPage([
      list('a', '1 Prospection / Séquence froide'),
      list('b', '1 Prospection / Relance'),
      list('c', '2 Clients / Exclus')
    ])
    const folders = Array.from(document.querySelectorAll('button[aria-expanded]'))
    expect(folders).toHaveLength(2)
    expect(folders[0]).toHaveTextContent('1 Prospection')
    expect(folders[0]).toHaveTextContent('2 items')
    expect(cardTitles()).toEqual(['Relance', 'Séquence froide', 'Exclus'])
    expect(screen.getByRole('button', { name: 'edit-a' })).toBeInTheDocument()
  })

  it('un dossier se plie au clavier et masque ses cartes', async () => {
    const user = userEvent.setup({ delay: null })
    await renderPage([list('a', 'D / A'), list('b', 'D / B'), list('c', 'Sans dossier')])
    const folder = document.querySelector('button[aria-expanded]') as HTMLElement
    expect(folder).toHaveAttribute('aria-expanded', 'true')
    folder.focus()
    await user.keyboard('{Enter}')
    expect(folder).toHaveAttribute('aria-expanded', 'false')
    expect(cardTitles()).toEqual(['Sans dossier'])
    await user.keyboard(' ')
    expect(cardTitles()).toEqual(['A', 'B', 'Sans dossier'])
    expect(within(document.body).queryAllByRole('button', { name: /^edit-/ })).toHaveLength(3)
  })
})
