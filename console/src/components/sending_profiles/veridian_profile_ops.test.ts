import { beforeEach, describe, expect, it, vi } from 'vitest'

import type { EmailProvider, Integration, Workspace } from '../../services/api/types'

vi.mock('../../services/api/workspace', async () => {
  const actual = await vi.importActual<typeof import('../../services/api/workspace')>('../../services/api/workspace')
  return {
    ...actual,
    workspaceService: {
      get: vi.fn(),
      update: vi.fn().mockResolvedValue({}),
      updateIntegration: vi.fn().mockResolvedValue({}),
      createIntegration: vi.fn().mockResolvedValue({ integration_id: 'new-imap' }),
      deleteIntegration: vi.fn().mockResolvedValue({})
    }
  }
})
vi.mock('../../services/api/email', () => ({ emailService: { testProvider: vi.fn() } }))

import { workspaceService } from '../../services/api/workspace'
import {
  linkInbox,
  ProfileOperationError,
  providerForRequest,
  saveInbox,
  setPaused,
  setRotation,
  setUsage,
  updateProfile
} from './veridian_profile_ops'

const provider = (verified = true, extra: Partial<EmailProvider> = {}): EmailProvider => ({
  kind: 'smtp',
  rate_limit_per_minute: 60,
  veridian_credentials_configured: true,
  veridian_transport_verified_at: verified ? '2026-10-04T07:00:00Z' : undefined,
  veridian_profile_daily_cap: 300,
  veridian_warmup_started_at: '2026-09-30T00:00:00Z',
  veridian_warmup_schedule: [20, 40],
  veridian_hard_bounce_freeze_threshold: 0.08,
  smtp: { host: 'smtp.exemple.fr', port: 587, username: 'u', use_tls: true, has_password: true },
  senders: [{ id: 's', email: 'a@exemple.fr', name: 'A', is_default: true }],
  ...extra
})

const integration = (id: string, p: EmailProvider): Integration => ({
  id,
  name: `Profil ${id}`,
  type: 'email',
  email_provider: p,
  created_at: '',
  updated_at: ''
})

const workspaceWith = (integrations: Integration[], settings: Partial<Workspace['settings']> = {}): Workspace =>
  ({
    id: 'ws-1',
    name: 'Atelier',
    created_at: '',
    updated_at: '',
    settings: { timezone: 'UTC', email_tracking_enabled: false, default_language: 'fr', languages: ['fr'], ...settings },
    integrations
  }) as Workspace

const load = (workspace: Workspace) => vi.mocked(workspaceService.get).mockResolvedValue({ workspace } as never)

describe('écritures de la page Profils d\'envoi', () => {
  beforeEach(() => {
    vi.clearAllMocks()
  })

  it('providerForRequest retire ce que seul le serveur émet et les marqueurs has_*', () => {
    const out = providerForRequest(provider())
    expect(out.veridian_credentials_configured).toBeUndefined()
    expect(out.veridian_transport_verified_at).toBeUndefined()
    expect(out.smtp).not.toHaveProperty('has_password')
    // tout le reste voyage tel quel: aucun réglage perdu à l'enregistrement
    expect(out.veridian_profile_daily_cap).toBe(300)
    expect(out.veridian_warmup_schedule).toEqual([20, 40])
    expect(out.veridian_hard_bounce_freeze_threshold).toBe(0.08)
  })

  it('pause: pose le drapeau et garde tous les autres réglages', async () => {
    load(workspaceWith([integration('a', provider())]))
    await setPaused('ws-1', 'a', true)
    const request = vi.mocked(workspaceService.updateIntegration).mock.calls[0][0]
    expect(request.integration_id).toBe('a')
    expect(request.provider?.veridian_paused).toBe(true)
    expect(request.provider?.veridian_warmup_schedule).toEqual([20, 40])
    expect(request.provider?.veridian_transport_verified_at).toBeUndefined()
  })

  it('reprise: retire le drapeau', async () => {
    load(workspaceWith([integration('a', provider(true, { veridian_paused: true }))]))
    await setPaused('ws-1', 'a', false)
    const request = vi.mocked(workspaceService.updateIntegration).mock.calls[0][0]
    expect(request.provider).not.toHaveProperty('veridian_paused')
  })

  it('lien IMAP: pose puis retire', async () => {
    load(workspaceWith([integration('a', provider())]))
    await linkInbox('ws-1', 'a', 'imap-1')
    expect(vi.mocked(workspaceService.updateIntegration).mock.calls[0][0].provider?.veridian_return_imap_integration_id).toBe('imap-1')
    await linkInbox('ws-1', 'a', null)
    expect(vi.mocked(workspaceService.updateIntegration).mock.calls[1][0].provider).not.toHaveProperty(
      'veridian_return_imap_integration_id'
    )
  })

  it('updateProfile relit le workspace avant d\'écrire', async () => {
    load(workspaceWith([integration('a', provider())]))
    await updateProfile('ws-1', 'a', { name: 'Nouveau nom' })
    expect(workspaceService.get).toHaveBeenCalledWith('ws-1')
    expect(vi.mocked(workspaceService.updateIntegration).mock.calls[0][0].name).toBe('Nouveau nom')
  })

  describe('rotation', () => {
    it('ajoute un profil vérifié', async () => {
      load(workspaceWith([integration('a', provider()), integration('b', provider())], { marketing_email_provider_id: 'a', veridian_marketing_email_provider_ids: ['a'] }))
      await setRotation('ws-1', 'b', true)
      const request = vi.mocked(workspaceService.update).mock.calls[0][0]
      expect(request.settings?.veridian_marketing_email_provider_ids).toEqual(['a', 'b'])
      expect(request.name).toBe('Atelier')
    })

    it('refuse un profil non vérifié', async () => {
      load(workspaceWith([integration('a', provider()), integration('b', provider(false))], { veridian_marketing_email_provider_ids: ['a'] }))
      await expect(setRotation('ws-1', 'b', true)).rejects.toMatchObject({ code: 'unverified' })
      expect(workspaceService.update).not.toHaveBeenCalled()
    })

    it('refuse le profil transactionnel (exclusivité)', async () => {
      load(workspaceWith([integration('a', provider()), integration('t', provider())], { veridian_marketing_email_provider_ids: ['a'], transactional_email_provider_id: 't' }))
      await expect(setRotation('ws-1', 't', true)).rejects.toMatchObject({ code: 'transactional' })
      expect(workspaceService.update).not.toHaveBeenCalled()
    })

    it('refuse de vider la rotation', async () => {
      load(workspaceWith([integration('a', provider())], { veridian_marketing_email_provider_ids: ['a'], marketing_email_provider_id: 'a' }))
      await expect(setRotation('ws-1', 'a', false)).rejects.toBeInstanceOf(ProfileOperationError)
      expect(workspaceService.update).not.toHaveBeenCalled()
    })

    it('retire un profil quand il en reste un autre', async () => {
      load(workspaceWith([integration('a', provider()), integration('b', provider())], { veridian_marketing_email_provider_ids: ['a', 'b'], marketing_email_provider_id: 'a' }))
      await setRotation('ws-1', 'a', false)
      expect(vi.mocked(workspaceService.update).mock.calls[0][0].settings?.veridian_marketing_email_provider_ids).toEqual(['b'])
    })
  })

  describe('usage exclusif', () => {
    it('transactionnel: sort le profil de la rotation et le pose en transactionnel', async () => {
      load(workspaceWith([integration('a', provider()), integration('b', provider())], { veridian_marketing_email_provider_ids: ['a', 'b'], marketing_email_provider_id: 'a' }))
      await setUsage('ws-1', 'b', 'transactional')
      const settings = vi.mocked(workspaceService.update).mock.calls[0][0].settings
      expect(settings?.veridian_marketing_email_provider_ids).toEqual(['a'])
      expect(settings?.transactional_email_provider_id).toBe('b')
    })

    it('transactionnel: refuse de prendre le seul profil de la rotation', async () => {
      load(workspaceWith([integration('a', provider())], { veridian_marketing_email_provider_ids: ['a'], marketing_email_provider_id: 'a' }))
      await expect(setUsage('ws-1', 'a', 'transactional')).rejects.toMatchObject({ code: 'last_profile' })
      expect(workspaceService.update).not.toHaveBeenCalled()
    })

    it('transactionnel: refuse un profil non vérifié', async () => {
      load(workspaceWith([integration('a', provider()), integration('b', provider(false))], { veridian_marketing_email_provider_ids: ['a'] }))
      await expect(setUsage('ws-1', 'b', 'transactional')).rejects.toMatchObject({ code: 'unverified' })
    })

    it('commercial: libère le profil transactionnel, hors rotation', async () => {
      load(workspaceWith([integration('a', provider()), integration('t', provider())], { veridian_marketing_email_provider_ids: ['a'], transactional_email_provider_id: 't' }))
      await setUsage('ws-1', 't', 'commercial')
      const settings = vi.mocked(workspaceService.update).mock.calls[0][0].settings
      expect(settings?.transactional_email_provider_id).toBe('')
      expect(settings?.veridian_marketing_email_provider_ids).toEqual(['a'])
    })
  })

  describe('boîte IMAP', () => {
    const settings = { host: 'imap.exemple.fr', port: 993, username: 'u', use_tls: true, has_password: true }

    it('création: le mot de passe part une fois, has_password jamais', async () => {
      const id = await saveInbox('ws-1', { name: 'Retour', settings: { ...settings, password: 'pw' } })
      expect(id).toBe('new-imap')
      const request = vi.mocked(workspaceService.createIntegration).mock.calls[0][0]
      expect(request.type).toBe('imap')
      expect(request.imap_settings?.password).toBe('pw')
      expect(request.imap_settings).not.toHaveProperty('has_password')
    })

    it('édition: mot de passe vide = secret conservé (champ absent de la requête)', async () => {
      await saveInbox('ws-1', { id: 'imap-1', name: 'Retour', settings: { ...settings, password: '' } })
      const request = vi.mocked(workspaceService.updateIntegration).mock.calls[0][0]
      expect(request.integration_id).toBe('imap-1')
      expect(request.imap_settings?.password).toBeUndefined()
    })
  })
})
