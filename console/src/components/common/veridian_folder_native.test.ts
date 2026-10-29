import { describe, expect, it } from 'vitest'
import { buildNativeTree, flattenFolders, itemsSignature, labelFor, withoutEmptyFolders } from './veridian_folder_native'
import type { NativeFolder, NativeItem, NativeTree } from '../../services/api/veridian_folders'
import type { SegmentTreeNode } from '../segment/veridian_segment_folders'

type S = { id: string; name: string }
const seg = (id: string, name: string): S => ({ id, name })

const folder = (id: string, name: string, parent: string | null, position: number, path: string[]): NativeFolder => ({
  id,
  parent_id: parent,
  name,
  position,
  depth: path.length,
  path,
  path_text: path.join(' / '),
  count: 0,
  direct_items: 0,
  subfolders: 0
})
const placed = (id: string, name: string, label: string, folderId: string | null, position: number | null): NativeItem => ({
  id,
  name,
  label,
  folder_id: folderId,
  position,
  path_text: ''
})

const shape = (nodes: SegmentTreeNode<S>[]): unknown[] =>
  nodes.map((n) => (n.kind === 'folder' ? { [n.label]: shape(n.children), count: n.count } : n.label))

describe('labelFor', () => {
  it('retire le chemin quand le nom le porte, sinon garde le nom entier', () => {
    expect(labelFor('A / B / C', ['A', 'B'])).toBe('C')
    expect(labelFor('a  /  b  /  C', ['A', 'B'])).toBe('C')
    expect(labelFor('Autre / C', ['A', 'B'])).toBe('Autre / C')
    expect(labelFor('Seul', [])).toBe('Seul')
  })
})

describe('buildNativeTree', () => {
  const native: NativeTree = {
    kind: 'segments',
    folders: [
      folder('f2', 'Zèbres', null, 1, ['Zèbres']),
      folder('f1', 'Alpha', null, 0, ['Alpha']),
      folder('f3', 'Sous', 'f1', 0, ['Alpha', 'Sous']),
      folder('f4', 'Vide', null, 2, ['Vide'])
    ],
    items: [
      placed('a', 'Alpha / Sous / Deux', 'Deux', 'f3', 1),
      placed('b', 'Alpha / Sous / Un', 'Un', 'f3', 0),
      placed('c', 'Zèbres / Z', 'Z', 'f2', 0),
      placed('r', 'Racine', 'Racine', null, null)
    ]
  }
  const items = [seg('r', 'Racine'), seg('a', 'Alpha / Sous / Deux'), seg('b', 'Alpha / Sous / Un'), seg('c', 'Zèbres / Z')]

  it("suit l'ordre explicite du serveur, pas l'ordre alphabétique ni celui de réception", () => {
    const tree = buildNativeTree(items, native)
    expect(shape(tree)).toEqual([
      { Alpha: [{ Sous: ['Un', 'Deux'], count: 2 }], count: 2 },
      { Zèbres: ['Z'], count: 1 },
      { Vide: [], count: 0 },
      'Racine'
    ])
  })

  it('garde un dossier vide (affiché atténué), compte récursivement, et porte l identifiant serveur', () => {
    const tree = buildNativeTree(items, native)
    const vide = tree.find((n) => n.kind === 'folder' && n.label === 'Vide')
    expect(vide && vide.kind === 'folder' && vide.folderId).toBe('f4')
    expect(vide && vide.kind === 'folder' && vide.count).toBe(0)
    expect(flattenFolders(tree).map((f) => [f.id, f.depth, f.parentId])).toEqual([
      ['f1', 0, null],
      ['f3', 1, 'f1'],
      ['f2', 0, null],
      ['f4', 0, null]
    ])
  })

  it('un objet non encore rangé côté serveur (nom à chemin) rejoint le dossier du même chemin', () => {
    const tree = buildNativeTree([...items, seg('new', 'Zèbres / Neuf')], native)
    const z = tree.find((n) => n.kind === 'folder' && n.label === 'Zèbres')
    expect(z && z.kind === 'folder' && z.children.map((c) => c.label)).toEqual(['Z', 'Neuf'])
  })

  it('un nom relu avant le serveur (ancien chemin) garde le libellé serveur', () => {
    const stale = buildNativeTree([seg('c', 'Ancien / Z')], {
      ...native,
      items: [placed('c', 'Zèbres / Z', 'Z', 'f2', 0)]
    })
    const z = stale.find((n) => n.kind === 'folder' && n.label === 'Zèbres')
    expect(z && z.kind === 'folder' && z.children[0].label).toBe('Z')
  })

  it('un objet renommé sans chemin prend son nouveau nom comme libellé', () => {
    const tree = buildNativeTree([seg('c', 'Renommé')], { ...native, items: [placed('c', 'Zèbres / Z', 'Z', 'f2', 0)] })
    const z = tree.find((n) => n.kind === 'folder' && n.label === 'Zèbres')
    expect(z && z.kind === 'folder' && z.children[0].label).toBe('Renommé')
  })

  it('sans données serveur, retombe sur les noms (même arbre qu avant)', () => {
    const tree = buildNativeTree(items, undefined)
    expect(shape(tree)).toEqual([{ Alpha: [{ Sous: ['Deux', 'Un'], count: 2 }], count: 2 }, { Zèbres: ['Z'], count: 1 }, 'Racine'])
  })

  it('les objets à la racine gardent l ordre reçu tant qu aucun rang n est posé', () => {
    const t = buildNativeTree([seg('z', 'Zz'), seg('a', 'Aa')], { kind: 'segments', folders: [], items: [] })
    expect(t.map((n) => n.kind === 'segment' && n.label)).toEqual(['Zz', 'Aa'])
  })

  it('withoutEmptyFolders retire les dossiers sans objet, récursivement', () => {
    const tree = withoutEmptyFolders(buildNativeTree(items, native))
    expect(shape(tree)).toEqual([{ Alpha: [{ Sous: ['Un', 'Deux'], count: 2 }], count: 2 }, { Zèbres: ['Z'], count: 1 }, 'Racine'])
  })
})

describe('itemsSignature', () => {
  it('change quand un objet apparaît, disparaît ou est renommé, pas sinon', () => {
    const base = [seg('a', 'A'), seg('b', 'B')]
    expect(itemsSignature(base)).toBe(itemsSignature([seg('a', 'A'), seg('b', 'B')]))
    expect(itemsSignature(base)).not.toBe(itemsSignature([seg('a', 'A')]))
    expect(itemsSignature(base)).not.toBe(itemsSignature([seg('a', 'A'), seg('b', 'B2')]))
    expect(itemsSignature(base)).not.toBe(itemsSignature([seg('a', 'A'), seg('c', 'B')]))
    expect(itemsSignature(undefined)).toBe('none')
  })
})
