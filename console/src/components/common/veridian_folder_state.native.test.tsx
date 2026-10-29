import { beforeEach, describe, expect, it, vi } from 'vitest'
import { fireEvent, render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { i18n } from '@lingui/core'
import { I18nProvider } from '@lingui/react'
import { App as AntApp, Table } from 'antd'
import type { TableColumnType } from 'antd'

import { useFolderRows } from './veridian_folder_state'
import { folderTableColumns } from './veridian_folder_columns'
import type { TreeRow } from './veridian_folder_rows'
import type { NativeTree } from '../../services/api/veridian_folders'

i18n.loadAndActivate({ locale: 'en', messages: {} })
vi.setConfig({ testTimeout: 30000 })

const api = vi.hoisted(() => ({ list: vi.fn(), create: vi.fn(), rename: vi.fn(), move: vi.fn(), remove: vi.fn(), moveItems: vi.fn() }))
vi.mock('../../services/api/veridian_folders', () => ({ foldersApi: api }))

interface Row { id: string; name: string }
const ITEMS: Row[] = [
  { id: 't1', name: 'Mails / Bienvenue' },
  { id: 't2', name: 'Mails / Relance' },
  { id: 't3', name: 'Libre' }
]
const f = (id: string, name: string, position: number) => ({
  id, parent_id: null, name, position, depth: 1, path: [name], path_text: name, count: 0, direct_items: 0, subfolders: 0
})
const p = (id: string, name: string, label: string, folder: string | null, position: number | null) => ({
  id, name, label, folder_id: folder, position, path_text: ''
})
const NATIVE: NativeTree = {
  kind: 'templates',
  folders: [f('fm', 'Mails', 0), f('fv', 'Archive', 1)],
  items: [p('t2', 'Mails / Relance', 'Relance', 'fm', 0), p('t1', 'Mails / Bienvenue', 'Bienvenue', 'fm', 1), p('t3', 'Libre', 'Libre', null, null)]
}

function Harness({ items, canWrite = true }: { items: Row[]; canWrite?: boolean }) {
  const { rows, toggle, nested, folders, rowProps, folderExtra } = useFolderRows(items, { workspaceId: 'ws', kind: 'templates', canWrite })
  const columns: TableColumnType<Row>[] = [{ title: 'Template', dataIndex: 'name', key: 'name' }]
  return (
    <div>
      {folders.toolbar()}
      {folders.dialogs}
      {nested ? (
        <Table<TreeRow<Row>>
          columns={folderTableColumns(columns, 'name', toggle, folderExtra)}
          dataSource={rows}
          rowKey="key"
          onRow={(row) => rowProps(row)}
          pagination={false}
        />
      ) : (
        <Table columns={columns} dataSource={items} rowKey="id" pagination={false} />
      )}
    </div>
  )
}

const renderHarness = (props: { items: Row[]; canWrite?: boolean }) =>
  render(
    <QueryClientProvider client={new QueryClient({ defaultOptions: { queries: { retry: false } } })}>
      <I18nProvider i18n={i18n}>
        <AntApp>
          <Harness {...props} />
        </AntApp>
      </I18nProvider>
    </QueryClientProvider>
  )

const dt = () => ({ setData: vi.fn(), effectAllowed: '', dropEffect: '' })
const rowOf = (text: string) => screen.getByText(text).closest('tr') as HTMLElement

beforeEach(() => {
  window.localStorage.clear()
  Object.values(api).forEach((m) => m.mockReset())
  api.list.mockResolvedValue(NATIVE)
  api.moveItems.mockResolvedValue({})
})

describe('modèles : tableau en dossiers natifs', () => {
  it("rend l'ordre du serveur (Relance avant Bienvenue), le dossier vide atténué, le libellé court", async () => {
    renderHarness({ items: ITEMS })
    await waitFor(() => expect(screen.getByText('Archive')).toBeInTheDocument())
    const text = (document.querySelector('tbody') as HTMLElement).textContent as string
    expect(text.indexOf('Relance')).toBeLessThan(text.indexOf('Bienvenue'))
    expect(text.indexOf('Mails')).toBeLessThan(text.indexOf('Relance'))
    expect(text.indexOf('Archive')).toBeGreaterThan(text.indexOf('Bienvenue'))
    const archive = rowOf('Archive').querySelector('span[style*="opacity"]') as HTMLElement
    expect(archive.style.opacity).toBe('0.55')
    expect(screen.queryByText('Mails / Relance')).toBeNull()
  })

  it('glisser un modèle sur un dossier le range dedans', async () => {
    renderHarness({ items: ITEMS })
    await waitFor(() => expect(screen.getByText('Archive')).toBeInTheDocument())
    const src = rowOf('Libre')
    const target = rowOf('Archive')
    fireEvent.dragStart(src, { dataTransfer: dt() })
    // milieu de la ligne : « dedans » (le haut de la ligne signifierait « avant ce dossier »)
    fireEvent.dragOver(target, { dataTransfer: dt(), clientY: 0 })
    fireEvent.drop(target, { dataTransfer: dt(), clientY: 0 })
    await waitFor(() => expect(api.moveItems).toHaveBeenCalled())
    expect(api.moveItems.mock.calls[0].slice(0, 4)).toEqual(['ws', 'templates', ['t3'], 'fv'])
  })

  it('sans droit d écriture : ni glisser ni bouton « New folder »', async () => {
    renderHarness({ items: ITEMS, canWrite: false })
    await waitFor(() => expect(screen.getByText('Archive')).toBeInTheDocument())
    expect(rowOf('Libre').getAttribute('draggable')).toBeNull()
    expect(screen.queryByRole('button', { name: /New folder/ })).toBeNull()
    expect(screen.queryByRole('button', { name: /Folder actions/ })).toBeNull()
  })

  it('le menu du dossier renomme et crée via le serveur', async () => {
    api.rename.mockResolvedValue({})
    renderHarness({ items: ITEMS })
    await waitFor(() => expect(screen.getByText('Archive')).toBeInTheDocument())
    await userEvent.click(screen.getAllByRole('button', { name: /Folder actions for Mails/ })[0])
    await userEvent.click(await screen.findByText('Rename'))
    const input = await screen.findByRole('textbox', { name: 'Folder name' })
    await userEvent.clear(input)
    await userEvent.type(input, 'Courriers')
    await userEvent.click(screen.getByRole('button', { name: 'Rename' }))
    await waitFor(() => expect(api.rename).toHaveBeenCalledWith('ws', 'fm', 'Courriers'))
  })

  it('refuse un nom avec « / » entouré d espaces avant tout appel', async () => {
    renderHarness({ items: ITEMS })
    await waitFor(() => expect(screen.getByText('Archive')).toBeInTheDocument())
    await userEvent.click(screen.getByRole('button', { name: /New folder/ }))
    const input = await screen.findByRole('textbox', { name: 'Folder name' })
    await userEvent.type(input, 'A / B')
    expect(await screen.findByRole('alert')).toBeInTheDocument()
    expect(screen.getByRole('button', { name: 'Create' })).toBeDisabled()
    expect(api.create).not.toHaveBeenCalled()
  })
})
