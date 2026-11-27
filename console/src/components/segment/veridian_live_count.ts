// Compte en direct de l'éditeur de segment (reprise amont 37.0) : chaque changement de l'arbre relance
// l'aperçu après une pause. Pendant le calcul, le dernier compte reste disponible ; une condition encore
// incomplète (erreur serveur) ne l'efface pas.
import { useEffect, useRef, useState } from 'react'
import {
  previewSegment,
  type PreviewSegmentRequest,
  type PreviewSegmentResponse,
  type TreeNode
} from '../../services/api/segment'

export const LIVE_COUNT_DELAY_MS = 800

export interface LiveSegmentCount {
  response?: PreviewSegmentResponse
  // Clé de la dernière requête réussie (JSON de la requête), pour comparer l'arbre affiché
  requestKey?: string
  pending: boolean
}

export function useLiveSegmentCount(
  tree: TreeNode | undefined,
  workspaceId: string | undefined,
  canCount: boolean
): LiveSegmentCount {
  const [state, setState] = useState<LiveSegmentCount>({ pending: false })
  const lastKey = useRef<string | undefined>(undefined)

  // Déclenché par le contenu de l'arbre, pas par sa référence
  const treeKey = JSON.stringify(tree ?? null)

  useEffect(() => {
    if (!workspaceId || !canCount || !tree) {
      setState((s) => ({ ...s, pending: false }))
      return
    }
    const requestData: PreviewSegmentRequest = { workspace_id: workspaceId, tree, limit: 100 }
    const key = JSON.stringify(requestData)
    if (key === lastKey.current) {
      setState((s) => ({ ...s, pending: false }))
      return
    }
    let cancelled = false
    setState((s) => ({ ...s, pending: true }))
    const timer = setTimeout(async () => {
      try {
        const response = await previewSegment(requestData)
        if (cancelled) return
        lastKey.current = key
        setState({ response, requestKey: key, pending: false })
      } catch {
        // Condition incomplète : on garde le dernier compte
        if (!cancelled) setState((s) => ({ ...s, pending: false }))
      }
    }, LIVE_COUNT_DELAY_MS)
    return () => {
      cancelled = true
      clearTimeout(timer)
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps -- seul le contenu de l'arbre déclenche le calcul
  }, [treeKey, workspaceId, canCount])

  return state
}
