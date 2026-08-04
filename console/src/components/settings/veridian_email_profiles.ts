import type { EmailProvider, Sender, SMTPSettings } from '../../services/api/types'

export type EmailProfileMode = 'gmail_app_password' | 'smtp_advanced'

export interface GmailAppPasswordProfileInput {
  email: string
  senderName: string
  appPassword?: string
  rateLimitPerMinute?: number
  existingSenders?: Sender[]
  existingSMTP?: SMTPSettings
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
    provider.smtp?.host?.toLowerCase() === GMAIL_SMTP_SETTINGS.host &&
    (provider.smtp.auth_type || 'basic') === 'basic'
  ) {
    return 'gmail_app_password'
  }

  return 'smtp_advanced'
}

export const buildGmailAppPasswordProvider = ({
  email,
  senderName,
  appPassword,
  rateLimitPerMinute = 1,
  existingSenders = [],
  existingSMTP
}: GmailAppPasswordProfileInput): EmailProvider => {
  const normalizedEmail = email.trim().toLowerCase()
  const normalizedPassword = appPassword?.replace(/\s/g, '')
  const existingSender = existingSenders.find(
    (sender) => sender.email.trim().toLowerCase() === normalizedEmail
  )

  return {
    kind: 'smtp',
    smtp: {
      ...GMAIL_SMTP_SETTINGS,
      username: normalizedEmail,
      password: normalizedPassword,
      encrypted_password: existingSMTP?.encrypted_password,
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
    rate_limit_per_minute: rateLimitPerMinute
  }
}
