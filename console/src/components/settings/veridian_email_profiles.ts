import type {
  EmailProvider,
  Sender,
  SMTPSettings,
  WorkspaceSettings
} from '../../services/api/types'

export type EmailProfileMode = 'gmail_app_password' | 'gmail_oauth' | 'smtp_advanced'

export const GMAIL_PERSONAL_DEFAULT_DAILY_CAP = 30
export const GMAIL_PERSONAL_MAX_DAILY_CAP = 50

export interface GmailAppPasswordProfileInput {
  email: string
  senderName: string
  appPassword?: string
  rateLimitPerMinute?: number
  profileDailyCap?: number
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

export const gmailPersonalDailyCap = (value?: number): number => {
  const cap = value ?? GMAIL_PERSONAL_DEFAULT_DAILY_CAP
  if (!Number.isInteger(cap) || cap < 1 || cap > GMAIL_PERSONAL_MAX_DAILY_CAP) {
    throw new RangeError(
      `Gmail personal daily cap must be between 1 and ${GMAIL_PERSONAL_MAX_DAILY_CAP}`
    )
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
    veridian_profile_daily_cap: gmailPersonalDailyCap(profileDailyCap)
  }
}
