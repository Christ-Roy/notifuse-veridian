// LOT 1 : règles pures de la page « File d'envoi ». Aucune lecture réseau, aucun texte
// affiché (les libellés vivent dans veridian_queue_labels.ts) : tout se teste sans rendu.

import type { QueueGroup, QueueOrphans } from '../../services/api/veridian_queue_explain'

export interface ReasonTotal {
  reason: string
  count: number
  never_examined: number
  oldest: string | null
  next_min: string | null
  next_max: string | null
}

const minDate = (a: string | null, b: string | null): string | null => {
  if (!a) return b
  if (!b) return a
  return new Date(a).getTime() <= new Date(b).getTime() ? a : b
}
const maxDate = (a: string | null, b: string | null): string | null => {
  if (!a) return b
  if (!b) return a
  return new Date(a).getTime() >= new Date(b).getTime() ? a : b
}

// Regroupement par raison : compteurs, jamais examinées, plus ancienne, prochaine tentative
// (min..max). Trié du plus gros au plus petit.
export function reasonTotals(groups: QueueGroup[]): ReasonTotal[] {
  const byReason = new Map<string, ReasonTotal>()
  for (const g of groups) {
    const key = g.reason || ''
    const cur =
      byReason.get(key) ??
      ({ reason: key, count: 0, never_examined: 0, oldest: null, next_min: null, next_max: null } as ReasonTotal)
    cur.count += g.count
    cur.never_examined += g.never_examined
    cur.oldest = minDate(cur.oldest, g.oldest_created_at)
    cur.next_min = minDate(cur.next_min, g.next_attempt_min)
    cur.next_max = maxDate(cur.next_max, g.next_attempt_max)
    byReason.set(key, cur)
  }
  return [...byReason.values()].sort((a, b) => b.count - a.count || a.reason.localeCompare(b.reason))
}

export type TreeLevel = 'automation' | 'node' | 'reason' | 'profile'

export interface QueueTreeRow {
  key: string
  level: TreeLevel
  // Valeur brute du niveau (id d'automation, id de nœud, code de raison, id de profil)
  value: string
  // Nom lisible quand le serveur en donne un (automation, profil)
  name: string
  // Filtres cumulés depuis la racine : servent aux actions bornées (recalcul)
  automation_id: string
  node_id?: string
  reason?: string
  profile_id?: string
  reason_detail: string
  count: number
  never_examined: number
  oldest: string | null
  next_min: string | null
  next_max: string | null
  sample_entry_ids: string[]
  children?: QueueTreeRow[]
}

const LEVELS: TreeLevel[] = ['automation', 'node', 'reason', 'profile']

function levelValue(g: QueueGroup, level: TreeLevel): { value: string; name: string } {
  switch (level) {
    case 'automation':
      return { value: g.automation_id, name: g.automation_name }
    case 'node':
      return { value: g.node_id, name: '' }
    case 'reason':
      return { value: g.reason, name: '' }
    default:
      return { value: g.profile_id, name: g.profile_name }
  }
}

// Arbre automation > nœud > raison > profil. Les agrégats d'un parent sont la somme de ses
// enfants (min / max pour les dates). Les échantillons ne vivent que sur les feuilles.
export function buildQueueTree(groups: QueueGroup[]): QueueTreeRow[] {
  const roots: QueueTreeRow[] = []
  const index = new Map<string, QueueTreeRow>()

  for (const g of groups) {
    let siblings = roots
    let path = ''
    const filters: Pick<QueueTreeRow, 'automation_id' | 'node_id' | 'reason' | 'profile_id'> = {
      automation_id: g.automation_id
    }
    LEVELS.forEach((level, depth) => {
      const { value, name } = levelValue(g, level)
      path = `${path}/${level}:${value}`
      if (level === 'node') filters.node_id = value || undefined
      if (level === 'reason') filters.reason = value || undefined
      if (level === 'profile') filters.profile_id = value || undefined
      let row = index.get(path)
      if (!row) {
        row = {
          key: path,
          level,
          value,
          name,
          ...filters,
          reason_detail: '',
          count: 0,
          never_examined: 0,
          oldest: null,
          next_min: null,
          next_max: null,
          sample_entry_ids: []
        }
        index.set(path, row)
        siblings.push(row)
      }
      row.count += g.count
      row.never_examined += g.never_examined
      row.oldest = minDate(row.oldest, g.oldest_created_at)
      row.next_min = minDate(row.next_min, g.next_attempt_min)
      row.next_max = maxDate(row.next_max, g.next_attempt_max)
      if (level === 'reason' && !row.reason_detail) row.reason_detail = g.reason_detail
      if (depth === LEVELS.length - 1) {
        row.sample_entry_ids = [...new Set([...row.sample_entry_ids, ...g.sample_entry_ids])]
        row.reason_detail = row.reason_detail || g.reason_detail
      } else {
        row.children = row.children ?? []
        siblings = row.children
      }
    })
  }

  const sortRows = (rows: QueueTreeRow[]) => {
    rows.sort((a, b) => b.count - a.count)
    rows.forEach((r) => r.children && sortRows(r.children))
  }
  sortRows(roots)
  return roots
}

// Clés des lignes à déplier au premier affichage : automations, nœuds et raisons, pour que
// les profils (feuilles, avec leur échantillon) soient visibles sans cliquer trois fois.
export function defaultExpandedKeys(rows: QueueTreeRow[]): string[] {
  const keys: string[] = []
  const walk = (list: QueueTreeRow[]) => {
    for (const r of list) {
      if (r.children && r.children.length > 0) {
        keys.push(r.key)
        walk(r.children)
      }
    }
  }
  walk(rows)
  return keys
}

export interface FilterOptions {
  automations: Array<{ id: string; name: string }>
  nodes: string[]
  profiles: Array<{ id: string; name: string }>
  classes: string[]
}

// Valeurs proposées aux filtres, relevées dans les groupes vus (et conservées d'un
// chargement à l'autre par l'appelant).
export function collectFilterOptions(groups: QueueGroup[], previous?: FilterOptions): FilterOptions {
  const automations = new Map((previous?.automations ?? []).map((a) => [a.id, a.name]))
  const nodes = new Set(previous?.nodes ?? [])
  const profiles = new Map((previous?.profiles ?? []).map((p) => [p.id, p.name]))
  const classes = new Set(previous?.classes ?? [])
  for (const g of groups) {
    if (g.automation_id) automations.set(g.automation_id, g.automation_name || g.automation_id)
    if (g.node_id) nodes.add(g.node_id)
    if (g.profile_id) profiles.set(g.profile_id, g.profile_name || g.profile_id)
    if (g.class) classes.add(g.class)
  }
  return {
    automations: [...automations].map(([id, name]) => ({ id, name })),
    nodes: [...nodes].sort(),
    profiles: [...profiles].map(([id, name]) => ({ id, name })),
    classes: [...classes].sort()
  }
}

export function hasOrphans(orphans: QueueOrphans | null | undefined): boolean {
  return !!orphans && orphans.count > 0
}

export type VerdictColor = 'green' | 'red' | 'orange' | 'default'

export function verdictColor(verdict: string): VerdictColor {
  switch (verdict) {
    case 'pass':
      return 'green'
    case 'block':
      return 'red'
    case 'slowed':
      return 'orange'
    default:
      return 'default'
  }
}

export type OutcomeColor = 'green' | 'red' | 'orange' | 'default' | 'blue'

// Issue d'un candidat ou d'une décision.
export function outcomeColor(outcome: string): OutcomeColor {
  switch (outcome) {
    case 'selected':
    case 'sent':
      return 'green'
    case 'blocked':
    case 'failed':
    case 'circuit_open':
      return 'red'
    case 'deferred':
    case 'paused':
      return 'orange'
    case 'recomputed':
      return 'blue'
    default:
      return 'default'
  }
}

// Valeur ou limite d'une porte : tout JSON, rendu sans jamais lever.
export function formatGateValue(value: unknown): string {
  if (value === null || value === undefined) return '—'
  if (typeof value === 'string') return value
  if (typeof value === 'number' || typeof value === 'boolean') return String(value)
  try {
    return JSON.stringify(value)
  } catch {
    return String(value)
  }
}

// 165600 s -> « 2 d 3 h » ; Intl gère l'unité et la langue.
export function formatDelaySeconds(seconds: number | undefined | null, locale: string): string {
  if (seconds === undefined || seconds === null || !Number.isFinite(seconds) || seconds <= 0) return '—'
  const parts: Array<[number, 'day' | 'hour' | 'minute' | 'second']> = []
  let rest = Math.round(seconds)
  const d = Math.floor(rest / 86400)
  rest -= d * 86400
  const h = Math.floor(rest / 3600)
  rest -= h * 3600
  const m = Math.floor(rest / 60)
  rest -= m * 60
  if (d) parts.push([d, 'day'])
  if (h) parts.push([h, 'hour'])
  if (m && !d) parts.push([m, 'minute'])
  if (rest && !d && !h) parts.push([rest, 'second'])
  return parts
    .slice(0, 2)
    .map(([v, unit]) => new Intl.NumberFormat(locale, { style: 'unit', unit, unitDisplay: 'narrow' }).format(v))
    .join(' ')
}
