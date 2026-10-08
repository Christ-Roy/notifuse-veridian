// Lot 5 : règles pures du tableau de bord de prospection. Aucune lecture réseau, aucun
// texte affiché (les libellés vivent dans les composants, via Lingui) : tout ce fichier
// se teste sans rendu.

import type { EmailProfilesOverview } from '../../services/api/veridian_email_profiles'
import { reputationIssues, type ReputationIssue } from '../sending_profiles/veridian_profile_rules'
import type { EmailSeriesDef } from '../analytics/email_metrics_series'

export type SendBucket = 'day' | 'hour'

// Envois par relais (profil d'envoi) : moteur analytics, schéma message_history,
// dimension veridian_profile_id, filtre message_type = commercial.
export const SENDS_BY_PROFILE_MEASURE = 'count_sent'
export const PROFILE_DIMENSION = 'veridian_profile_id'

// Rejets, désinscriptions, plaintes : chaque mesure se groupe sur SA date d'événement
// (les refus de politique n'ont pas de date de rejet : ils se datent de l'envoi).
export const REJECTION_SERIES: EmailSeriesDef[] = [
  { dimension: 'bounced_at', measures: ['count_bounced_hard', 'count_bounced_soft'] },
  { dimension: 'sent_at', measures: ['count_policy_refused'] },
  { dimension: 'complained_at', measures: ['count_complained'] },
  { dimension: 'unsubscribed_at', measures: ['count_unsubscribed'] }
]

export interface RejectionTotals {
  hard: number
  soft: number
  policy: number
  unsubscribed: number
  complained: number
}

const toNumber = (value: unknown): number => {
  if (typeof value === 'number') return Number.isFinite(value) ? value : 0
  if (typeof value === 'string') {
    const parsed = parseFloat(value)
    return Number.isNaN(parsed) ? 0 : parsed
  }
  return 0
}

export function sumMeasure(rows: Array<Record<string, unknown>> | undefined, measure: string): number {
  return (rows ?? []).reduce((acc, row) => acc + toNumber(row[measure]), 0)
}

// Une réponse par famille de mesures, dans l'ordre de REJECTION_SERIES.
export function rejectionTotals(responses: Array<Array<Record<string, unknown>> | undefined>): RejectionTotals {
  const [bounced, policy, complained, unsubscribed] = responses
  return {
    hard: sumMeasure(bounced, 'count_bounced_hard'),
    soft: sumMeasure(bounced, 'count_bounced_soft'),
    policy: sumMeasure(policy, 'count_policy_refused'),
    complained: sumMeasure(complained, 'count_complained'),
    unsubscribed: sumMeasure(unsubscribed, 'count_unsubscribed')
  }
}

// ── Envois par jour ou par heure, par relais ─────────────────────────────────

export interface SendsSeries {
  buckets: string[]
  series: Array<{ profileId: string; name: string; values: number[]; total: number }>
  total: number
}

export function hourSlots(): string[] {
  return Array.from({ length: 24 }, (_, hour) => String(hour).padStart(2, '0'))
}

// Jours AAAA-MM-JJ de start inclus à end EXCLU (le tableau de bord borne à demain).
export function daySlots(range: [string, string]): string[] {
  const slots: string[] = []
  const cursor = new Date(`${range[0]}T00:00:00Z`)
  const end = new Date(`${range[1]}T00:00:00Z`)
  if (Number.isNaN(cursor.getTime()) || Number.isNaN(end.getTime())) return slots
  while (cursor < end && slots.length < 400) {
    slots.push(cursor.toISOString().slice(0, 10))
    cursor.setUTCDate(cursor.getUTCDate() + 1)
  }
  return slots
}

// Date du jour (AAAA-MM-JJ) dans un fuseau IANA ; décalage en jours possible.
export function dayInTimezone(timezone: string, offsetDays = 0, now: Date = new Date()): string {
  let day: string
  try {
    day = new Intl.DateTimeFormat('en-CA', { timeZone: timezone || 'UTC' }).format(now)
  } catch {
    day = now.toISOString().slice(0, 10)
  }
  if (offsetDays === 0) return day
  const shifted = new Date(`${day}T00:00:00Z`)
  shifted.setUTCDate(shifted.getUTCDate() + offsetDays)
  return shifted.toISOString().slice(0, 10)
}

// Clé de seau d'une ligne du moteur analytics : « AAAA-MM-JJ » (jour) ou « HH » (heure).
// Le moteur rend la colonne temporelle dans le fuseau demandé.
export function bucketKey(raw: unknown, bucket: SendBucket): string {
  const text = String(raw ?? '')
  return bucket === 'day' ? text.slice(0, 10) : text.slice(11, 13)
}

export function buildSendsSeries(
  data: Array<Record<string, unknown>> | undefined,
  bucket: SendBucket,
  slots: string[],
  profileName: (profileId: string) => string
): SendsSeries {
  const timeField = `sent_at_${bucket}`
  const byProfile = new Map<string, Map<string, number>>()
  const known = new Set(slots)
  const buckets = [...slots]
  for (const row of data ?? []) {
    const key = bucketKey(row[timeField], bucket)
    if (!key) continue
    if (!known.has(key)) {
      known.add(key)
      buckets.push(key)
    }
    const profileId = String(row[PROFILE_DIMENSION] ?? '')
    const perBucket = byProfile.get(profileId) ?? new Map<string, number>()
    perBucket.set(key, (perBucket.get(key) ?? 0) + toNumber(row[SENDS_BY_PROFILE_MEASURE]))
    byProfile.set(profileId, perBucket)
  }
  buckets.sort()
  const series = [...byProfile.entries()].map(([profileId, perBucket]) => {
    const values = buckets.map((key) => perBucket.get(key) ?? 0)
    return { profileId, name: profileName(profileId), values, total: values.reduce((a, b) => a + b, 0) }
  })
  series.sort((a, b) => b.total - a.total || a.name.localeCompare(b.name))
  return { buckets, series, total: series.reduce((acc, s) => acc + s.total, 0) }
}

// ── Réputation par fournisseur destinataire : couples ralentis ou arrêtés ─────

export interface ReputationCouple {
  profileId: string
  profileName: string
  issue: ReputationIssue
}

// Seulement les profils commerciaux et les classes ralenties ou arrêtées, les arrêts
// d'abord, puis le ralentissement le plus fort. Aucun calcul : le plan du serveur fait foi.
export function reputationCouples(overview: EmailProfilesOverview | undefined): ReputationCouple[] {
  const couples: ReputationCouple[] = []
  for (const profile of overview?.profiles ?? []) {
    if (profile.usage !== 'commercial') continue
    for (const issue of reputationIssues(profile.plan)) {
      couples.push({ profileId: profile.integration_id, profileName: profile.name, issue })
    }
  }
  return couples.sort(
    (a, b) =>
      Number(b.issue.stopped) - Number(a.issue.stopped) ||
      b.issue.factor - a.issue.factor ||
      a.profileName.localeCompare(b.profileName)
  )
}

// ── Séquences ───────────────────────────────────────────────────────────────

// Sorties qui ne sont pas des réponses : rejet, désinscription, exclusion, autre.
export function nonReplyExits(exits: { rejected: number; unsubscribed: number; excluded: number; other: number }): number {
  return exits.rejected + exits.unsubscribed + exits.excluded + exits.other
}
