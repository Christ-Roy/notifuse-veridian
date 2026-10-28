// Colonne « Segments » de la liste des contacts (fork Veridian).
// Module pur (aucun import React) : choisit les pastilles visibles et celles repliées sous « +N ».
// Règle : au plus 2 pastilles, une par dossier de premier niveau (ordre naturel des dossiers),
// avec pour chaque dossier le segment au premier rang de l'ordre naturel. Avec un seul dossier
// (ou aucun), on complète par les suivants dans l'ordre naturel.
import { parseSegmentPath } from '../segment/veridian_segment_folders'

export const MAX_VISIBLE_SEGMENTS = 2
export const DEFAULT_SEGMENT_COLOR = '#1890ff'

export interface ContactSegmentEntry {
  segment_id: string
  // Nom complet (chemin « Dossier / Sous-dossier / Nom »). Absent : on affiche l'identifiant.
  name?: string
  color?: string
  matched_at?: string
  version?: number
}

export interface ContactSegmentView {
  segment_id: string
  // Chemin complet (ou l'identifiant en repli).
  name: string
  // Dernier élément du chemin.
  shortName: string
  // Dossier de premier niveau, absent pour un segment à la racine.
  folder?: string
  color: string
  matched_at?: string
  version?: number
}

export interface ContactSegmentSelection {
  visible: ContactSegmentView[]
  hidden: ContactSegmentView[]
}

const naturalCollator = new Intl.Collator('fr', { numeric: true, sensitivity: 'base' })

function comparePaths(a: string[], b: string[]): number {
  const common = Math.min(a.length, b.length)
  for (let i = 0; i < common; i += 1) {
    const diff = naturalCollator.compare(a[i], b[i])
    if (diff !== 0) return diff
  }
  return a.length - b.length
}

interface Ranked {
  view: ContactSegmentView
  path: string[]
}

export function pickContactSegments(
  entries: ContactSegmentEntry[] | undefined | null
): ContactSegmentSelection {
  if (!entries || entries.length === 0) return { visible: [], hidden: [] }

  const ranked: Ranked[] = entries.map((entry) => {
    const name = entry.name || entry.segment_id
    const path = parseSegmentPath(name)
    return {
      path,
      view: {
        segment_id: entry.segment_id,
        name,
        shortName: path[path.length - 1],
        folder: path.length > 1 ? path[0] : undefined,
        color: entry.color || DEFAULT_SEGMENT_COLOR,
        matched_at: entry.matched_at,
        version: entry.version
      }
    }
  })
  // Tri stable : à chemin égal, l'ordre reçu est conservé.
  const sorted = [...ranked].sort((a, b) => comparePaths(a.path, b.path))

  // Un représentant par dossier de premier niveau : le premier de l'ordre naturel.
  const chosen = new Set<Ranked>()
  const seenFolders = new Set<string>()
  for (const item of sorted) {
    const folder = item.view.folder
    if (folder === undefined || seenFolders.has(folder)) continue
    seenFolders.add(folder)
    if (chosen.size < MAX_VISIBLE_SEGMENTS) chosen.add(item)
  }
  // Complément dans l'ordre naturel (un seul dossier, ou aucun).
  for (const item of sorted) {
    if (chosen.size >= MAX_VISIBLE_SEGMENTS) break
    chosen.add(item)
  }

  return {
    visible: sorted.filter((item) => chosen.has(item)).map((item) => item.view),
    hidden: sorted.filter((item) => !chosen.has(item)).map((item) => item.view)
  }
}
