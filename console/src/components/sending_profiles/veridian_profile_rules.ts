// Lot 3 « page Profils d'envoi » (08/10/2026) : lecture des règles d'un profil.
//
// Fonctions PURES : elles transforment le contrat `emailProfiles.overview` (la
// vérité du worker, calculée côté serveur par EffectivePlan) en données
// d'affichage. Aucune règle n'est recalculée ici : la console ne devine ni le
// plafond, ni la porte limitante, ni le ralentissement. Les libellés (t`...`)
// sont posés par les composants, jamais ici (piège Lingui : un `t` passé en
// paramètre d'une fonction hors composant rend une chaîne vide).

import type {
  EmailProfileOverview,
  EmailProfilePlan,
  EmailProfilePlanClass,
  EmailProfilePlanWindow
} from '../../services/api/veridian_email_profiles'

export type LimitingFactor =
  | { kind: 'warmup'; day: number; of: number }
  | { kind: 'profile_cap' }
  | { kind: 'per_sender' }
  | { kind: 'class_cap' }
  | { kind: 'none' }

// Porte qui fixe le plafond du jour. « Chauffe » porte son jour (3/5).
export function limitingFactor(plan: EmailProfilePlan): LimitingFactor {
  switch (plan.limiting_gate) {
    case 'warmup':
      return { kind: 'warmup', day: plan.warmup.day ?? 1, of: plan.warmup.of ?? 1 }
    case 'profile_cap':
      return { kind: 'profile_cap' }
    case 'per_sender':
      return { kind: 'per_sender' }
    case 'class_cap':
      return { kind: 'class_cap' }
    default:
      return { kind: 'none' }
  }
}

export interface WindowReopening {
  // Heure locale de réouverture dans le fuseau de la fenêtre, « 08:00 ».
  time: string
  // Jour de la réouverture dans le fuseau de la fenêtre (0 = dimanche), et
  // nombre de jours calendaires qui séparent ce jour d'aujourd'hui (0 = aujourd'hui).
  weekday: number
  daysAhead: number
}

const WEEKDAY_INDEX: Record<string, number> = {
  Sun: 0,
  Mon: 1,
  Tue: 2,
  Wed: 3,
  Thu: 4,
  Fri: 5,
  Sat: 6
}

function zonedParts(date: Date, timeZone: string) {
  const parts = new Intl.DateTimeFormat('en-US', {
    timeZone,
    weekday: 'short',
    year: 'numeric',
    month: '2-digit',
    day: '2-digit',
    hour: '2-digit',
    minute: '2-digit',
    hourCycle: 'h23'
  }).formatToParts(date)
  const get = (type: string) => parts.find((p) => p.type === type)?.value ?? ''
  return {
    weekday: WEEKDAY_INDEX[get('weekday')] ?? 0,
    ymd: `${get('year')}-${get('month')}-${get('day')}`,
    time: `${get('hour')}:${get('minute')}`
  }
}

// Quand la fenêtre d'envoi rouvre, lu dans SON fuseau. null si elle n'est pas
// fermée ou si le serveur ne donne pas la prochaine ouverture.
export function windowReopening(
  window: EmailProfilePlanWindow,
  now: Date = new Date()
): WindowReopening | null {
  if (!window.configured || window.open_now || !window.next_open_at) return null
  const next = new Date(window.next_open_at)
  if (Number.isNaN(next.getTime())) return null
  const timeZone = window.timezone || 'UTC'
  let nextParts: ReturnType<typeof zonedParts>
  let nowParts: ReturnType<typeof zonedParts>
  try {
    nextParts = zonedParts(next, timeZone)
    nowParts = zonedParts(now, timeZone)
  } catch {
    // Fuseau inconnu du navigateur : on retombe sur UTC plutôt que d'afficher faux sans le dire.
    nextParts = zonedParts(next, 'UTC')
    nowParts = zonedParts(now, 'UTC')
  }
  const dayMs = 24 * 3600 * 1000
  const daysAhead = Math.round(
    (Date.parse(`${nextParts.ymd}T00:00:00Z`) - Date.parse(`${nowParts.ymd}T00:00:00Z`)) / dayMs
  )
  return { time: nextParts.time, weekday: nextParts.weekday, daysAhead: Math.max(0, daysAhead) }
}

export type Blocker =
  | { kind: 'paused' }
  | { kind: 'unverified' }
  | { kind: 'not_in_rotation' }
  | { kind: 'window_closed'; reopening: WindowReopening | null }
  | { kind: 'reputation_stopped' }

// Ce qui empêche le profil d'envoyer MAINTENANT (hors plafond : le plafond a sa
// propre ligne). Ordre d'affichage : le plus structurant d'abord.
export function profileBlockers(
  profile: EmailProfileOverview,
  now: Date = new Date()
): Blocker[] {
  const plan = profile.plan
  if (!plan.applicable) return []
  const out: Blocker[] = []
  const has = (reason: string) => plan.blocked_by.includes(reason)
  if (has('paused') || profile.paused) out.push({ kind: 'paused' })
  if (has('unverified') || !profile.verified) out.push({ kind: 'unverified' })
  else if (has('not_in_rotation')) out.push({ kind: 'not_in_rotation' })
  if (has('window_closed')) out.push({ kind: 'window_closed', reopening: windowReopening(plan.window, now) })
  if (plan.classes.some((c) => c.stopped)) out.push({ kind: 'reputation_stopped' })
  return out
}

export interface TodayProgress {
  sent: number
  reserved: number
  // null = aucun plafond configuré.
  cap: number | null
  // 0 à 100, null sans plafond.
  percent: number | null
}

export function todayProgress(plan: EmailProfilePlan): TodayProgress {
  const cap = plan.daily_cap_today
  const percent = cap === null ? null : cap <= 0 ? 100 : Math.min(100, Math.round((plan.sent_today / cap) * 100))
  return { sent: plan.sent_today, reserved: plan.reserved_today, cap, percent }
}

export interface ReputationIssue {
  class: string
  factor: number
  stopped: boolean
  // hard_bounce_rate | policy_refusal_rate | complaint | bulk_policy_refusal
  reason: string
  // Taux (0 à 1) quand la raison en porte un, sinon null.
  rate: number | null
  sent7d: number
}

// Seulement les classes ralenties ou arrêtées. Les classes saines n'apparaissent pas.
export function reputationIssues(plan: EmailProfilePlan): ReputationIssue[] {
  return plan.classes
    .filter((c: EmailProfilePlanClass) => c.slowdown_factor > 1 || c.stopped)
    .map((c) => ({
      class: c.class,
      factor: c.slowdown_factor,
      stopped: c.stopped,
      reason: c.slowdown_reason ?? '',
      rate: c.slowdown_rate && c.slowdown_rate > 0 ? c.slowdown_rate : null,
      sent7d: c.sent_7d ?? 0
    }))
}

// Classes que le profil exclut volontairement (levier d'exclusion), triées.
export function excludedClassesOf(plan: EmailProfilePlan): string[] {
  return [...(plan.excluded_classes ?? [])].sort()
}

export type RotationAction =
  | { allowed: true }
  | { allowed: false; reason: 'transactional' | 'unverified' | 'last_profile' }

// Ajouter à la rotation commerciale : refusé pour un profil réservé au
// transactionnel (exclusivité) ou non vérifié (serveur : un profil non testé est refusé).
export function canAddToRotation(profile: EmailProfileOverview): RotationAction {
  if (profile.usage === 'transactional') return { allowed: false, reason: 'transactional' }
  if (!profile.verified) return { allowed: false, reason: 'unverified' }
  return { allowed: true }
}

// Retirer de la rotation : refusé pour le dernier profil de la rotation (la rotation ne
// peut pas être vide : withMarketingProfileRotation lève une erreur).
export function canRemoveFromRotation(
  profile: EmailProfileOverview,
  all: EmailProfileOverview[]
): RotationAction {
  const inRotation = all.filter((p) => p.in_rotation && p.usage === 'commercial')
  if (inRotation.length <= 1) return { allowed: false, reason: 'last_profile' }
  void profile
  return { allowed: true }
}

export interface ProfileGroups {
  commercial: EmailProfileOverview[]
  transactional: EmailProfileOverview[]
}

// Commercial : les profils en rotation d'abord, puis les autres. Un profil n'est
// jamais dans les deux groupes (exclusivité), `usage` fait foi.
export function groupProfiles(profiles: EmailProfileOverview[]): ProfileGroups {
  const commercial = profiles
    .filter((p) => p.usage !== 'transactional')
    .sort((a, b) => Number(b.in_rotation) - Number(a.in_rotation) || a.name.localeCompare(b.name))
  const transactional = profiles
    .filter((p) => p.usage === 'transactional')
    .sort((a, b) => a.name.localeCompare(b.name))
  return { commercial, transactional }
}
