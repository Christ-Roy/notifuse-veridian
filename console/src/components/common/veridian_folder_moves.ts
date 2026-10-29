// Déplacements dans l'arbre des dossiers (lot L2) : traduit un geste (glisser-déposer, flèches
// du clavier, « Déplacer vers… ») en requête serveur, ou en rien si le geste n'a pas de sens
// (dossier déposé dans son propre sous-arbre, objet déjà à sa place). Module pur, testé seul.
import type { SegmentLike, SegmentTreeNode } from '../segment/veridian_segment_folders'
import type { FolderPlace } from '../../services/api/veridian_folders'

export type DragSource = { type: 'item'; id: string } | { type: 'folder'; id: string }
export type DropTarget =
  | { type: 'folder'; id: string | null } // dans ce dossier (null = racine), en queue
  | { type: 'before-item'; id: string }
  | { type: 'before-folder'; id: string }

export type MovePlan =
  | { op: 'moveItems'; ids: string[]; folderId: string | null; place: FolderPlace }
  | { op: 'moveFolder'; id: string; parentId: string | null; place: FolderPlace }

interface Index {
  parentOfItem: Map<string, string | null>
  parentOfFolder: Map<string, string | null>
  childrenItems: Map<string | null, string[]>
  childrenFolders: Map<string | null, string[]>
}

function index<T extends SegmentLike>(tree: SegmentTreeNode<T>[]): Index {
  const ix: Index = { parentOfItem: new Map(), parentOfFolder: new Map(), childrenItems: new Map(), childrenFolders: new Map() }
  const walk = (nodes: SegmentTreeNode<T>[], parent: string | null) => {
    const items: string[] = []
    const folders: string[] = []
    for (const n of nodes) {
      if (n.kind === 'segment') {
        ix.parentOfItem.set(n.segment.id, parent)
        items.push(n.segment.id)
      } else if (n.folderId) {
        ix.parentOfFolder.set(n.folderId, parent)
        folders.push(n.folderId)
        walk(n.children, n.folderId)
      }
    }
    ix.childrenItems.set(parent, items)
    ix.childrenFolders.set(parent, folders)
  }
  walk(tree, null)
  return ix
}

function isSelfOrDescendant(ix: Index, candidate: string | null, ancestor: string): boolean {
  for (let cur = candidate, guard = 0; cur !== null && guard < 64; guard++) {
    if (cur === ancestor) return true
    cur = ix.parentOfFolder.get(cur) ?? null
  }
  return false
}

export function planDrop<T extends SegmentLike>(tree: SegmentTreeNode<T>[], src: DragSource, target: DropTarget): MovePlan | null {
  const ix = index(tree)
  if (src.type === 'item') {
    if (!ix.parentOfItem.has(src.id)) return null
    if (target.type === 'folder') {
      if (target.id !== null && !ix.parentOfFolder.has(target.id)) return null
      if (ix.parentOfItem.get(src.id) === target.id) return null
      return { op: 'moveItems', ids: [src.id], folderId: target.id, place: {} }
    }
    if (target.type === 'before-item') {
      if (target.id === src.id || !ix.parentOfItem.has(target.id)) return null
      const folderId = ix.parentOfItem.get(target.id) ?? null
      return { op: 'moveItems', ids: [src.id], folderId, place: { before_id: target.id } }
    }
    return null
  }
  if (!ix.parentOfFolder.has(src.id)) return null
  if (target.type === 'folder') {
    if (target.id !== null && (!ix.parentOfFolder.has(target.id) || isSelfOrDescendant(ix, target.id, src.id))) return null
    if (ix.parentOfFolder.get(src.id) === target.id) return null
    return { op: 'moveFolder', id: src.id, parentId: target.id, place: {} }
  }
  if (target.type === 'before-folder') {
    if (target.id === src.id || !ix.parentOfFolder.has(target.id) || isSelfOrDescendant(ix, target.id, src.id)) return null
    return { op: 'moveFolder', id: src.id, parentId: ix.parentOfFolder.get(target.id) ?? null, place: { before_id: target.id } }
  }
  return null
}

// Alt+↑ / Alt+↓ : décale d'un cran parmi les frères de même nature. null aux extrémités.
export function planNudge<T extends SegmentLike>(tree: SegmentTreeNode<T>[], src: DragSource, delta: -1 | 1): MovePlan | null {
  const ix = index(tree)
  const parent = src.type === 'item' ? ix.parentOfItem.get(src.id) : ix.parentOfFolder.get(src.id)
  if (parent === undefined) return null
  const siblings = (src.type === 'item' ? ix.childrenItems : ix.childrenFolders).get(parent) ?? []
  const at = siblings.indexOf(src.id)
  const to = at + delta
  if (at < 0 || to < 0 || to >= siblings.length) return null
  // Monter : avant le frère précédent. Descendre : avant le frère qui suit celui d'après (ou en queue).
  const before = delta === -1 ? siblings[to] : siblings[to + 1]
  const place: FolderPlace = before ? { before_id: before } : {}
  return src.type === 'item'
    ? { op: 'moveItems', ids: [src.id], folderId: parent, place }
    : { op: 'moveFolder', id: src.id, parentId: parent, place }
}

// Destinations proposées par « Déplacer vers… » : tous les dossiers sauf, pour un dossier, lui-même et son sous-arbre.
export function forbiddenDestinations<T extends SegmentLike>(tree: SegmentTreeNode<T>[], src: DragSource): Set<string> {
  const forbidden = new Set<string>()
  if (src.type !== 'folder') return forbidden
  const ix = index(tree)
  for (const id of ix.parentOfFolder.keys()) if (isSelfOrDescendant(ix, id, src.id)) forbidden.add(id)
  return forbidden
}
