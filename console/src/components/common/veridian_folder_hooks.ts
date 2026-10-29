// Crochets React des dossiers natifs (lot L2) : lecture, écritures, pli mémorisé, glisser-déposer.
import { useCallback, useMemo, useRef, useState } from 'react'
import { keepPreviousData, useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { message } from 'antd'
import { foldersApi, type FolderKind, type NativeTree } from '../../services/api/veridian_folders'
import type { SegmentLike, SegmentTreeNode } from '../segment/veridian_segment_folders'
import { buildNativeTree, isNativeTree, itemsSignature } from './veridian_folder_native'
import { planDrop, planNudge, type DragSource, type DropTarget, type MovePlan } from './veridian_folder_moves'
import { pruneOpenState, readOpenState, writeOpenState, type OpenState } from './veridian_folder_store'

export const foldersQueryKey = (workspaceId: string | undefined, kind: FolderKind) =>
  ['veridian-folders', workspaceId, kind] as const

// Lit les dossiers. La clé porte l'empreinte des objets : un objet créé, supprimé ou renommé
// (y compris à l'ancienne, avec un chemin dans son nom) relance la lecture, et le serveur le range.
export function useNativeFolders(workspaceId: string | undefined, kind: FolderKind, items: SegmentLike[] | undefined) {
  const signature = useMemo(() => itemsSignature(items), [items])
  const query = useQuery<NativeTree>({
    queryKey: [...foldersQueryKey(workspaceId, kind), signature],
    queryFn: async () => {
      const tree = await foldersApi.list(workspaceId as string, kind)
      if (!isNativeTree(tree)) throw new Error('unexpected folders.list response')
      return tree
    },
    enabled: !!workspaceId && items !== undefined,
    staleTime: 15_000,
    retry: 1,
    placeholderData: keepPreviousData
  })
  return { native: query.data, isError: query.isError, isLoading: query.isLoading }
}

// Arbre prêt à l'emploi : natif quand le serveur répond, déduit des noms sinon (repli).
export function useFolderTree<T extends SegmentLike>(items: T[] | undefined, native: NativeTree | undefined) {
  return useMemo(() => buildNativeTree(items ?? [], native), [items, native])
}

export interface FolderActions {
  busy: boolean
  run: (plan: MovePlan) => Promise<void>
  createFolder: (name: string, parentId: string | null) => Promise<void>
  renameFolder: (id: string, name: string) => Promise<void>
  deleteFolder: (id: string, toParent: boolean) => Promise<void>
  announcement: string
}

export function useFolderActions(workspaceId: string | undefined, kind: FolderKind): FolderActions {
  const queryClient = useQueryClient()
  const [announcement, setAnnouncement] = useState('')
  const refresh = useCallback(async () => {
    await Promise.all([
      queryClient.invalidateQueries({ queryKey: foldersQueryKey(workspaceId, kind) }),
      // Le serveur réécrit le nom stocké des objets : la liste des objets se relit aussi.
      queryClient.invalidateQueries({ queryKey: [kind, workspaceId] })
    ])
  }, [queryClient, workspaceId, kind])

  const mutation = useMutation({
    mutationFn: async (job: { label: string; call: () => Promise<unknown> }) => {
      await job.call()
      return job.label
    },
    onSuccess: async (label) => {
      setAnnouncement(label)
      await refresh()
    },
    onError: (error: Error) => {
      message.error(error?.message || 'Folder operation failed')
      setAnnouncement(error?.message || '')
      void refresh()
    }
  })

  const ws = workspaceId as string
  const exec = useCallback(
    (label: string, call: () => Promise<unknown>) => mutation.mutateAsync({ label, call }).then(() => undefined, () => undefined),
    [mutation]
  )
  return {
    busy: mutation.isPending,
    announcement,
    run: (plan) =>
      plan.op === 'moveItems'
        ? exec('moved', () => foldersApi.moveItems(ws, kind, plan.ids, plan.folderId, plan.place))
        : exec('moved', () => foldersApi.move(ws, plan.id, plan.parentId, plan.place)),
    createFolder: (name, parentId) => exec('created', () => foldersApi.create(ws, kind, name, parentId)),
    renameFolder: (id, name) => exec('renamed', () => foldersApi.rename(ws, id, name)),
    deleteFolder: (id, toParent) => exec('deleted', () => foldersApi.remove(ws, id, toParent))
  }
}

// Pli mémorisé : l'utilisateur ouvre ou ferme un dossier, le choix survit au rechargement.
export function useFolderOpen(workspaceId: string | undefined, kind: FolderKind) {
  const scope = `${workspaceId}:${kind}`
  // L'état est rattaché à son workspace et à son genre : en changer relit le stockage, sans effet.
  const [held, setHeld] = useState<{ scope: string; state: OpenState }>(() => ({
    scope,
    state: workspaceId ? readOpenState(workspaceId, kind) : {}
  }))
  const state = useMemo(
    () => (held.scope === scope ? held.state : workspaceId ? readOpenState(workspaceId, kind) : {}),
    [held, scope, workspaceId, kind]
  )

  const commit = useCallback(
    (change: (prev: OpenState) => OpenState) => {
      setHeld((prev) => {
        const base = prev.scope === scope ? prev.state : workspaceId ? readOpenState(workspaceId, kind) : {}
        const next = change(base)
        if (next === base) return prev.scope === scope ? prev : { scope, state: base }
        if (workspaceId) writeOpenState(workspaceId, kind, next)
        return { scope, state: next }
      })
    },
    [scope, workspaceId, kind]
  )

  // `fallback` : ouvert ou non tant que l'utilisateur n'a pas choisi.
  const isOpen = useCallback((folderId: string, fallback = true) => state[folderId] ?? fallback, [state])
  const setOpen = useCallback((folderId: string, open: boolean) => commit((prev) => ({ ...prev, [folderId]: open })), [commit])
  const toggle = useCallback((folderId: string, fallback = true) => commit((prev) => ({ ...prev, [folderId]: !(prev[folderId] ?? fallback) })), [commit])
  const setAll = useCallback(
    (ids: string[], open: boolean) =>
      commit((prev) => {
        const next = { ...prev }
        for (const id of ids) next[id] = open
        return next
      }),
    [commit]
  )
  const prune = useCallback(
    (existing: string[]) =>
      commit((prev) => {
        const next = pruneOpenState(prev, existing)
        return Object.keys(next).length === Object.keys(prev).length ? prev : next
      }),
    [commit]
  )
  return { isOpen, setOpen, toggle, setAll, prune }
}

export interface DndApi {
  dragging: DragSource | null
  overKey: string | null
  // Props à étaler sur l'élément qu'on peut saisir.
  dragProps: (src: DragSource, enabled?: boolean) => React.HTMLAttributes<HTMLElement>
  // Props à étaler sur l'élément qui reçoit un dépôt. `key` repère la cible survolée (style).
  // `target` peut dépendre du point de dépôt (bord gauche d'un dossier = avant lui, reste = dedans).
  dropProps: (target: DropTarget | ((e: React.DragEvent<HTMLElement>) => DropTarget), key: string) => React.HTMLAttributes<HTMLElement>
  // Clavier : Alt+↑/↓ décale d'un cran ; « m » ouvre « Déplacer vers… ».
  keyProps: (src: DragSource, onMoveTo: (src: DragSource) => void) => React.HTMLAttributes<HTMLElement>
}

export function useFolderDnd<T extends SegmentLike>(
  tree: SegmentTreeNode<T>[],
  onPlan: (plan: MovePlan) => void,
  enabled: boolean
): DndApi {
  const [dragging, setDragging] = useState<DragSource | null>(null)
  const [overKey, setOverKey] = useState<string | null>(null)
  const draggingRef = useRef<DragSource | null>(null)

  const dragProps: DndApi['dragProps'] = (src, on = true) =>
    enabled && on
      ? {
          draggable: true,
          onDragStart: (e) => {
            e.stopPropagation()
            draggingRef.current = src
            setDragging(src)
            e.dataTransfer.effectAllowed = 'move'
            e.dataTransfer.setData('text/plain', `${src.type}:${src.id}`)
          },
          onDragEnd: () => {
            draggingRef.current = null
            setDragging(null)
            setOverKey(null)
          }
        }
      : {}

  const resolve = (target: DropTarget | ((e: React.DragEvent<HTMLElement>) => DropTarget), e: React.DragEvent<HTMLElement>) =>
    typeof target === 'function' ? target(e) : target

  const dropProps: DndApi['dropProps'] = (target, key) =>
    enabled
      ? {
          onDragOver: (e) => {
            const src = draggingRef.current
            if (!src || !planDrop(tree, src, resolve(target, e))) return
            e.preventDefault()
            e.stopPropagation()
            e.dataTransfer.dropEffect = 'move'
            if (overKey !== key) setOverKey(key)
          },
          onDragLeave: () => setOverKey((k) => (k === key ? null : k)),
          onDrop: (e) => {
            const src = draggingRef.current
            const plan = src ? planDrop(tree, src, resolve(target, e)) : null
            draggingRef.current = null
            setDragging(null)
            setOverKey(null)
            if (!plan) return
            e.preventDefault()
            e.stopPropagation()
            onPlan(plan)
          }
        }
      : {}

  const keyProps: DndApi['keyProps'] = (src, onMoveTo) =>
    enabled
      ? {
          onKeyDown: (e) => {
            // Les événements d'un tiroir ou d'une fenêtre ouverts depuis l'élément remontent par React
            // (portail) : on ne réagit qu'aux touches tapées dans l'élément lui-même, jamais dans un champ.
            const target = e.target as HTMLElement
            if (!e.currentTarget.contains(target)) return
            if (target.closest('input, textarea, select, [contenteditable="true"], [role="dialog"], .ant-dropdown')) return
            if (e.altKey && (e.key === 'ArrowUp' || e.key === 'ArrowDown')) {
              const plan = planNudge(tree, src, e.key === 'ArrowUp' ? -1 : 1)
              e.preventDefault()
              if (plan) onPlan(plan)
            } else if (!e.altKey && !e.ctrlKey && !e.metaKey && (e.key === 'm' || e.key === 'M')) {
              e.preventDefault()
              onMoveTo(src)
            }
          }
        }
      : {}

  return { dragging, overKey, dragProps, dropProps, keyProps }
}
