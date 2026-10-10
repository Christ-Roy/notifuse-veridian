import { describe, it, expect, vi } from 'vitest'

vi.mock('./analytics', () => ({ analyticsService: { query: vi.fn() } }))

import { analyticsService } from './analytics'
import { automationApi } from './automation'

// LOT 1 : count_queued est une mesure à part ; count_completed = envoyé réellement.
describe('automationApi.getNodeStats', () => {
  it('demande count_queued et le range dans queued, sans le mélanger à completed', async () => {
    vi.mocked(analyticsService.query).mockResolvedValue({
      data: [
        {
          node_id: 'j0a',
          node_type: 'email',
          count_entered: 30,
          count_completed: 5,
          count_queued: 20,
          count_failed: 2,
          count_skipped: 0
        },
        { node_id: 'wait', node_type: 'delay', count_entered: 4, count_completed: 4, count_failed: 0, count_skipped: 0 }
      ]
    } as never)
    const { node_stats } = await automationApi.getNodeStats({ workspace_id: 'ws-1', automation_id: 'a1' })
    const query = vi.mocked(analyticsService.query).mock.calls[0][0] as { measures: string[] }
    expect(query.measures).toContain('count_queued')
    expect(node_stats.j0a.queued).toBe(20)
    expect(node_stats.j0a.completed).toBe(5)
    expect(node_stats.j0a.failed).toBe(2)
    expect(node_stats.wait.queued).toBe(0)
  })
})
