// Dossiers de segments (fork Veridian). Aucune migration : le NOM du segment
// porte le chemin, séparateur « espace slash espace » (« 2 Cibles / Animaleries / P1 »).
// Un nom sans séparateur reste un segment à la racine.
// Module pur (aucun import React) : testable seul, partagé par la liste et les sélecteurs.

export interface SegmentLike {
  id: string
  name: string
}

export interface SegmentFolderNode<T extends SegmentLike> {
  kind: 'folder'
  key: string
  label: string
  // Nombre de segments contenus, sous-dossiers compris.
  count: number
  children: SegmentTreeNode<T>[]
}

export interface SegmentLeafNode<T extends SegmentLike> {
  kind: 'segment'
  key: string
  label: string
  segment: T
}

export type SegmentTreeNode<T extends SegmentLike> = SegmentFolderNode<T> | SegmentLeafNode<T>

const SEPARATOR_RE = /(?:\s+\/)+\s+/
const LEADING_SEPARATOR_RE = /^(?:\/\s+)+/
const TRAILING_SEPARATOR_RE = /(?:\s+\/)+$/

const naturalCollator = new Intl.Collator('fr', { numeric: true, sensitivity: 'base' })

// Découpe le nom en chemin. Tolère les espaces en trop et les séparateurs répétés ;
// un slash collé sans espace (« AC/DC », « 1/2 ») n'est pas un séparateur.
export function parseSegmentPath(name: string): string[] {
  const trimmed = name.trim()
  const cleaned = trimmed
    .replace(LEADING_SEPARATOR_RE, '')
    .replace(TRAILING_SEPARATOR_RE, '')
    .trim()
  const parts = cleaned
    .split(SEPARATOR_RE)
    .map((part) => part.trim())
    .filter((part) => part !== '')
  return parts.length > 0 ? parts : [trimmed]
}

interface MutableFolder<T extends SegmentLike> {
  path: string[]
  folders: Map<string, MutableFolder<T>>
  leaves: { segment: T; label: string }[]
}

function newFolder<T extends SegmentLike>(path: string[]): MutableFolder<T> {
  return { path, folders: new Map(), leaves: [] }
}

function finalizeFolder<T extends SegmentLike>(folder: MutableFolder<T>): SegmentTreeNode<T>[] {
  const subfolders = [...folder.folders.values()]
    .sort((a, b) => naturalCollator.compare(a.path[a.path.length - 1], b.path[b.path.length - 1]))
    .map((sub): SegmentFolderNode<T> => {
      const children = finalizeFolder(sub)
      return {
        kind: 'folder',
        key: `folder:${JSON.stringify(sub.path)}`,
        label: sub.path[sub.path.length - 1],
        count: countSegments(children),
        children
      }
    })
  // Dossiers d'abord (triés naturellement), puis segments. Dans un dossier, les
  // segments sont triés naturellement (P1, P2, P3, P4) ; à la racine, ordre reçu
  // (une liste sans dossier reste identique à l'ancienne).
  const sortLeaves = folder.path.length > 0
  const leaves: SegmentLeafNode<T>[] = folder.leaves
    .map((leaf) => ({
      kind: 'segment' as const,
      key: `segment:${leaf.segment.id}`,
      label: leaf.label,
      segment: leaf.segment
    }))
    .sort((a, b) => (sortLeaves ? naturalCollator.compare(a.label, b.label) : 0))
  return [...subfolders, ...leaves]
}

// Construit l'arbre. Un dossier et un segment homonymes restent deux nœuds distincts.
export function buildSegmentTree<T extends SegmentLike>(segments: T[]): SegmentTreeNode<T>[] {
  const root = newFolder<T>([])
  for (const segment of segments) {
    const path = parseSegmentPath(segment.name)
    if (path.length === 1) {
      root.leaves.push({ segment, label: segment.name })
      continue
    }
    let current = root
    for (const part of path.slice(0, -1)) {
      let next = current.folders.get(part)
      if (!next) {
        next = newFolder<T>([...current.path, part])
        current.folders.set(part, next)
      }
      current = next
    }
    current.leaves.push({ segment, label: path[path.length - 1] })
  }
  return finalizeFolder(root)
}

export function countSegments<T extends SegmentLike>(nodes: SegmentTreeNode<T>[]): number {
  return nodes.reduce((sum, node) => sum + (node.kind === 'segment' ? 1 : node.count), 0)
}

// Vrai si au moins un dossier existe à un niveau quelconque. Faux = liste plate d'avant.
export function hasFolders<T extends SegmentLike>(nodes: SegmentTreeNode<T>[]): boolean {
  return nodes.some((node) => node.kind === 'folder')
}

export function segmentsIn<T extends SegmentLike>(nodes: SegmentTreeNode<T>[]): T[] {
  return nodes.flatMap((node) => (node.kind === 'segment' ? [node.segment] : segmentsIn(node.children)))
}

// Clés de tous les dossiers qui contiennent (à n'importe quelle profondeur) un segment de la liste.
export function folderKeysContaining<T extends SegmentLike>(
  nodes: SegmentTreeNode<T>[],
  segmentIds: Set<string>
): string[] {
  return nodes.flatMap((node) => {
    if (node.kind === 'segment') return []
    const inner = folderKeysContaining(node.children, segmentIds)
    const holdsOne = segmentsIn(node.children).some((segment) => segmentIds.has(segment.id))
    return holdsOne ? [node.key, ...inner] : inner
  })
}

function normalizeText(text: string): string {
  return text.normalize('NFD').replace(/\p{Diacritic}/gu, '').toLowerCase().trim()
}

// Recherche texte sur le nom complet. Ne garde que les dossiers qui contiennent un résultat.
export function filterSegmentTree<T extends SegmentLike>(
  nodes: SegmentTreeNode<T>[],
  query: string
): SegmentTreeNode<T>[] {
  const needle = normalizeText(query)
  if (needle === '') return nodes
  const matches = (segment: T) => normalizeText(segment.name).includes(needle)
  const prune = (list: SegmentTreeNode<T>[]): SegmentTreeNode<T>[] =>
    list.flatMap((node): SegmentTreeNode<T>[] => {
      if (node.kind === 'segment') return matches(node.segment) ? [node] : []
      const children = prune(node.children)
      return children.length > 0
        ? [{ ...node, children, count: countSegments(children) }]
        : []
    })
  return prune(nodes)
}

export interface SegmentTreeSelectNode {
  title: string
  value: string
  selectable: boolean
  children?: SegmentTreeSelectNode[]
}

// Données pour un sélecteur arborescent : les dossiers ne sont pas sélectionnables.
export function toTreeSelectData<T extends SegmentLike>(
  nodes: SegmentTreeNode<T>[]
): SegmentTreeSelectNode[] {
  return nodes.map((node) =>
    node.kind === 'folder'
      ? {
          title: `${node.label} (${node.count})`,
          value: node.key,
          selectable: false,
          children: toTreeSelectData(node.children)
        }
      : { title: node.label, value: node.segment.id, selectable: true }
  )
}
