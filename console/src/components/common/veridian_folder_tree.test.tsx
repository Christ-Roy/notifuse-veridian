import { describe, expect, it } from 'vitest'
import { render, screen } from '@testing-library/react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import userEvent from '@testing-library/user-event'
import { i18n } from '@lingui/core'
import { I18nProvider } from '@lingui/react'
import { Table } from 'antd'
import type { TableColumnType } from 'antd'

import { FolderToggleButton } from './veridian_folder_tree'
import { useFolderRows } from './veridian_folder_state'
import { folderTableColumns } from './veridian_folder_columns'
import type { TreeRow } from './veridian_folder_rows'

i18n.loadAndActivate({ locale: 'en', messages: {} })

interface Row {
  id: string
  name: string
  subject: string
}
const item = (id: string, name: string): Row => ({ id, name, subject: `Sujet ${id}` })

function Harness({ items }: { items: Row[] }) {
  const { rows, toggle, nested } = useFolderRows(items)
  const columns: TableColumnType<Row>[] = [
    { title: 'Template', dataIndex: 'name', key: 'name' },
    { title: 'Subject', dataIndex: 'subject', key: 'subject' }
  ]
  if (!nested) {
    return <Table columns={columns} dataSource={items} rowKey="id" pagination={false} />
  }
  return (
    <Table<TreeRow<Row>>
      columns={folderTableColumns(columns, 'name', toggle)}
      dataSource={rows}
      rowKey="key"
      pagination={false}
    />
  )
}

const renderHarness = (items: Row[]) =>
  render(
    <QueryClientProvider client={new QueryClient({ defaultOptions: { queries: { retry: false } } })}>
      <I18nProvider i18n={i18n}>
        <Harness items={items} />
      </I18nProvider>
    </QueryClientProvider>
  )

describe('FolderToggleButton', () => {
  it('est un bouton natif avec aria-expanded, compte au singulier et au pluriel', () => {
    const base = { kind: 'folder' as const, key: 'k', label: 'Dossier', depth: 0 }
    const { rerender } = render(
      <I18nProvider i18n={i18n}>
        <FolderToggleButton row={{ ...base, count: 1, open: true }} onToggle={() => {}} />
      </I18nProvider>
    )
    const button = screen.getByRole('button', { name: /Dossier/ })
    expect(button.tagName).toBe('BUTTON')
    expect(button).toHaveAttribute('aria-expanded', 'true')
    expect(button).toHaveTextContent('1 item')
    rerender(
      <I18nProvider i18n={i18n}>
        <FolderToggleButton row={{ ...base, count: 3, open: false }} onToggle={() => {}} />
      </I18nProvider>
    )
    expect(screen.getByRole('button', { name: /Dossier/ })).toHaveAttribute('aria-expanded', 'false')
    expect(screen.getByRole('button', { name: /Dossier/ })).toHaveTextContent('3 items')
  })
})

describe('arbre de dossiers dans un tableau', () => {
  it('liste plate : aucun bouton de dossier, mêmes lignes dans le même ordre', () => {
    renderHarness([item('b', 'Zèbre'), item('a', 'Alpha')])
    expect(screen.queryAllByRole('button')).toHaveLength(0)
    const cells = screen.getAllByRole('row').slice(1)
    expect(cells).toHaveLength(2)
    expect(cells[0]).toHaveTextContent('Zèbre')
    expect(cells[1]).toHaveTextContent('Alpha')
  })

  it('Dossier / Nom : une ligne de dossier avec 2 éléments, nom court et colonnes conservées', () => {
    renderHarness([item('a', '1 Prospection / Séquence'), item('b', '1 Prospection / Relance')])
    const buttons = screen.getAllByRole('button')
    expect(buttons).toHaveLength(1)
    expect(buttons[0]).toHaveTextContent('1 Prospection')
    expect(buttons[0]).toHaveTextContent('2 items')
    const rows = screen.getAllByRole('row').slice(1)
    expect(rows).toHaveLength(3)
    expect(rows[1]).toHaveTextContent('Relance')
    expect(rows[1]).not.toHaveTextContent('1 Prospection')
    expect(rows[1]).toHaveTextContent('Sujet b')
  })

  it('se plie et se déplie au clic', async () => {
    const user = userEvent.setup({ delay: null })
    renderHarness([item('a', 'D / A'), item('b', 'D / B')])
    const button = screen.getByRole('button', { name: /D/ })
    expect(button).toHaveAttribute('aria-expanded', 'true')
    await user.click(button)
    expect(button).toHaveAttribute('aria-expanded', 'false')
    expect(screen.getAllByRole('row').slice(1)).toHaveLength(1)
    await user.click(button)
    expect(screen.getAllByRole('row').slice(1)).toHaveLength(3)
  })

  it('se plie et se déplie au clavier : Entrée et Espace', async () => {
    const user = userEvent.setup({ delay: null })
    renderHarness([item('a', 'D / A')])
    const button = screen.getByRole('button', { name: /D/ })
    button.focus()
    await user.keyboard('{Enter}')
    expect(button).toHaveAttribute('aria-expanded', 'false')
    await user.keyboard(' ')
    expect(button).toHaveAttribute('aria-expanded', 'true')
  })
})
