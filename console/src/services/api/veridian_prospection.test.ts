import { describe, it, expect, vi, beforeEach } from 'vitest'

vi.mock('./client', () => ({ api: { get: vi.fn() } }))

import { api } from './client'
import { prospectionStatsService } from './veridian_prospection'

describe('prospectionStatsService', () => {
  beforeEach(() => vi.clearAllMocks())

  it("lit prospection.stats avec le workspace et la fenêtre", async () => {
    vi.mocked(api.get).mockResolvedValue({ sequences: [], segments: [] })
    await prospectionStatsService.get({ workspace_id: 'ws-1', start: '2026-09-28', end: '2026-10-08' })
    expect(api.get).toHaveBeenCalledWith(
      '/api/veridian/prospection.stats?workspace_id=ws-1&start=2026-09-28&end=2026-10-08'
    )
  })

  it("sans fenêtre : tout l'historique", async () => {
    vi.mocked(api.get).mockResolvedValue({ sequences: [], segments: [] })
    await prospectionStatsService.get({ workspace_id: 'ws-2' })
    expect(api.get).toHaveBeenCalledWith('/api/veridian/prospection.stats?workspace_id=ws-2')
  })
})
