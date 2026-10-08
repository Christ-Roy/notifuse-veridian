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
vi.mock('../../services/api/veridian_email_profiles', async () => {
  const actual = await vi.importActual<typeof import('../../services/api/veridian_email_profiles')>(
    '../../services/api/veridian_email_profiles'
  )
  return {
    ...actual,
    emailProfilesStateService: {
      setUsage: vi.fn().mockResolvedValue({}),
      pause: vi.fn().mockResolvedValue({ integration_id: 'a', paused: true }),
      resume: vi.fn().mockResolvedValue({ integration_id: 'a', paused: false })
    }
  }
})

import { workspaceService } from '../../services/api/workspace'
import { emailProfilesStateService } from '../../services/api/veridian_email_profiles'
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

  it('pause: appelle emailProfiles.pause, et plus updateIntegration', async () => {
    load(workspaceWith([integration('a', provider())]))
    await setPaused('ws-1', 'a', true)
    expect(emailProfilesStateService.pause).toHaveBeenCalledWith({ workspace_id: 'ws-1', integration_id: 'a' })
    expect(emailProfilesStateService.resume).not.toHaveBeenCalled()
    expect(workspaceService.updateIntegration).not.toHaveBeenCalled()
    expect(workspaceService.update).not.toHaveBeenCalled()
  })

  it('reprise: appelle emailProfiles.resume, et plus updateIntegration', async () => {
    load(workspaceWith([integration('a', provider(true, { veridian_paused: true }))]))
    await setPaused('ws-1', 'a', false)
    expect(emailProfilesStateService.resume).toHaveBeenCalledWith({ workspace_id: 'ws-1', integration_id: 'a' })
    expect(emailProfilesStateService.pause).not.toHaveBeenCalled()
    expect(workspaceService.updateIntegration).not.toHaveBeenCalled()
  })

  it("pause: un profil transactionnel est refusé avant même l'appel (le serveur répond 400)", async () => {
    load(workspaceWith([integration('a', provider()), integration('t', provider())], { transactional_email_provider_id: 't' }))
    await expect(setPaused('ws-1', 't', true)).rejects.toMatchObject({ code: 'transactional' })
    expect(emailProfilesStateService.pause).not.toHaveBeenCalled()
  })

  it('pause: le refus du serveur remonte tel quel', async () => {
    load(workspaceWith([integration('a', provider())]))
    vi.mocked(emailProfilesStateService.pause).mockRejectedValueOnce(new Error('Profil transactionnel : pause impossible'))
    await expect(setPaused('ws-1', 'a', true)).rejects.toThrow('Profil transactionnel : pause impossible')
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
    it("ajoute un profil vérifié: usage commercial par l'API dédiée", async () => {
      load(workspaceWith([integration('a', provider()), integration('b', provider())], { marketing_email_provider_id: 'a', veridian_marketing_email_provider_ids: ['a'] }))
      await setRotation('ws-1', 'b', true)
      expect(emailProfilesStateService.setUsage).toHaveBeenCalledWith({
        workspace_id: 'ws-1',
        integration_id: 'b',
        usage: 'commercial'
      })
      expect(workspaceService.update).not.toHaveBeenCalled()
      expect(workspaceService.updateIntegration).not.toHaveBeenCalled()
    })

    it('refuse un profil non vérifié', async () => {
      load(workspaceWith([integration('a', provider()), integration('b', provider(false))], { veridian_marketing_email_provider_ids: ['a'] }))
      await expect(setRotation('ws-1', 'b', true)).rejects.toMatchObject({ code: 'unverified' })
      expect(emailProfilesStateService.setUsage).not.toHaveBeenCalled()
    })

    it('refuse le profil transactionnel (exclusivité)', async () => {
      load(workspaceWith([integration('a', provider()), integration('t', provider())], { veridian_marketing_email_provider_ids: ['a'], transactional_email_provider_id: 't' }))
      await expect(setRotation('ws-1', 't', true)).rejects.toMatchObject({ code: 'transactional' })
      expect(emailProfilesStateService.setUsage).not.toHaveBeenCalled()
    })

    it('refuse de vider la rotation', async () => {
      load(workspaceWith([integration('a', provider())], { veridian_marketing_email_provider_ids: ['a'], marketing_email_provider_id: 'a' }))
      await expect(setRotation('ws-1', 'a', false)).rejects.toBeInstanceOf(ProfileOperationError)
      expect(emailProfilesStateService.setUsage).not.toHaveBeenCalled()
    })

    it('retire un profil quand il en reste un autre: hors service', async () => {
      load(workspaceWith([integration('a', provider()), integration('b', provider())], { veridian_marketing_email_provider_ids: ['a', 'b'], marketing_email_provider_id: 'a' }))
      await setRotation('ws-1', 'a', false)
      expect(emailProfilesStateService.setUsage).toHaveBeenCalledWith({
        workspace_id: 'ws-1',
        integration_id: 'a',
        usage: 'unassigned'
      })
    })

    it('le refus du serveur remonte tel quel', async () => {
      load(workspaceWith([integration('a', provider()), integration('b', provider())], { veridian_marketing_email_provider_ids: ['a'] }))
      vi.mocked(emailProfilesStateService.setUsage).mockRejectedValueOnce(new Error('Profil non vérifié côté serveur'))
      await expect(setRotation('ws-1', 'b', true)).rejects.toThrow('Profil non vérifié côté serveur')
    })
  })

  describe('usage exclusif', () => {
    it("transactionnel: un seul appel setUsage, pas d'écriture du workspace", async () => {
      load(workspaceWith([integration('a', provider()), integration('b', provider())], { veridian_marketing_email_provider_ids: ['a', 'b'], marketing_email_provider_id: 'a' }))
      await setUsage('ws-1', 'b', 'transactional')
      expect(emailProfilesStateService.setUsage).toHaveBeenCalledTimes(1)
      expect(emailProfilesStateService.setUsage).toHaveBeenCalledWith({
        workspace_id: 'ws-1',
        integration_id: 'b',
        usage: 'transactional'
      })
      expect(workspaceService.update).not.toHaveBeenCalled()
      expect(workspaceService.updateIntegration).not.toHaveBeenCalled()
    })

    it('transactionnel: refuse de prendre le seul profil de la rotation', async () => {
      load(workspaceWith([integration('a', provider())], { veridian_marketing_email_provider_ids: ['a'], marketing_email_provider_id: 'a' }))
      await expect(setUsage('ws-1', 'a', 'transactional')).rejects.toMatchObject({ code: 'last_profile' })
      expect(emailProfilesStateService.setUsage).not.toHaveBeenCalled()
    })

    it('transactionnel: refuse un profil non vérifié', async () => {
      load(workspaceWith([integration('a', provider()), integration('b', provider(false))], { veridian_marketing_email_provider_ids: ['a'] }))
      await expect(setUsage('ws-1', 'b', 'transactional')).rejects.toMatchObject({ code: 'unverified' })
      expect(emailProfilesStateService.setUsage).not.toHaveBeenCalled()
    })

    it('commercial: appelle setUsage commercial (le serveur le remet en rotation)', async () => {
      load(workspaceWith([integration('a', provider()), integration('t', provider())], { veridian_marketing_email_provider_ids: ['a'], transactional_email_provider_id: 't' }))
      await setUsage('ws-1', 't', 'commercial')
      expect(emailProfilesStateService.setUsage).toHaveBeenCalledWith({
        workspace_id: 'ws-1',
        integration_id: 't',
        usage: 'commercial'
      })
      expect(workspaceService.update).not.toHaveBeenCalled()
    })

    it('hors service: appelle setUsage unassigned', async () => {
      load(workspaceWith([integration('a', provider()), integration('t', provider())], { transactional_email_provider_id: 't' }))
      await setUsage('ws-1', 't', 'unassigned')
      expect(emailProfilesStateService.setUsage).toHaveBeenCalledWith({
        workspace_id: 'ws-1',
        integration_id: 't',
        usage: 'unassigned'
      })
    })

    it('le message du serveur est affiché tel quel quand il refuse', async () => {
      load(workspaceWith([integration('a', provider()), integration('b', provider())], { veridian_marketing_email_provider_ids: ['a', 'b'] }))
      vi.mocked(emailProfilesStateService.setUsage).mockRejectedValueOnce(new Error('Refus du serveur : domaine partagé'))
      await expect(setUsage('ws-1', 'b', 'transactional')).rejects.toThrow('Refus du serveur : domaine partagé')
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
