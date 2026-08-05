import { beforeEach, describe, expect, it, vi } from 'vitest'

vi.mock('./client', () => ({
  api: { post: vi.fn() }
}))

import { api } from './client'
import { emailService } from './email'

describe('emailService.testProvider', () => {
  beforeEach(() => vi.clearAllMocks())

  it('tests a persisted integration without sending provider credentials', async () => {
    vi.mocked(api.post).mockResolvedValue({ success: true } as never)

    await emailService.testProvider('workspace-1', 'gmail-profile-1', 'owner@example.com')

    expect(api.post).toHaveBeenCalledWith('/api/email.testProvider', {
      workspace_id: 'workspace-1',
      integration_id: 'gmail-profile-1',
      to: 'owner@example.com'
    })
    expect(JSON.stringify(vi.mocked(api.post).mock.calls[0][1])).not.toContain('password')
  })
})
