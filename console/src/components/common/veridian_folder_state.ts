import { useCallback, useMemo } from 'react'
import { buildRowsFromTree, hasFolderRows, type FolderRow, type NamedItem, type TreeRow } from './veridian_folder_rows'
import { createElement, type CSSProperties, type HTMLAttributes, type ReactNode } from 'react'
import { FolderMenu } from './veridian_folder_ui'
import type { DragSource, DropTarget } from './veridian_folder_moves'
import { useVeridianFolders } from './veridian_folder_manager'
import type { FolderKind } from '../../services/api/veridian_folders'

export interface FolderRowsOptions {
  // Dossiers natifs (lot L2). Sans workspace : l'arbre se déduit des noms « A / B », comme avant.
  workspaceId?: string
  kind?: FolderKind
  canWrite?: boolean
}

// Dossiers dépliés par défaut : rien n'est caché, le pli est un confort (et il est mémorisé).
export function useFolderRows<T extends NamedItem>(items: T[] | undefined, options: FolderRowsOptions = {}) {
  const { workspaceId, kind = 'lists', canWrite = false } = options
  const folders = useVeridianFolders<T>({ workspaceId, kind, items, canWrite })
  const { visibleTree, isOpen, toggle: toggleNode, searching } = folders

  const byKey = useMemo(() => {
    const map = new Map<string, Parameters<typeof toggleNode>[0]>()
    const walk = (nodes: typeof visibleTree) => {
      for (const n of nodes) {
        if (n.kind === 'folder') {
          map.set(n.key, n)
          walk(n.children)
        }
      }
    }
    walk(folders.tree)
    return map
  }, [folders.tree])
  const toggle = useCallback(
    (key: string) => {
      const node = byKey.get(key)
      if (node) toggleNode(node)
    },
    [byKey, toggleNode]
  )

  const rows: TreeRow<T>[] = useMemo(
    () => buildRowsFromTree(visibleTree, items ?? [], (node) => isOpen(node)),
    // isOpen change quand le pli mémorisé change
    // eslint-disable-next-line react-hooks/exhaustive-deps
    [visibleTree, items, folders.open.isOpen, searching]
  )
  // Une recherche affiche le résultat même sans dossier ; sans recherche, plat tant qu'aucun dossier n'existe.
  const nested = useMemo(() => hasFolderRows(rows) || folders.nested || searching, [rows, folders.nested, searching])

  // Glisser-déposer et clavier sur une ligne (carte, ligne de tableau). Dossier : haut de ligne = avant lui, reste = dedans.
  const { dnd, startMove } = folders
  const rowProps = (row: TreeRow<T>): HTMLAttributes<HTMLElement> & { style?: CSSProperties } => {
    if (!folders.canWrite) return {}
    if (row.kind === 'folder') {
      if (!row.folderId) return {}
      const src: DragSource = { type: 'folder', id: row.folderId }
      const target = (e: React.DragEvent<HTMLElement>): DropTarget => {
        const box = e.currentTarget.getBoundingClientRect()
        return e.clientY < box.top + box.height * 0.25 ? { type: 'before-folder', id: row.folderId as string } : { type: 'folder', id: row.folderId as string }
      }
      return {
        ...dnd.dragProps(src),
        ...dnd.dropProps(target, row.key),
        ...dnd.keyProps(src, startMove),
        style: dnd.overKey === row.key ? OVER : undefined
      }
    }
    const src: DragSource = { type: 'item', id: row.item.id }
    return {
      ...dnd.dragProps(src),
      ...dnd.dropProps({ type: 'before-item', id: row.item.id }, row.key),
      ...dnd.keyProps(src, startMove),
      tabIndex: 0,
      style: dnd.overKey === row.key ? OVER : undefined
    }
  }
  const folderExtra = (row: FolderRow): ReactNode =>
    row.node ? createElement(FolderMenu, { node: row.node, ...folders.menu(row.node as never) }) : null
  return { rows, toggle, nested, folders, rowProps, folderExtra }
}

const OVER: CSSProperties = { outline: '2px dashed var(--ant-color-primary, #1677ff)', outlineOffset: -2 }
