import { describe, expect, it } from 'vitest'
import { buildNativeTree } from './veridian_folder_native'
import { forbiddenDestinations, planDrop, planNudge } from './veridian_folder_moves'
import type { NativeTree } from '../../services/api/veridian_folders'

type S = { id: string; name: string }
const f = (id: string, name: string, parent: string | null, position: number) => ({
  id, parent_id: parent, name, position, depth: 1, path: [name], path_text: name, count: 0, direct_items: 0, subfolders: 0
})
const it_ = (id: string, name: string, folderId: string | null, position: number | null) => ({
  id, name, label: name, folder_id: folderId, position, path_text: ''
})
// A { B { b1, b2 }, a1, a2 }, C {}, root: r1
const native: NativeTree = {
  kind: 'segments',
  folders: [f('A', 'A', null, 0), f('B', 'B', 'A', 0), f('C', 'C', null, 1)],
  items: [it_('b1', 'b1', 'B', 0), it_('b2', 'b2', 'B', 1), it_('a1', 'a1', 'A', 0), it_('a2', 'a2', 'A', 1), it_('r1', 'r1', null, null)]
}
const items: S[] = ['b1', 'b2', 'a1', 'a2', 'r1'].map((id) => ({ id, name: id }))
const tree = buildNativeTree(items, native)

describe('planDrop', () => {
  it('objet sur un dossier : le range dedans, en queue', () => {
    expect(planDrop(tree, { type: 'item', id: 'r1' }, { type: 'folder', id: 'C' })).toEqual({
      op: 'moveItems', ids: ['r1'], folderId: 'C', place: {}
    })
  })
  it('objet sur la racine : le sort de son dossier', () => {
    expect(planDrop(tree, { type: 'item', id: 'a1' }, { type: 'folder', id: null })).toEqual({
      op: 'moveItems', ids: ['a1'], folderId: null, place: {}
    })
  })
  it('objet sur un autre objet : se place avant lui, dans son dossier', () => {
    expect(planDrop(tree, { type: 'item', id: 'r1' }, { type: 'before-item', id: 'b2' })).toEqual({
      op: 'moveItems', ids: ['r1'], folderId: 'B', place: { before_id: 'b2' }
    })
  })
  it('rien à faire : objet déjà dans le dossier visé, ou déposé sur lui-même', () => {
    expect(planDrop(tree, { type: 'item', id: 'a1' }, { type: 'folder', id: 'A' })).toBeNull()
    expect(planDrop(tree, { type: 'item', id: 'a1' }, { type: 'before-item', id: 'a1' })).toBeNull()
    expect(planDrop(tree, { type: 'item', id: 'r1' }, { type: 'folder', id: null })).toBeNull()
  })
  it('dossier : jamais dans lui-même ni dans son sous-arbre', () => {
    expect(planDrop(tree, { type: 'folder', id: 'A' }, { type: 'folder', id: 'A' })).toBeNull()
    expect(planDrop(tree, { type: 'folder', id: 'A' }, { type: 'folder', id: 'B' })).toBeNull()
    expect(planDrop(tree, { type: 'folder', id: 'A' }, { type: 'before-folder', id: 'B' })).toBeNull()
  })
  it('dossier sur un dossier : devient son enfant ; avant un dossier : même parent, avec repère', () => {
    expect(planDrop(tree, { type: 'folder', id: 'C' }, { type: 'folder', id: 'A' })).toEqual({
      op: 'moveFolder', id: 'C', parentId: 'A', place: {}
    })
    expect(planDrop(tree, { type: 'folder', id: 'C' }, { type: 'before-folder', id: 'B' })).toEqual({
      op: 'moveFolder', id: 'C', parentId: 'A', place: { before_id: 'B' }
    })
    expect(planDrop(tree, { type: 'folder', id: 'B' }, { type: 'folder', id: null })).toEqual({
      op: 'moveFolder', id: 'B', parentId: null, place: {}
    })
  })
  it('cibles ou sources inconnues : rien', () => {
    expect(planDrop(tree, { type: 'item', id: 'zz' }, { type: 'folder', id: 'A' })).toBeNull()
    expect(planDrop(tree, { type: 'item', id: 'a1' }, { type: 'folder', id: 'zz' })).toBeNull()
    expect(planDrop(tree, { type: 'folder', id: 'zz' }, { type: 'folder', id: 'A' })).toBeNull()
    expect(planDrop(tree, { type: 'folder', id: 'C' }, { type: 'before-item', id: 'a1' })).toBeNull()
  })
})

describe('planNudge (Alt + flèches)', () => {
  it('monter : avant le frère précédent', () => {
    expect(planNudge(tree, { type: 'item', id: 'a2' }, -1)).toEqual({ op: 'moveItems', ids: ['a2'], folderId: 'A', place: { before_id: 'a1' } })
  })
  it('descendre : avant le frère qui suit le suivant, ou en queue', () => {
    expect(planNudge(tree, { type: 'item', id: 'b1' }, 1)).toEqual({ op: 'moveItems', ids: ['b1'], folderId: 'B', place: {} })
    expect(planNudge(tree, { type: 'folder', id: 'A' }, 1)).toEqual({ op: 'moveFolder', id: 'A', parentId: null, place: {} })
  })
  it('aux extrémités ou sur un inconnu : rien', () => {
    expect(planNudge(tree, { type: 'item', id: 'a1' }, -1)).toBeNull()
    expect(planNudge(tree, { type: 'item', id: 'a2' }, 1)).toBeNull()
    expect(planNudge(tree, { type: 'folder', id: 'A' }, -1)).toBeNull()
    expect(planNudge(tree, { type: 'item', id: 'zz' }, 1)).toBeNull()
  })
  it('descendre sur trois frères passe avant le dernier+1 (repère), pas avant le suivant', () => {
    const t3 = buildNativeTree(['x', 'y', 'z'].map((id) => ({ id, name: id })), {
      kind: 'segments', folders: [], items: [it_('x', 'x', null, 0), it_('y', 'y', null, 1), it_('z', 'z', null, 2)]
    })
    expect(planNudge(t3, { type: 'item', id: 'x' }, 1)).toEqual({ op: 'moveItems', ids: ['x'], folderId: null, place: { before_id: 'z' } })
  })
})

describe('forbiddenDestinations', () => {
  it('un dossier ne peut aller ni dans lui-même ni dans ses descendants ; un objet va partout', () => {
    expect([...forbiddenDestinations(tree, { type: 'folder', id: 'A' })].sort()).toEqual(['A', 'B'])
    expect(forbiddenDestinations(tree, { type: 'item', id: 'a1' }).size).toBe(0)
  })
})
