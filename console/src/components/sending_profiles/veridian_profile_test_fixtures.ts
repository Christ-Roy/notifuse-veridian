// Fixtures des tests du lot 3 : forme exacte de emailProfiles.overview (relevée sur
// `notifuse profiles:overview`). Aucun secret.

import type {
  EmailProfileOverview,
  EmailProfilePlan,
  EmailProfilePlanClass,
  EmailProfilesOverview
} from '../../services/api/veridian_email_profiles'

export const planClass = (cls: string, overrides: Partial<EmailProfilePlanClass> = {}): EmailProfilePlanClass => ({
  class: cls,
  excluded: false,
  rate_per_min: 0.5,
  rate_configured: 0.5,
  daily_cap: 150,
  daily_cap_configured: 150,
  sent_today: 0,
  remaining: 150,
  slowdown_factor: 1,
  stopped: false,
  sendable_now: true,
  ...overrides
})

export const basePlan = (overrides: Partial<EmailProfilePlan> = {}): EmailProfilePlan => ({
  date: '2026-10-07',
  applicable: true,
  mode: 'commercial',
  paused: false,
  daily_cap_today: 300,
  limiting_gate: 'warmup',
  limiting_detail: 'domaine envoi.example, toutes classes',
  sent_today: 149,
  reserved_today: 149,
  remaining_today: 151,
  remaining_gate: 'warmup',
  gates: [],
  per_recipient_daily_cap: 1,
  per_sender_daily_cap: 300,
  profile_daily_cap: 300,
  native_rate_per_min: 6,
  class_caps_source: 'profile',
  class_rates_source: 'profile',
  warmup: { active: true, day: 3, of: 5, cap_today: 300, started_at: '2026-10-05T00:00:00Z' },
  window: { configured: false, open_now: true, source: 'none' },
  excluded_classes: [],
  sender_domains: ['envoi.example'],
  domain_slowdown_factor: 1,
  complaints_7d: 0,
  reputation_alert: false,
  classes: [planClass('google'), planClass('microsoft'), planClass('security_gateway')],
  sendable_now: true,
  blocked_by: [],
  ...overrides
})

export const profile = (
  id: string,
  overrides: Partial<EmailProfileOverview> = {},
  plan: Partial<EmailProfilePlan> = {}
): EmailProfileOverview => ({
  integration_id: id,
  name: `Profil ${id}`,
  kind: 'smtp',
  type: 'smtp',
  usage: 'commercial',
  in_rotation: true,
  paused: false,
  verified: true,
  verified_at: '2026-10-04T07:34:34Z',
  credentials_configured: true,
  senders: [{ email: `robert@${id}.example`, name: 'Robert Brunon', is_default: true }],
  return_inbox: null,
  plan: basePlan(plan),
  ...overrides
})

export const overviewOf = (
  profiles: EmailProfileOverview[],
  overrides: Partial<EmailProfilesOverview> = {}
): EmailProfilesOverview => ({
  date: '2026-10-07',
  generated_at: '2026-10-07T19:28:00Z',
  timezone: 'UTC',
  profiles,
  global_inboxes: [],
  usage_conflicts: [],
  totals: {
    commercial_sent_today: 319,
    transactional_sent_today: 0,
    commercial_capacity_today: 600,
    active_commercial_profiles: 3,
    paused_profiles: 0
  },
  ...overrides
})
