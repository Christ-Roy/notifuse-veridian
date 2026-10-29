// Gestionnaire des dossiers natifs (lot L2) : assemble lecture, écritures, pli mémorisé,
// recherche, glisser-déposer et dialogues pour UN genre d'objet. Les trois écrans (pastilles de
// segments, cartes de listes, tableau de modèles) en consomment les morceaux, sans dupliquer la logique.
import { useCallback, useMemo, useState } from 'react'
import { Modal } from 'antd'
import { useLingui } from '@lingui/react/macro'
import {
  filterSegmentTree,
  hasFolders,
  type SegmentFolderNode,
  type SegmentLike,
  type SegmentTreeNode
} from '../segment/veridian_segment_folders'
import type { FolderKind } from '../../services/api/veridian_folders'
import { findFolder, flattenFolders, folderKey } from './veridian_folder_native'
import { useFolderActions, useFolderDnd, useFolderOpen, useFolderTree, useNativeFolders } from './veridian_folder_hooks'
import type { DragSource, MovePlan } from './veridian_folder_moves'
import { FolderMovePicker, FolderNamePrompt, FolderToolbar } from './veridian_folder_ui'

export interface FolderManagerOptions<T extends SegmentLike> {
  workspaceId: string | undefined
  kind: FolderKind
  items: T[] | undefined
  canWrite: boolean
}

type Prompt = { mode: 'create'; parentId: string | null } | { mode: 'rename'; id: string; initial: string }

export function useVeridianFolders<T extends SegmentLike>({ workspaceId, kind, items, canWrite }: FolderManagerOptions<T>) {
  const { t } = useLingui()
  const { native } = useNativeFolders(workspaceId, kind, items)
  const tree = useFolderTree(items, native)
  const actions = useFolderActions(workspaceId, kind)
  const open = useFolderOpen(workspaceId, kind)
  const [query, setQuery] = useState('')
  const [prompt, setPrompt] = useState<Prompt | null>(null)
  const [moving, setMoving] = useState<DragSource | null>(null)

  const searching = query.trim() !== ''
  const visibleTree = useMemo(() => filterSegmentTree(tree, query), [tree, query])
  const nested = hasFolders(tree)
  const flat = useMemo(() => flattenFolders(tree), [tree])

  const run = useCallback((plan: MovePlan) => void actions.run(plan), [actions])
  const dnd = useFolderDnd(tree, run, canWrite)

  const announcement = useMemo(() => {
    switch (actions.announcement) {
      case 'moved':
        return t`Moved.`
      case 'created':
        return t`Folder created.`
      case 'renamed':
        return t`Folder renamed.`
      case 'deleted':
        return t`Folder deleted.`
      default:
        return actions.announcement
    }
  }, [actions.announcement, t])

  const sourceLabel = (src: DragSource | null): string => {
    if (!src) return ''
    if (src.type === 'folder') return findFolder(tree, src.id)?.label ?? ''
    const stack: SegmentTreeNode<T>[][] = [tree]
    while (stack.length) {
      for (const n of stack.pop() as SegmentTreeNode<T>[]) {
        if (n.kind === 'segment' && n.segment.id === src.id) return n.label
        if (n.kind === 'folder') stack.push(n.children)
      }
    }
    return ''
  }

  const confirmDelete = (node: SegmentFolderNode<T>) => {
    if (!node.folderId) return
    const id = node.folderId
    const nonEmpty = node.count > 0 || node.children.some((c) => c.kind === 'folder')
    Modal.confirm({
      title: t`Delete folder "${node.label}"`,
      content: nonEmpty
        ? t`Its content is not deleted: it moves up one level.`
        : t`The folder is empty.`,
      okText: t`Delete folder`,
      cancelText: t`Cancel`,
      okButtonProps: { danger: true },
      onOk: () => actions.deleteFolder(id, true)
    })
  }

  const dialogs = (
    <>
      <FolderNamePrompt
        open={prompt !== null}
        title={prompt?.mode === 'rename' ? t`Rename folder` : t`New folder`}
        initial={prompt?.mode === 'rename' ? prompt.initial : ''}
        okText={prompt?.mode === 'rename' ? t`Rename` : t`Create`}
        onCancel={() => setPrompt(null)}
        onOk={(name) => {
          const current = prompt
          setPrompt(null)
          if (!current) return
          if (current.mode === 'create') void actions.createFolder(name, current.parentId)
          else void actions.renameFolder(current.id, name)
        }}
      />
      <FolderMovePicker
        source={moving}
        tree={tree}
        sourceLabel={sourceLabel(moving)}
        onCancel={() => setMoving(null)}
        onPick={(folderId) => {
          const src = moving
          setMoving(null)
          if (!src) return
          const plan: MovePlan =
            src.type === 'item'
              ? { op: 'moveItems', ids: [src.id], folderId, place: {} }
              : { op: 'moveFolder', id: src.id, parentId: folderId, place: {} }
          run(plan)
        }}
      />
    </>
  )

  const allIds = flat.map((f) => f.id)
  const toolbar = (rootDrop?: React.ReactNode) => (
    <FolderToolbar
      query={query}
      onQuery={setQuery}
      canWrite={canWrite}
      onNewFolder={() => setPrompt({ mode: 'create', parentId: null })}
      onExpandAll={() => open.setAll(allIds, true)}
      onCollapseAll={() => open.setAll(allIds, false)}
      announcement={announcement}
      rootDrop={rootDrop}
      hasFolders={nested}
    />
  )

  return {
    native,
    tree,
    visibleTree,
    nested,
    searching,
    query,
    setQuery,
    canWrite,
    open,
    dnd,
    actions,
    flat,
    // Dossier ouvert ? Une recherche ouvre tout ; sinon le choix mémorisé, sinon `fallback`.
    // Repli sur les noms (API en échec) : le dossier n'a pas d'identifiant serveur, sa clé en tient lieu.
    isOpen: (node: SegmentFolderNode<T>, fallback = true) =>
      searching ? true : open.isOpen(node.folderId ?? node.key, fallback),
    toggle: (node: SegmentFolderNode<T>, fallback = true) => {
      if (!searching) open.toggle(node.folderId ?? node.key, fallback)
    },
    menu: (node: SegmentFolderNode<T>) => ({
      canWrite,
      onRename: () => node.folderId && setPrompt({ mode: 'rename', id: node.folderId, initial: node.label }),
      onCreateSub: () => node.folderId && setPrompt({ mode: 'create', parentId: node.folderId }),
      onMove: () => node.folderId && setMoving({ type: 'folder', id: node.folderId }),
      onDelete: () => confirmDelete(node)
    }),
    startMove: (src: DragSource) => setMoving(src),
    newFolder: () => setPrompt({ mode: 'create', parentId: null }),
    dialogs,
    toolbar,
    folderKey
  }
}

export type VeridianFolders<T extends SegmentLike> = ReturnType<typeof useVeridianFolders<T>>
