// Dossiers natifs (fork Veridian, lot L2, V65) : client des routes /api/veridian/folders.* et
// /api/veridian/{segments,lists,templates}.move. Le serveur tient aussi à jour le nom stocké
// `A / B / C` des objets : après chaque écriture, la liste des objets doit être relue.
import { api } from './client'

export type FolderKind = 'segments' | 'lists' | 'templates'

export interface NativeFolder {
  id: string
  parent_id: string | null
  name: string
  position: number
  depth: number
  path: string[]
  path_text: string
  count: number
  direct_items: number
  subfolders: number
}

export interface NativeItem {
  id: string
  name: string
  label: string
  folder_id: string | null
  position: number | null
  path_text: string
}

export interface NativeTree {
  kind: FolderKind
  folders: NativeFolder[]
  items: NativeItem[]
  adopt?: { adopted: number; folders_created: number; pruned: number; warnings?: string[] }
}

export interface FolderPlace {
  // Indice parmi les frères (0 = en tête) ou « juste avant ce frère » ; vide = en queue.
  position?: number
  before_id?: string
}

const base = '/api/veridian'

export const foldersApi = {
  list: (workspaceId: string, kind: FolderKind) =>
    api.get<NativeTree>(`${base}/folders.list?workspace_id=${encodeURIComponent(workspaceId)}&kind=${kind}`),
  create: (workspaceId: string, kind: FolderKind, name: string, parentId: string | null, place: FolderPlace = {}) =>
    api.post(`${base}/folders.create`, { workspace_id: workspaceId, kind, name, parent_id: parentId ?? '', ...place }),
  rename: (workspaceId: string, id: string, name: string) =>
    api.post(`${base}/folders.rename`, { workspace_id: workspaceId, id, name }),
  move: (workspaceId: string, id: string, parentId: string | null, place: FolderPlace = {}) =>
    api.post(`${base}/folders.move`, { workspace_id: workspaceId, id, parent_id: parentId ?? '', ...place }),
  remove: (workspaceId: string, id: string, toParent: boolean) =>
    api.post(`${base}/folders.delete`, { workspace_id: workspaceId, id, contents: toParent ? 'to_parent' : 'refuse' }),
  moveItems: (workspaceId: string, kind: FolderKind, ids: string[], folderId: string | null, place: FolderPlace = {}) =>
    api.post(`${base}/${kind}.move`, { workspace_id: workspaceId, ids, folder_id: folderId ?? '', ...place })
}
