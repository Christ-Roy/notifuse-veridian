import { describe, expect, it, vi } from 'vitest'

import {
  buildGmailAppPasswordProvider,
  inferEmailProfileMode
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
      existingSMTP: {
        host: 'smtp.gmail.com',
        port: 587,
        use_tls: true,
        encrypted_password: 'saved-ciphertext'
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
    expect(provider.smtp?.encrypted_password).toBe('saved-ciphertext')
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
})
