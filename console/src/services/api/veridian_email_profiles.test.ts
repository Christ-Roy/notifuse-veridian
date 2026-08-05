import { describe, expect, it, vi } from 'vitest'

vi.mock('./client', () => ({ api: { get: vi.fn() } }))

import { api } from './client'
import { emailProfilesUsageService } from './veridian_email_profiles'

describe('emailProfilesUsageService', () => {
  it('uses the authenticated workspace usage contract', async () => {
    vi.mocked(api.get).mockResolvedValue({
      date: '2026-08-05',
      total_used: 0,
      total_accepted: 0,
      profiles: []
    })

    await emailProfilesUsageService.get('workspace with spaces')

    expect(api.get).toHaveBeenCalledWith(
      '/api/veridian/emailProfiles.usage?workspace_id=workspace+with+spaces'
    )
  })
})
