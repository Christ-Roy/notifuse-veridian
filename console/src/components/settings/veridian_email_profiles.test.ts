import { describe, expect, it, vi } from 'vitest'

import {
  buildGmailAppPasswordProvider,
  gmailPersonalDailyCap,
  inferEmailProfileMode,
  marketingProfileIds,
  smtpSettingsForEdit,
  smtpSettingsForRequest,
  withMarketingProfileRotation
} from './veridian_email_profiles'

describe('Gmail app-password email profiles', () => {
  it('builds the official Gmail STARTTLS configuration and strips display spaces', () => {
    vi.stubGlobal('crypto', { randomUUID: () => 'sender-id' })

    const provider = buildGmailAppPasswordProvider({
      email: ' AVSE.MONETIQUE@gmail.com ',
      senderName: ' AVSE Monétique ',
      appPassword: 'abcd efgh ijkl mnop'
    })

    expect(provider).toMatchObject({
      kind: 'smtp',
      rate_limit_per_minute: 1,
      veridian_profile_daily_cap: 30,
      smtp: {
        host: 'smtp.gmail.com',
        port: 587,
        use_tls: true,
        auth_type: 'basic',
        username: 'avse.monetique@gmail.com',
        password: 'abcdefghijklmnop'
      },
      senders: [
        {
          id: 'sender-id',
          email: 'avse.monetique@gmail.com',
          name: 'AVSE Monétique',
          is_default: true
        }
      ]
    })

    vi.unstubAllGlobals()
  })

  it('keeps the sender identity while an existing profile is edited', () => {
    const provider = buildGmailAppPasswordProvider({
      email: 'avse.monetique@gmail.com',
      senderName: 'AVSE Monétique',
      existingProvider: {
        kind: 'smtp',
        rate_limit_per_minute: 1,
        senders: [],
        veridian_provider_class_rates: { google: 0.5 },
        veridian_credentials_configured: true,
        veridian_transport_verified_at: '2026-08-05T12:00:00Z',
        smtp: {
          host: 'smtp.gmail.com',
          port: 587,
          username: 'avse.monetique@gmail.com',
          use_tls: true,
          has_password: true
        }
      },
      existingSenders: [
        {
          id: 'existing-sender-id',
          email: 'avse.monetique@gmail.com',
          name: 'Old name',
          is_default: true
        }
      ]
    })

    expect(provider.senders[0].id).toBe('existing-sender-id')
    expect(provider.smtp?.password).toBeUndefined()
    expect(provider.veridian_provider_class_rates).toEqual({ google: 0.5 })
    expect(provider.veridian_credentials_configured).toBeUndefined()
    expect(provider.veridian_transport_verified_at).toBeUndefined()
    expect(JSON.stringify(provider)).not.toContain('encrypted_')
  })

  it('distinguishes Gmail app passwords from advanced SMTP profiles', () => {
    const gmail = buildGmailAppPasswordProvider({
      email: 'client@gmail.com',
      senderName: 'Client'
    })
    const custom = {
      ...gmail,
      smtp: { ...gmail.smtp!, host: 'mail.example.com' }
    }

    expect(inferEmailProfileMode(gmail)).toBe('gmail_app_password')
    expect(inferEmailProfileMode(custom)).toBe('smtp_advanced')
  })

  it('recognizes Gmail OAuth profiles without exposing a broken creation flow', () => {
    const oauth = {
      kind: 'smtp' as const,
      rate_limit_per_minute: 1,
      senders: [],
      smtp: {
        host: 'smtp.gmail.com',
        port: 587,
        username: 'client@gmail.com',
        use_tls: true,
        auth_type: 'oauth2' as const,
        oauth2_provider: 'google' as const
      }
    }

    expect(inferEmailProfileMode(oauth)).toBe('gmail_oauth')
  })

  it('enforces the Gmail personal daily safety ceiling', () => {
    expect(gmailPersonalDailyCap()).toBe(30)
    expect(gmailPersonalDailyCap(50)).toBe(50)
    expect(() => gmailPersonalDailyCap(51)).toThrow(/between 1 and 50/)
  })

  it('uses the multi-profile pool with a legacy singleton fallback', () => {
    expect(marketingProfileIds({ marketing_email_provider_id: 'legacy' })).toEqual(['legacy'])
    expect(
      marketingProfileIds({
        marketing_email_provider_id: 'legacy',
        veridian_marketing_email_provider_ids: ['gmail-a', 'gmail-b', 'gmail-a']
      })
    ).toEqual(['gmail-a', 'gmail-b'])
  })

  it('keeps the legacy default aligned when rotation changes', () => {
    const settings = withMarketingProfileRotation(
      {
        timezone: 'Europe/Paris',
        email_tracking_enabled: true,
        default_language: 'fr',
        languages: ['fr'],
        marketing_email_provider_id: 'gmail-a'
      },
      'gmail-b',
      true
    )

    expect(settings.veridian_marketing_email_provider_ids).toEqual(['gmail-a', 'gmail-b'])
    expect(settings.marketing_email_provider_id).toBe('gmail-a')
    expect(() => withMarketingProfileRotation(settings, 'gmail-a', false)).not.toThrow()
    expect(() =>
      withMarketingProfileRotation(
        { ...settings, veridian_marketing_email_provider_ids: ['gmail-a'] },
        'gmail-a',
        false
      )
    ).toThrow(/at least one/i)
  })

  it('strips every write-only SMTP secret before editing', () => {
    const editable = smtpSettingsForEdit({
      host: 'smtp.gmail.com',
      port: 587,
      username: 'client@gmail.com',
      use_tls: true,
      password: 'must-not-render',
      has_password: true,
      oauth2_client_secret: 'must-not-render',
      has_oauth2_client_secret: true,
      oauth2_refresh_token: 'must-not-render',
      has_oauth2_refresh_token: true
    })

    expect(editable).toMatchObject({
      has_password: true,
      has_oauth2_client_secret: true,
      has_oauth2_refresh_token: true
    })
    expect(editable?.password).toBeUndefined()
    expect(editable?.oauth2_client_secret).toBeUndefined()
    expect(editable?.oauth2_refresh_token).toBeUndefined()
    expect(JSON.stringify(editable)).not.toContain('must-not-render')
    expect(JSON.stringify(editable)).not.toContain('encrypted_')
  })

  it('omits absent plaintext and every encrypted field from an edit request payload', () => {
    const provider = buildGmailAppPasswordProvider({
      email: 'client@gmail.com',
      senderName: 'Client',
      appPassword: '',
      existingProvider: {
        kind: 'smtp',
        rate_limit_per_minute: 1,
        senders: [],
        veridian_credentials_configured: true,
        veridian_transport_verified_at: '2026-08-05T12:00:00Z'
      }
    })
    const serialized = JSON.stringify(provider)

    expect(serialized).not.toContain('"password"')
    expect(serialized).not.toContain('encrypted_')
    expect(serialized).not.toContain('veridian_credentials_configured')
    expect(serialized).not.toContain('veridian_transport_verified_at')
  })

  it('strips read-only credential metadata from advanced SMTP writes', () => {
    const request = smtpSettingsForRequest({
      host: 'smtp.example.com',
      port: 587,
      username: 'sender@example.com',
      use_tls: true,
      password: '',
      has_password: true,
      has_oauth2_client_secret: true,
      has_oauth2_refresh_token: true
    })
    const serialized = JSON.stringify(request)

    expect(serialized).not.toContain('password')
    expect(serialized).not.toContain('has_')
    expect(serialized).not.toContain('encrypted_')
  })
})
