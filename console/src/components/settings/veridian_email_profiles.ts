import type {
  EmailProvider,
  Sender,
  SMTPSettings,
  WorkspaceSettings
} from '../../services/api/types'

export type EmailProfileMode = 'gmail_app_password' | 'gmail_oauth' | 'smtp_advanced'

export const GMAIL_PERSONAL_DEFAULT_DAILY_CAP = 30
// Plafonds relevés le 2026-10-03 (mission plafonds Gmail) : Google documente
// ~500 destinataires/jour pour un Gmail personnel et ~2000/jour pour un compte
// Google Workspace. On reste sous ces seuils (marge de sécurité) tout en
// laissant largement monter au-dessus de l'ancien plafond fixe (50) une fois
// la chauffe établie. Le défaut (30/jour) ne change pas.
export const GMAIL_PERSONAL_MAX_DAILY_CAP = 450
export const GMAIL_WORKSPACE_MAX_DAILY_CAP = 1800
export type GmailAccountType = 'personal' | 'workspace'
export const gmailAccountTypeMaxDailyCap = (accountType?: GmailAccountType): number =>
  accountType === 'workspace' ? GMAIL_WORKSPACE_MAX_DAILY_CAP : GMAIL_PERSONAL_MAX_DAILY_CAP

export interface GmailAppPasswordProfileInput {
  email: string
  senderName: string
  appPassword?: string
  rateLimitPerMinute?: number
  profileDailyCap?: number
  accountType?: GmailAccountType
  existingSenders?: Sender[]
  existingProvider?: EmailProvider
}

export const GMAIL_SMTP_SETTINGS: Pick<SMTPSettings, 'host' | 'port' | 'use_tls' | 'auth_type'> = {
  host: 'smtp.gmail.com',
  port: 587,
  use_tls: true,
  auth_type: 'basic'
}

export const inferEmailProfileMode = (provider?: EmailProvider): EmailProfileMode => {
  if (
    provider?.kind === 'smtp' &&
    provider.smtp?.auth_type === 'oauth2' &&
    provider.smtp.oauth2_provider === 'google'
  ) {
    return 'gmail_oauth'
  }

  if (
    provider?.kind === 'smtp' &&
    provider.smtp?.host?.toLowerCase() === GMAIL_SMTP_SETTINGS.host &&
    (provider.smtp.auth_type || 'basic') === 'basic'
  ) {
    return 'gmail_app_password'
  }

  return 'smtp_advanced'
}

export const gmailPersonalDailyCap = (value?: number, accountType?: GmailAccountType): number => {
  const cap = value ?? GMAIL_PERSONAL_DEFAULT_DAILY_CAP
  const max = gmailAccountTypeMaxDailyCap(accountType)
  if (!Number.isInteger(cap) || cap < 1 || cap > max) {
    throw new RangeError(`Gmail personal daily cap must be between 1 and ${max}`)
  }
  return cap
}

export const marketingProfileIds = (
  settings: Pick<
    WorkspaceSettings,
    'veridian_marketing_email_provider_ids' | 'marketing_email_provider_id'
  >
): string[] => {
  const explicit = settings.veridian_marketing_email_provider_ids?.filter(Boolean) || []
  if (explicit.length > 0) return [...new Set(explicit)]
  return settings.marketing_email_provider_id ? [settings.marketing_email_provider_id] : []
}

export const withMarketingProfileRotation = (
  settings: WorkspaceSettings,
  integrationId: string,
  enabled: boolean
): WorkspaceSettings => {
  const current = marketingProfileIds(settings)
  const next = enabled
    ? [...new Set([...current, integrationId])]
    : current.filter((id) => id !== integrationId)

  if (next.length === 0) {
    throw new RangeError('At least one marketing sending profile must remain active')
  }

  return {
    ...settings,
    veridian_marketing_email_provider_ids: next,
    marketing_email_provider_id: next[0]
  }
}

// Explicitly clear write-only inputs. The server returns only non-sensitive
// `has_*` metadata and preserves the stored secret when these values are absent.
export const smtpSettingsForEdit = (smtp?: SMTPSettings): SMTPSettings | undefined =>
  smtp
    ? {
        ...smtp,
        password: undefined,
        oauth2_client_secret: undefined,
        oauth2_refresh_token: undefined
      }
    : undefined

// Request payloads must contain only editable SMTP configuration. Read-only
// credential-presence metadata is deliberately kept out of writes.
export const smtpSettingsForRequest = (smtp?: SMTPSettings): SMTPSettings | undefined => {
  if (!smtp) return undefined
  const {
    has_password: _hasPassword,
    has_oauth2_client_secret: _hasOAuthClientSecret,
    has_oauth2_refresh_token: _hasOAuthRefreshToken,
    ...editableSMTP
  } = smtp
  void _hasPassword
  void _hasOAuthClientSecret
  void _hasOAuthRefreshToken

  return {
    ...editableSMTP,
    password: editableSMTP.password || undefined,
    oauth2_client_secret: editableSMTP.oauth2_client_secret || undefined,
    oauth2_refresh_token: editableSMTP.oauth2_refresh_token || undefined
  }
}

export const buildGmailAppPasswordProvider = ({
  email,
  senderName,
  appPassword,
  rateLimitPerMinute = 1,
  profileDailyCap,
  accountType,
  existingSenders = [],
  existingProvider
}: GmailAppPasswordProfileInput): EmailProvider => {
  const normalizedEmail = email.trim().toLowerCase()
  const compactPassword = appPassword?.replace(/\s/g, '')
  const normalizedPassword = compactPassword || undefined
  const existingSender = existingSenders.find(
    (sender) => sender.email.trim().toLowerCase() === normalizedEmail
  )

  const {
    veridian_credentials_configured: _credentialsConfigured,
    veridian_transport_verified_at: _transportVerifiedAt,
    ...editableProvider
  } = existingProvider || ({} as EmailProvider)
  void _credentialsConfigured
  void _transportVerifiedAt

  return {
    ...editableProvider,
    kind: 'smtp',
    smtp: {
      ...GMAIL_SMTP_SETTINGS,
      username: normalizedEmail,
      password: normalizedPassword,
      ehlo_hostname: ''
    },
    senders: [
      {
        id: existingSender?.id || crypto.randomUUID(),
        email: normalizedEmail,
        name: senderName.trim(),
        is_default: true
      }
    ],
    rate_limit_per_minute: rateLimitPerMinute,
    veridian_profile_daily_cap: gmailPersonalDailyCap(profileDailyCap, accountType),
    veridian_gmail_account_type: accountType
  }
}
