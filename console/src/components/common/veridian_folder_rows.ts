// Listes et modèles en dossiers pliables (fork Veridian). Aucune migration : le NOM porte le
// chemin, séparateur « espace slash espace » (« 1 Prospection / Séquence froide »).
// Module pur (aucun import React) : transforme la liste en lignes « dossier » / « élément ».
// Une liste sans dossier rend exactement les mêmes lignes qu'avant (même ordre, même clé).
import {
  buildSegmentTree,
  hasFolders,
  type SegmentFolderNode,
  type SegmentLike,
  type SegmentTreeNode
} from '../segment/veridian_segment_folders'

export type NamedItem = SegmentLike

export interface FolderRow {
  kind: 'folder'
  key: string
  label: string
  depth: number
  // Éléments contenus, sous-dossiers compris (indépendant de l'état plié).
  count: number
  open: boolean
  // Dossier natif (lot L2) : identifiant serveur et noeud, pour le menu et les déplacements.
  folderId?: string
  node?: SegmentFolderNode<NamedItem>
}

export interface ItemRow<T extends NamedItem> {
  kind: 'item'
  key: string
  // Nom court (dernier élément du chemin) ; le nom complet reste dans `item.name`.
  label: string
  depth: number
  item: T
}

export type TreeRow<T extends NamedItem> = FolderRow | ItemRow<T>

export function buildFolderRows<T extends NamedItem>(
  items: T[],
  collapsed: ReadonlySet<string>
): TreeRow<T>[] {
  return buildRowsFromTree(buildSegmentTree(items), items, (node) => !collapsed.has(node.key))
}

// Lignes d'un arbre déjà construit (natif ou déduit des noms). `isOpen` décide du pli.
export function buildRowsFromTree<T extends NamedItem>(
  tree: SegmentTreeNode<T>[],
  items: T[],
  isOpen: (node: SegmentFolderNode<T>) => boolean
): TreeRow<T>[] {
  if (!hasFolders(tree)) {
    // Liste plate : les éléments tels que reçus, dans l'ordre reçu (ceux que la recherche a gardés).
    const kept = new Set(tree.flatMap((n) => (n.kind === 'segment' ? [n.segment.id] : [])))
    return items
      .filter((item) => kept.has(item.id))
      .map((item) => ({ kind: 'item', key: item.id, label: item.name, depth: 0, item }))
  }
  const rows: TreeRow<T>[] = []
  const walk = (nodes: SegmentTreeNode<T>[], depth: number) => {
    for (const node of nodes) {
      if (node.kind === 'segment') {
        rows.push({ kind: 'item', key: node.key, label: node.label, depth, item: node.segment })
        continue
      }
      const open = isOpen(node)
      rows.push({
        kind: 'folder',
        key: node.key,
        label: node.label,
        depth,
        count: node.count,
        open,
        folderId: node.folderId,
        node: node as unknown as SegmentFolderNode<NamedItem>
      })
      if (open) walk(node.children, depth + 1)
    }
  }
  walk(tree, 0)
  return rows
}

export function hasFolderRows<T extends NamedItem>(rows: TreeRow<T>[]): boolean {
  return rows.some((row) => row.kind === 'folder')
}
