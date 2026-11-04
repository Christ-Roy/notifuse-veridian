import { describe, expect, it } from 'vitest'
import type { SystemSettingsData } from '../../types/settings'
import { relayCertNeedsEmail, toSettingsUpdate } from './systemSettingsRelay'

const base = {
  root_email: 'root@example.test',
  api_endpoint: 'https://notifuse.example.test',
  veridian_smtp_relay_enabled: true,
  veridian_relay_cert_managed: true,
  veridian_relay_acme_email: 'ops@example.test',
  veridian_relay_acme_directory: 'test',
  has_veridian_relay_cf_token: true,
  veridian_relay_cert_not_after: '2027-02-01T00:00:00Z',
  veridian_relay_cert_serial: 'ABC',
  veridian_relay_cert_last_renewed: '2026-11-04T10:00:00Z',
  veridian_relay_cert_last_error: ''
} as unknown as SystemSettingsData

describe('toSettingsUpdate', () => {
  it('garde les valeurs courantes du relais et du certificat', () => {
    const body = toSettingsUpdate(base)
    expect(body.veridian_smtp_relay_enabled).toBe(true)
    expect(body.veridian_relay_cert_managed).toBe(true)
    expect(body.veridian_relay_acme_email).toBe('ops@example.test')
    expect(body.veridian_relay_acme_directory).toBe('test')
  })

  it('retire les champs d état lecture seule', () => {
    const body = toSettingsUpdate(base) as unknown as Record<string, unknown>
    for (const key of [
      'has_veridian_relay_cf_token',
      'veridian_relay_cert_not_after',
      'veridian_relay_cert_serial',
      'veridian_relay_cert_last_renewed',
      'veridian_relay_cert_last_error'
    ]) {
      expect(key in body).toBe(false)
    }
  })

  it('n envoie le jeton que lorsqu il est saisi (vide = jeton stocké conservé)', () => {
    expect('veridian_relay_cf_token' in toSettingsUpdate({ ...base, veridian_relay_cf_token: '' })).toBe(false)
    expect('veridian_relay_cf_token' in toSettingsUpdate({ ...base, veridian_relay_cf_token: undefined })).toBe(false)
    expect(toSettingsUpdate({ ...base, veridian_relay_cf_token: 'jeton-saisi' }).veridian_relay_cf_token).toBe(
      'jeton-saisi'
    )
  })

  it('n altère pas le formulaire d origine', () => {
    const source = { ...base, veridian_relay_cf_token: '' } as SystemSettingsData
    toSettingsUpdate(source)
    expect('has_veridian_relay_cf_token' in source).toBe(true)
  })
})

describe('relayCertNeedsEmail', () => {
  it('exige un contact ACME seulement quand le certificat est géré', () => {
    expect(relayCertNeedsEmail(true)).toBe(true)
    expect(relayCertNeedsEmail(false)).toBe(false)
    expect(relayCertNeedsEmail(undefined)).toBe(false)
  })
})
