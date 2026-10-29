// Arbre natif des dossiers (lot L2) : fusionne la liste des objets (segments, listes, modèles)
// avec les dossiers et appartenances du serveur, et rend le MÊME arbre que l'ancien module par
// nom (veridian_segment_folders.ts), pour que la liste, les sélecteurs et les tableaux n'aient
// rien à changer. Module pur (aucun import React) : testable seul.
//
// Repli : sans données natives (API en échec), l'arbre se déduit des noms `A / B / C`, comme avant.
import {
  buildSegmentTree,
  countSegments,
  parseSegmentPath,
  type SegmentFolderNode,
  type SegmentLeafNode,
  type SegmentLike,
  type SegmentTreeNode
} from '../segment/veridian_segment_folders'
import type { NativeFolder, NativeItem, NativeTree } from '../../services/api/veridian_folders'

const naturalCollator = new Intl.Collator('fr', { numeric: true, sensitivity: 'base' })

const fold = (s: string) => s.normalize('NFC').toLowerCase()

// Libellé court d'un objet rangé sous `path` : le nom sans le chemin s'il le porte, sinon le nom entier.
export function labelFor(name: string, path: string[]): string {
  if (path.length === 0) return name
  const parts = parseSegmentPath(name)
  if (parts.length === path.length + 1 && path.every((p, i) => fold(parts[i]) === fold(p))) {
    return parts[parts.length - 1]
  }
  return name
}

// Empreinte des objets (identifiant + nom) : change dès qu'un objet apparaît, disparaît ou est
// renommé, ce qui relance la lecture des dossiers (le serveur y range les noms à l'ancienne).
export function itemsSignature(items: SegmentLike[] | undefined): string {
  if (!items) return 'none'
  let h = 2166136261
  for (const it of items) {
    const s = `${it.id}\u0000${it.name}\u0001`
    for (let i = 0; i < s.length; i++) {
      h ^= s.charCodeAt(i)
      h = Math.imul(h, 16777619)
    }
  }
  return `${items.length}:${(h >>> 0).toString(36)}`
}

export const folderKey = (id: string) => `folder:${id}`
export const itemKey = (id: string) => `segment:${id}`

function byPositionThenName(a: NativeFolder, b: NativeFolder) {
  if (a.position !== b.position) return a.position - b.position
  return naturalCollator.compare(a.name, b.name) || (a.id < b.id ? -1 : 1)
}

export function isNativeTree(native: unknown): native is NativeTree {
  const n = native as Partial<NativeTree> | null | undefined
  return !!n && Array.isArray(n.folders) && Array.isArray(n.items)
}

export function buildNativeTree<T extends SegmentLike>(items: T[], native: NativeTree | undefined): SegmentTreeNode<T>[] {
  // Réponse absente ou de forme inattendue (proxy, ancienne version du serveur) : on se replie sur les noms.
  if (!isNativeTree(native)) return buildSegmentTree(items)

  const byId = new Map(native.folders.map((f) => [f.id, f]))
  const kids = new Map<string | null, NativeFolder[]>()
  for (const f of native.folders) {
    const parent = f.parent_id && byId.has(f.parent_id) ? f.parent_id : null
    const list = kids.get(parent) ?? []
    list.push(f)
    kids.set(parent, list)
  }
  for (const list of kids.values()) list.sort(byPositionThenName)

  const placement = new Map<string, NativeItem>(native.items.filter((i) => i.folder_id).map((i) => [i.id, i]))
  const rootRank = new Map<string, number>(
    native.items.filter((i) => !i.folder_id && i.position !== null).map((i) => [i.id, i.position as number])
  )
  const pathIndex = new Map<string, string>(native.folders.map((f) => [fold(f.path_text), f.id]))

  type Entry = { item: T; label: string; rank: number; order: number }
  const inFolder = new Map<string | null, Entry[]>()
  items.forEach((item, order) => {
    const p = placement.get(item.id)
    let folderId: string | null = null
    let label = item.name
    let rank = Number.MAX_SAFE_INTEGER
    if (p && byId.has(p.folder_id as string)) {
      folderId = p.folder_id
      const folder = byId.get(folderId as string) as NativeFolder
      if (p.name === item.name) label = p.label
      else {
        // Nom relu après le serveur (ou avant) : un nom qui porte encore un autre chemin est périmé, le libellé serveur fait foi.
        const derived = labelFor(item.name, folder.path)
        label = derived === item.name && parseSegmentPath(item.name).length > 1 ? p.label : derived
      }
      rank = p.position ?? rank
    } else {
      // Pas (encore) rangé côté serveur : un nom à l'ancienne se range sous le dossier du même chemin s'il existe.
      const parts = parseSegmentPath(item.name)
      if (parts.length > 1) {
        const target = pathIndex.get(fold(parts.slice(0, -1).join(' / ')))
        if (target) {
          folderId = target
          label = parts[parts.length - 1]
        }
      }
      if (folderId === null) rank = rootRank.get(item.id) ?? rank
    }
    const list = inFolder.get(folderId) ?? []
    list.push({ item, label, rank, order })
    inFolder.set(folderId, list)
  })

  const build = (parent: string | null): SegmentTreeNode<T>[] => {
    const folders: SegmentFolderNode<T>[] = (kids.get(parent) ?? []).map((f) => {
      const children = build(f.id)
      return {
        kind: 'folder',
        key: folderKey(f.id),
        label: f.name,
        count: countSegments(children),
        children,
        folderId: f.id
      }
    })
    const leaves: SegmentLeafNode<T>[] = (inFolder.get(parent) ?? [])
      .sort((a, b) => a.rank - b.rank || (parent === null ? a.order - b.order : naturalCollator.compare(a.label, b.label) || a.order - b.order))
      .map((e) => ({ kind: 'segment', key: itemKey(e.item.id), label: e.label, segment: e.item }))
    return [...folders, ...leaves]
  }
  return build(null)
}

// Tous les dossiers de l'arbre, dans l'ordre d'affichage, avec leur profondeur.
export interface FlatFolder {
  id: string
  key: string
  label: string
  depth: number
  parentId: string | null
  count: number
}

export function flattenFolders<T extends SegmentLike>(nodes: SegmentTreeNode<T>[]): FlatFolder[] {
  const out: FlatFolder[] = []
  const walk = (list: SegmentTreeNode<T>[], depth: number, parentId: string | null) => {
    for (const n of list) {
      if (n.kind !== 'folder' || !n.folderId) continue
      out.push({ id: n.folderId, key: n.key, label: n.label, depth, parentId, count: n.count })
      walk(n.children, depth + 1, n.folderId)
    }
  }
  walk(nodes, 0, null)
  return out
}

export function findFolder<T extends SegmentLike>(nodes: SegmentTreeNode<T>[], folderId: string): SegmentFolderNode<T> | undefined {
  for (const n of nodes) {
    if (n.kind !== 'folder') continue
    if (n.folderId === folderId) return n
    const inner = findFolder(n.children, folderId)
    if (inner) return inner
  }
  return undefined
}

// Retire les dossiers sans aucun objet (sélecteurs : un dossier vide n'offre rien à choisir).
export function withoutEmptyFolders<T extends SegmentLike>(nodes: SegmentTreeNode<T>[]): SegmentTreeNode<T>[] {
  return nodes.flatMap((n): SegmentTreeNode<T>[] => {
    if (n.kind === 'segment') return [n]
    const children = withoutEmptyFolders(n.children)
    return children.length > 0 ? [{ ...n, children, count: countSegments(children) }] : []
  })
}

// Même règle que le serveur : pas de « / » entouré d'espaces dans un nom (ancienne convention de chemin).
export function folderNameError(name: string): 'empty' | 'separator' | null {
  const trimmed = name.trim()
  if (trimmed === '') return 'empty'
  if (/(^|\s)\/(\s|$)/.test(trimmed)) return 'separator'
  return null
}
