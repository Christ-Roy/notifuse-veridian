import { describe, it, expect, vi, beforeEach } from 'vitest'

vi.mock('./client', () => ({
  api: {
    get: vi.fn()
  }
}))

import { api } from './client'
import { replyStatsApi } from './veridian_reply_stats'
import type { VeridianReplyStatsResponse } from './veridian_reply_stats'

describe('replyStatsApi', () => {
  beforeEach(() => vi.clearAllMocks())

  it('GETs the reply stats endpoint with workspace + window params', async () => {
    const result: VeridianReplyStatsResponse = { replied: 12 }
    vi.mocked(api.get).mockResolvedValue(result)

    const res = await replyStatsApi.get({
      workspace_id: 'ws-1',
      start: '2026-06-01',
      end: '2026-06-15'
    })

    expect(api.get).toHaveBeenCalledWith(
      '/api/veridian/messages.replyStats?workspace_id=ws-1&start=2026-06-01&end=2026-06-15'
    )
    expect(res.replied).toBe(12)
  })

  it('omits start/end when not provided (whole history)', async () => {
    vi.mocked(api.get).mockResolvedValue({ replied: 0 })
    await replyStatsApi.get({ workspace_id: 'ws-2' })
    expect(api.get).toHaveBeenCalledWith('/api/veridian/messages.replyStats?workspace_id=ws-2')
  })
})
