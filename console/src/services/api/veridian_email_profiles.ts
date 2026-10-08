import { api } from './client'
export interface EmailProfileUsage {
  integration_id: string
  // `used` is quota capacity consumed. It may be greater than accepted_used
  // when SMTP returned an ambiguous result after DATA.
  used: number
  accepted_used: number
  cap: number
  remaining: number
  // Keep unknown future backend classes visible instead of silently folding
  // them into a misleading canonical bucket.
  accepted_by_provider_class: Record<string, number>
}

export interface EmailProfilesUsageResponse {
  date: string
  total_used: number
  total_accepted: number
  profiles: EmailProfileUsage[]
}

export const emailProfilesUsageService = {
  get: (workspaceId: string) => {
    const searchParams = new URLSearchParams({ workspace_id: workspaceId })
    return api.get<EmailProfilesUsageResponse>(
      `/api/veridian/emailProfiles.usage?${searchParams.toString()}`
    )
  }
}

// ── Lot 3 : page « Profils d'envoi » ─────────────────────────────────────────
// Miroir de internal/domain/veridian_email_profile_overview.go et
// veridian_effective_plan.go. Aucun secret ne transite par ces types.

export type EmailProfileType = 'smtp' | 'gmail_app_password' | 'gmail_oauth' | string
// unassigned : créé, ni dans la rotation ni transactionnel (traité comme commercial hors rotation).
export type EmailProfileUsageKind = 'commercial' | 'transactional' | 'unassigned'

// Portes de plafond journalier (VeridianPlanGate*).
export type EmailProfilePlanGate =
  | 'warmup'
  | 'profile_cap'
  | 'per_sender'
  | 'class_cap'
  | 'none'
  | string

// Raisons de blocage d'un envoi maintenant (VeridianPlanBlock*).
export type EmailProfilePlanBlock =
  | 'paused'
  | 'not_in_rotation'
  | 'unverified'
  | 'window_closed'
  | 'excluded_class'
  | 'reputation_stopped'
  | 'warmup'
  | 'profile_cap'
  | 'per_sender'
  | 'class_cap'
  | string

export interface EmailProfilePlanGateRow {
  name: EmailProfilePlanGate
  cap: number
  used: number
  remaining: number
  detail?: string
}

export interface EmailProfilePlanClass {
  class: string
  excluded: boolean
  rate_per_min: number
  rate_configured: number
  daily_cap: number | null
  daily_cap_configured: number | null
  sent_today: number
  remaining: number | null
  slowdown_factor: number
  // hard_bounce_rate | policy_refusal_rate | complaint | bulk_policy_refusal
  slowdown_reason?: string
  // Taux (0 à 1) qui a déclenché le ralentissement (rejets durs ou refus 5.7.x).
  slowdown_rate?: number
  sent_7d?: number
  stopped: boolean
  sendable_now: boolean
  blocked_by?: EmailProfilePlanBlock
}

export interface EmailProfilePlanWindow {
  configured: boolean
  open_now: boolean
  next_open_at?: string
  days?: number[]
  start_hour?: number
  end_hour?: number
  timezone?: string
  source: string
}

export interface EmailProfilePlanWarmup {
  active: boolean
  day?: number
  of?: number
  cap_today?: number
  started_at?: string
}

export interface EmailProfilePlan {
  date: string
  applicable: boolean
  mode: EmailProfileUsageKind
  paused: boolean
  daily_cap_today: number | null
  limiting_gate: EmailProfilePlanGate
  limiting_detail?: string
  sent_today: number
  reserved_today: number
  remaining_today: number | null
  remaining_gate: EmailProfilePlanGate
  gates: EmailProfilePlanGateRow[]
  per_recipient_daily_cap: number
  per_sender_daily_cap: number
  profile_daily_cap: number
  native_rate_per_min: number
  class_caps_source: string
  class_rates_source: string
  warmup: EmailProfilePlanWarmup
  window: EmailProfilePlanWindow
  excluded_classes: string[]
  sender_domains: string[]
  domain_slowdown_factor: number
  complaints_7d: number
  reputation_alert: boolean
  classes: EmailProfilePlanClass[]
  sendable_now: boolean
  blocked_by: EmailProfilePlanBlock[]
}

export interface EmailProfileSender {
  email: string
  name: string
  is_default: boolean
}

export interface EmailProfileInbox {
  integration_id: string
  name: string
  host: string
  address: string
  folder?: string
  linked_profiles: string[]
}

export interface EmailProfileOverview {
  integration_id: string
  name: string
  kind: string
  type: EmailProfileType
  usage: EmailProfileUsageKind
  in_rotation: boolean
  paused: boolean
  verified: boolean
  verified_at: string | null
  credentials_configured: boolean
  senders: EmailProfileSender[]
  return_inbox: EmailProfileInbox | null
  plan: EmailProfilePlan
}

export interface EmailProfilesOverview {
  date: string
  generated_at: string
  timezone: string
  profiles: EmailProfileOverview[]
  global_inboxes: EmailProfileInbox[]
  usage_conflicts: string[]
  totals: {
    commercial_sent_today: number
    transactional_sent_today: number
    commercial_capacity_today: number | null
    active_commercial_profiles: number
    paused_profiles: number
  }
}

export interface CreateEmailProfileRequest {
  workspace_id: string
  type: 'smtp_imap' | 'gmail_app_password'
  name: string
  sender_email: string
  sender_name: string
  smtp?: { host: string; port: number; use_tls: boolean; username: string; password: string }
  imap?: {
    host: string
    port: number
    use_tls: boolean
    username: string
    password: string
    folder?: string
  }
  // Gmail seulement : saisi une fois, jamais relu ni renvoyé.
  app_password?: string
  gmail_account_type?: 'personal' | 'workspace'
  profile_daily_cap?: number
}

export interface CreateEmailProfileResponse {
  integration_id: string
  imap_integration_id?: string
}

export const emailProfilesOverviewService = {
  get: (workspaceId: string) => {
    const searchParams = new URLSearchParams({ workspace_id: workspaceId })
    return api.get<EmailProfilesOverview>(
      `/api/veridian/emailProfiles.overview?${searchParams.toString()}`
    )
  }
}

export const emailProfilesCreateService = {
  create: (request: CreateEmailProfileRequest) =>
    api.post<CreateEmailProfileResponse>('/api/veridian/emailProfiles.create', request)
}

// Lot 4 : usage et pause passent par une API dediee. Le serveur valide l exclusivite
// (un profil est commercial OU transactionnel) et applique tout en une ecriture ; un
// refus est un 400 {"error": "message lisible"} que ApiError porte tel quel.
export interface SetEmailProfileUsageRequest {
  workspace_id: string
  integration_id: string
  usage: EmailProfileUsageKind
}

export interface SetEmailProfileUsageResponse {
  integration_id: string
  usage: EmailProfileUsageKind
  in_rotation: boolean
  paused: boolean
  // Identifiants du pool commercial apres l ecriture
  rotation: string[]
  // Profil transactionnel apres l ecriture ; vide s il n y en a pas
  transactional_integration_id: string
}

export interface EmailProfilePauseRequest {
  workspace_id: string
  integration_id: string
}

export interface EmailProfilePauseResponse {
  integration_id: string
  paused: boolean
}

export const emailProfilesStateService = {
  setUsage: (request: SetEmailProfileUsageRequest) =>
    api.post<SetEmailProfileUsageResponse>('/api/veridian/emailProfiles.setUsage', request),
  pause: (request: EmailProfilePauseRequest) =>
    api.post<EmailProfilePauseResponse>('/api/veridian/emailProfiles.pause', request),
  resume: (request: EmailProfilePauseRequest) =>
    api.post<EmailProfilePauseResponse>('/api/veridian/emailProfiles.resume', request)
}
