// Repli mémorisé des dossiers (lot L2) : l'état ouvert / fermé de chaque dossier, par workspace
// et par genre, dans le navigateur de l'utilisateur. Un confort : si le stockage est absent ou
// refuse (navigation privée), tout reste affiché comme sans mémoire. Module pur.
export type OpenState = Record<string, boolean>

const key = (workspaceId: string, kind: string) => `veridian:folders:open:${workspaceId}:${kind}`

export function readOpenState(workspaceId: string, kind: string): OpenState {
  try {
    const raw = window.localStorage.getItem(key(workspaceId, kind))
    if (!raw) return {}
    const parsed = JSON.parse(raw)
    if (parsed === null || typeof parsed !== 'object' || Array.isArray(parsed)) return {}
    const out: OpenState = {}
    for (const [k, v] of Object.entries(parsed)) if (typeof v === 'boolean') out[k] = v
    return out
  } catch {
    return {}
  }
}

export function writeOpenState(workspaceId: string, kind: string, state: OpenState): void {
  try {
    window.localStorage.setItem(key(workspaceId, kind), JSON.stringify(state))
  } catch {
    /* stockage indisponible : le pli n'est simplement pas mémorisé */
  }
}

// Ne garde que les dossiers qui existent encore (sinon le stockage grossit d'un dossier supprimé à l'autre).
export function pruneOpenState(state: OpenState, existingFolderIds: Iterable<string>): OpenState {
  const alive = new Set(existingFolderIds)
  const out: OpenState = {}
  for (const [k, v] of Object.entries(state)) if (alive.has(k)) out[k] = v
  return out
}
