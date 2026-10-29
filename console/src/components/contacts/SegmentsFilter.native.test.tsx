import { beforeEach, describe, expect, it, vi } from 'vitest'
import { fireEvent, render, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { i18n } from '@lingui/core'
import { I18nProvider } from '@lingui/react'
import { App as AntApp } from 'antd'

import { SegmentsFilter } from './SegmentsFilter'
import type { Segment } from '../../services/api/segment'
import type { NativeTree } from '../../services/api/veridian_folders'

i18n.loadAndActivate({ locale: 'en', messages: {} })
vi.setConfig({ testTimeout: 30000 })

vi.mock('../segment/button_upsert', () => ({ default: () => <button type="button">New segment</button> }))
vi.mock('../../services/api/task', () => ({ taskApi: { findBySegmentId: vi.fn() } }))

const api = vi.hoisted(() => ({
  list: vi.fn(),
  create: vi.fn(),
  rename: vi.fn(),
  move: vi.fn(),
  remove: vi.fn(),
  moveItems: vi.fn()
}))
vi.mock('../../services/api/veridian_folders', () => ({ foldersApi: api }))

const seg = (id: string, name: string): Segment =>
  ({ id, name, color: 'blue', status: 'active', users_count: 1 }) as unknown as Segment

const folder = (id: string, name: string, parent: string | null, position: number) => ({
  id, parent_id: parent, name, position, depth: 1, path: [name], path_text: name, count: 0, direct_items: 0, subfolders: 0
})
const placed = (id: string, name: string, label: string, folderId: string | null, position: number | null) => ({
  id, name, label, folder_id: folderId, position, path_text: ''
})

// Ordre explicite du serveur : Zèbres avant Alpha, P2 avant P1. Un dossier vide.
const SEGMENTS = [seg('p1', 'Alpha / P1'), seg('p2', 'Alpha / P2'), seg('z', 'Zèbres / Z'), seg('r', 'Libre')]
const NATIVE: NativeTree = {
  kind: 'segments',
  folders: [folder('fz', 'Zèbres', null, 0), folder('fa', 'Alpha', null, 1), folder('fe', 'Vide', null, 2)],
  items: [
    placed('p2', 'Alpha / P2', 'P2', 'fa', 0),
    placed('p1', 'Alpha / P1', 'P1', 'fa', 1),
    placed('z', 'Zèbres / Z', 'Z', 'fz', 0),
    placed('r', 'Libre', 'Libre', null, null)
  ]
}

const renderFilter = (selected: string[] = [], segments = SEGMENTS) => {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  return render(
    <QueryClientProvider client={client}>
      <I18nProvider i18n={i18n}>
        <AntApp>
          <SegmentsFilter workspaceId="ws" segments={segments} selectedSegmentIds={selected} onSegmentToggle={vi.fn()} />
        </AntApp>
      </I18nProvider>
    </QueryClientProvider>
  )
}

// Un dossier = son enveloppe `data-folder-id` (bouton + menu). On le retrouve par son libellé exact.
const folderEl = (label: string) => {
  const el = Array.from(document.querySelectorAll<HTMLElement>('[data-folder-id]')).find((e) =>
    e.querySelector('button[aria-expanded]')?.textContent?.startsWith(label)
  )
  if (!el) throw new Error(`pas de dossier ${label}`)
  return el
}
const folderBtn = (label: string) => folderEl(label).querySelector('button[aria-expanded]') as HTMLElement
// Attend la lecture serveur : avant, l'arbre se déduit des noms (clés de pli différentes).
const loaded = () => waitFor(() => expect(folderEl('Vide')).toBeInTheDocument())
const wrapperOf = (text: string) => screen.getByText(text).closest('[draggable="true"]') as HTMLElement
const dt = () => ({ setData: vi.fn(), effectAllowed: '', dropEffect: '' })

beforeEach(() => {
  window.localStorage.clear()
  Object.values(api).forEach((f) => f.mockReset())
  api.list.mockResolvedValue(NATIVE)
  api.moveItems.mockResolvedValue({})
  api.move.mockResolvedValue({})
})

describe('segments : dossiers natifs', () => {
  it("affiche les dossiers dans l'ordre du serveur, avec effectif, le dossier vide atténué", async () => {
    renderFilter()
    await loaded()
    const labels = Array.from(document.querySelectorAll('[data-folder-id] button[aria-expanded]')).map((b) => b.textContent)
    expect(labels).toEqual(['Zèbres1', 'Alpha2', 'Vide0'])
    expect(folderEl('Vide').style.opacity).toBe('0.55')
    expect(folderEl('Alpha').style.opacity).toBe('1')
    expect(api.list).toHaveBeenCalledWith('ws', 'segments')
  })

  it("ouvre un dossier au clic et montre ses segments dans l'ordre du serveur (P2 avant P1)", async () => {
    renderFilter()
    await loaded()
    await userEvent.click(folderBtn('Alpha'))
    const p2 = screen.getByText('P2')
    const p1 = screen.getByText('P1')
    expect(p2.compareDocumentPosition(p1) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy()
  })

  it('mémorise le pli : fermé ou ouvert, il le reste au rechargement', async () => {
    const first = renderFilter()
    await loaded()
    await userEvent.click(folderBtn('Alpha'))
    expect(folderBtn('Alpha')).toHaveAttribute('aria-expanded', 'true')
    first.unmount()
    renderFilter()
    await loaded()
    expect(folderBtn('Alpha')).toHaveAttribute('aria-expanded', 'true')
    expect(folderBtn('Zèbres')).toHaveAttribute('aria-expanded', 'false')
  })

  it('la recherche ouvre les dossiers qui contiennent un résultat et cache les autres', async () => {
    renderFilter()
    await loaded()
    await userEvent.type(screen.getByRole('textbox', { name: 'Search' }), 'p2')
    expect(screen.getByText('P2')).toBeInTheDocument()
    expect(screen.queryByText('P1')).toBeNull()
    expect(document.querySelector('[data-folder-id]')).not.toBeNull()
    expect(() => folderEl('Zèbres')).toThrow()
  })

  it('glisser un segment sur un dossier le range dedans (appel serveur)', async () => {
    renderFilter()
    await loaded()
    await userEvent.click(folderBtn('Zèbres'))
    const src = wrapperOf('Z')
    const target = folderEl('Alpha')
    fireEvent.dragStart(src, { dataTransfer: dt() })
    fireEvent.dragOver(target, { dataTransfer: dt() })
    fireEvent.drop(target, { dataTransfer: dt() })
    await waitFor(() => expect(api.moveItems).toHaveBeenCalledWith('ws', 'segments', ['z'], 'fa', {}))
  })

  it('un dépôt sans sens (segment sur son propre dossier) ne fait aucun appel', async () => {
    renderFilter()
    await loaded()
    await userEvent.click(folderBtn('Alpha'))
    const src = wrapperOf('P1')
    const target = folderEl('Alpha')
    fireEvent.dragStart(src, { dataTransfer: dt() })
    fireEvent.dragOver(target, { dataTransfer: dt() })
    fireEvent.drop(target, { dataTransfer: dt() })
    expect(api.moveItems).not.toHaveBeenCalled()
  })

  it('clavier : Alt+↓ sur un segment le descend d un cran', async () => {
    renderFilter()
    await loaded()
    await userEvent.click(folderBtn('Alpha'))
    fireEvent.keyDown(within(wrapperOf('P2')).getAllByRole('button')[0], { key: 'ArrowDown', altKey: true })
    // P2 (rang 0) descend : en queue du dossier (il n'y a que deux frères).
    await waitFor(() => expect(api.moveItems).toHaveBeenCalledWith('ws', 'segments', ['p2'], 'fa', {}))
  })

  it('clavier : « m » ouvre « Déplacer vers… », flèches et Entrée choisissent le dossier', async () => {
    renderFilter()
    await loaded()
    await userEvent.click(folderBtn('Zèbres'))
    fireEvent.keyDown(within(wrapperOf('Z')).getAllByRole('button')[0], { key: 'm' })
    const dialog = await screen.findByRole('dialog')
    const tree = within(dialog).getByRole('tree')
    // Au clavier seulement : flèche bas jusqu'à « Alpha » (racine, Zèbres, Alpha), puis Entrée.
    const focusable = (tree.parentElement as HTMLElement).querySelector('input') as HTMLElement
    fireEvent.keyDown(focusable, { key: 'ArrowDown', keyCode: 40 })
    fireEvent.keyDown(focusable, { key: 'ArrowDown', keyCode: 40 })
    fireEvent.keyDown(focusable, { key: 'ArrowDown', keyCode: 40 })
    fireEvent.keyDown(focusable, { key: 'Enter', keyCode: 13 })
    await waitFor(() => expect(api.moveItems).toHaveBeenCalledWith('ws', 'segments', ['z'], 'fa', {}))
  })

  it('taper « m » dans un champ ouvert depuis un segment (tiroir, fenêtre) ne déclenche rien', async () => {
    renderFilter()
    await loaded()
    await userEvent.click(folderBtn('Alpha'))
    const wrapper = wrapperOf('P1')
    const field = document.createElement('input')
    wrapper.appendChild(field)
    fireEvent.keyDown(field, { key: 'm' })
    fireEvent.keyDown(field, { key: 'ArrowDown', altKey: true })
    expect(screen.queryByRole('dialog')).toBeNull()
    expect(api.moveItems).not.toHaveBeenCalled()
    // Un événement venu d'un portail (hors du DOM de l'élément) est ignoré aussi.
    const outside = document.createElement('input')
    document.body.appendChild(outside)
    fireEvent.keyDown(outside, { key: 'm' })
    expect(screen.queryByRole('dialog')).toBeNull()
  })

  it('l API des dossiers en panne : retombe sur les noms, rien ne disparaît', async () => {
    api.list.mockRejectedValue(new Error('boom'))
    renderFilter()
    await waitFor(() => expect(screen.getByText('Alpha')).toBeInTheDocument())
    expect(screen.getByText('Zèbres')).toBeInTheDocument()
    expect(screen.getByText('Libre')).toBeInTheDocument()
    // Le pli marche aussi sans identifiants serveur (clé du dossier).
    await userEvent.click(screen.getByText('Alpha'))
    expect(screen.getByText('P1')).toBeInTheDocument()
  })

  it('un workspace sans dossier garde la rangée plate et propose « New folder »', async () => {
    api.list.mockResolvedValue({ kind: 'segments', folders: [], items: [] })
    renderFilter([], [seg('a', 'Zèbre'), seg('b', 'Alpha')])
    await waitFor(() => expect(screen.getByRole('button', { name: /New folder/ })).toBeInTheDocument())
    expect(screen.queryByRole('textbox', { name: 'Search' })).toBeNull()
  })
})
