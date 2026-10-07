// Lot 3 : d'où vient la valeur d'un réglage. Le profil est la source; si un
// réglage y est vide et que le workspace en porte un, le worker retombe dessus
// (cascade backend : metadata de broadcast, profil, workspace). L'écran le DIT
// (« hérité du workspace ») au lieu de laisser le workspace primer en silence.

import type { EmailProvider, WorkspaceSettings } from '../../services/api/types'

export type SettingSource = 'profile' | 'workspace' | 'default'

export type InheritableSetting =
  | 'class_rates'
  | 'class_caps'
  | 'per_recipient_cap'
  | 'per_sender_cap'
  | 'sending_window'
  | 'jitter'
  | 'anti_hash'
  | 'anti_hash_window'
  | 'excluded_classes'

const hasEntries = (value: Record<string, unknown> | undefined | null): boolean =>
  !!value && Object.keys(value).length > 0

// Un réglage est « posé » selon la sémantique du backend : 0 = non posé pour les
// plafonds entiers, mais 0 est une valeur explicite pour le jitter (opt-out) et
// false pour l'anti-hash (pointeurs tri-états côté Go).
function profileHas(setting: InheritableSetting, profile: EmailProvider): boolean {
  switch (setting) {
    case 'class_rates':
      return hasEntries(profile.veridian_provider_class_rates)
    case 'class_caps':
      return hasEntries(profile.veridian_provider_class_daily_cap)
    case 'per_recipient_cap':
      return (profile.veridian_per_recipient_daily_cap ?? 0) > 0
    case 'per_sender_cap':
      return (profile.veridian_per_sender_daily_cap ?? 0) > 0
    case 'sending_window':
      return !!profile.veridian_sending_window
    case 'jitter':
      return profile.veridian_jitter_pct !== undefined && profile.veridian_jitter_pct !== null
    case 'anti_hash':
      return profile.veridian_anti_hash_enabled !== undefined && profile.veridian_anti_hash_enabled !== null
    case 'anti_hash_window':
      return (profile.veridian_anti_hash_window_hours ?? 0) > 0
    case 'excluded_classes':
      return (profile.veridian_excluded_provider_classes?.length ?? 0) > 0
  }
}

function workspaceHas(setting: InheritableSetting, ws: Partial<WorkspaceSettings>): boolean {
  switch (setting) {
    case 'class_rates':
      return hasEntries(ws.veridian_provider_class_rates)
    case 'class_caps':
      return hasEntries(ws.veridian_provider_class_daily_cap)
    case 'per_recipient_cap':
      return (ws.veridian_per_recipient_daily_cap ?? 0) > 0
    case 'per_sender_cap':
      return (ws.veridian_per_sender_daily_cap ?? 0) > 0
    case 'sending_window':
      return !!ws.veridian_sending_window
    case 'jitter':
      return ws.veridian_jitter_pct !== undefined && ws.veridian_jitter_pct !== null
    case 'anti_hash':
      return ws.veridian_anti_hash_enabled !== undefined && ws.veridian_anti_hash_enabled !== null
    case 'anti_hash_window':
      return (ws.veridian_anti_hash_window_hours ?? 0) > 0
    case 'excluded_classes':
      return (ws.veridian_excluded_provider_classes?.length ?? 0) > 0
  }
}

export function settingSource(
  setting: InheritableSetting,
  profile: EmailProvider,
  workspaceSettings: Partial<WorkspaceSettings>
): SettingSource {
  if (profileHas(setting, profile)) return 'profile'
  if (workspaceHas(setting, workspaceSettings)) return 'workspace'
  return 'default'
}

// Retire du profil la valeur d'un réglage : il retombe alors sur le workspace
// (ou le défaut). Les tables par classe se remplacent en bloc côté backend, d'où
// l'absence de remise à zéro « une classe à la fois ».
export function clearProfileSetting(profile: EmailProvider, setting: InheritableSetting): EmailProvider {
  const next = { ...profile }
  switch (setting) {
    case 'class_rates':
      delete next.veridian_provider_class_rates
      break
    case 'class_caps':
      delete next.veridian_provider_class_daily_cap
      break
    case 'per_recipient_cap':
      delete next.veridian_per_recipient_daily_cap
      break
    case 'per_sender_cap':
      delete next.veridian_per_sender_daily_cap
      break
    case 'sending_window':
      delete next.veridian_sending_window
      break
    case 'jitter':
      delete next.veridian_jitter_pct
      break
    case 'anti_hash':
      delete next.veridian_anti_hash_enabled
      break
    case 'anti_hash_window':
      delete next.veridian_anti_hash_window_hours
      break
    case 'excluded_classes':
      delete next.veridian_excluded_provider_classes
      break
  }
  return next
}

// Ce que le profil devient quand on « personnalise » une table par classe : une
// COPIE complète de la valeur héritée, pour qu'aucune classe ne perde sa limite
// en silence (la table du profil remplace celle du workspace en bloc).
export function seedClassTable(
  setting: 'class_rates' | 'class_caps',
  profile: EmailProvider,
  workspaceSettings: Partial<WorkspaceSettings>
): EmailProvider {
  const next = { ...profile }
  if (setting === 'class_rates') {
    next.veridian_provider_class_rates = {
      ...(workspaceSettings.veridian_provider_class_rates ?? {})
    } as EmailProvider['veridian_provider_class_rates']
  } else {
    next.veridian_provider_class_daily_cap = {
      ...(workspaceSettings.veridian_provider_class_daily_cap ?? {})
    } as EmailProvider['veridian_provider_class_daily_cap']
  }
  return next
}
