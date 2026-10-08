import { describe, expect, it, vi } from 'vitest'

vi.mock('./client', () => ({ api: { get: vi.fn(), post: vi.fn() } }))

import { api } from './client'
import { emailProfilesStateService } from './veridian_email_profiles'

describe('emailProfilesStateService (API dédiée du lot 4)', () => {
  it('setUsage : POST /api/veridian/emailProfiles.setUsage avec le corps du contrat', async () => {
    vi.mocked(api.post).mockResolvedValue({
      integration_id: 'p1',
      usage: 'transactional',
      in_rotation: false,
      paused: false,
      rotation: ['p2'],
      transactional_integration_id: 'p1'
    })
    const response = await emailProfilesStateService.setUsage({
      workspace_id: 'ws-1',
      integration_id: 'p1',
      usage: 'transactional'
    })
    expect(api.post).toHaveBeenCalledWith('/api/veridian/emailProfiles.setUsage', {
      workspace_id: 'ws-1',
      integration_id: 'p1',
      usage: 'transactional'
    })
    expect(response.transactional_integration_id).toBe('p1')
  })

  it('pause : POST /api/veridian/emailProfiles.pause', async () => {
    vi.mocked(api.post).mockResolvedValue({ integration_id: 'p1', paused: true })
    await emailProfilesStateService.pause({ workspace_id: 'ws-1', integration_id: 'p1' })
    expect(api.post).toHaveBeenLastCalledWith('/api/veridian/emailProfiles.pause', {
      workspace_id: 'ws-1',
      integration_id: 'p1'
    })
  })

  it('resume : POST /api/veridian/emailProfiles.resume', async () => {
    vi.mocked(api.post).mockResolvedValue({ integration_id: 'p1', paused: false })
    await emailProfilesStateService.resume({ workspace_id: 'ws-1', integration_id: 'p1' })
    expect(api.post).toHaveBeenLastCalledWith('/api/veridian/emailProfiles.resume', {
      workspace_id: 'ws-1',
      integration_id: 'p1'
    })
  })

  it('le message du 400 est propagé tel quel', async () => {
    vi.mocked(api.post).mockRejectedValue(new Error('Un profil transactionnel ne se met pas en pause'))
    await expect(
      emailProfilesStateService.pause({ workspace_id: 'ws-1', integration_id: 'p1' })
    ).rejects.toThrow('Un profil transactionnel ne se met pas en pause')
  })
})
