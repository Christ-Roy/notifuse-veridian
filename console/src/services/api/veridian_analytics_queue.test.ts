import { describe, it, expect, vi, beforeEach } from 'vitest'

vi.mock('../../router', () => ({ router: { navigate: vi.fn() } }))
vi.mock('./client', () => ({ api: { post: vi.fn() } }))

import { api } from './client'
import { analyticsService, type AnalyticsQuery } from './analytics'
import { initializeAnalytics } from '../../utils/analytics-config'

const q = (m: string): AnalyticsQuery => ({ schema: 'message_history', measures: [m], dimensions: [] })
const tick = () => new Promise((r) => setTimeout(r, 5))

describe('file des requêtes analytics', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    analyticsService.clearCache()
    vi.mocked(api.post).mockImplementation(async () => {
      await tick()
      return { data: [], meta: { total: 0, query: '', params: [] } }
    })
  })

  it('la même demande, encore en cours, ne part qu’une fois ; ensuite le cache répond', async () => {
    await Promise.all([analyticsService.query(q('a'), 'ws'), analyticsService.query(q('a'), 'ws'), analyticsService.query(q('b'), 'ws')])
    expect(api.post).toHaveBeenCalledTimes(2)
    await analyticsService.query(q('a'), 'ws')
    expect(api.post).toHaveBeenCalledTimes(2)
  })

  it('plusieurs demandes différentes partent ensemble, jamais toutes', async () => {
    let now = 0
    let peak = 0
    vi.mocked(api.post).mockImplementation(async () => {
      peak = Math.max(peak, ++now)
      await tick()
      now--
      return { data: [], meta: { total: 0, query: '', params: [] } }
    })
    initializeAnalytics()
    await Promise.all(Array.from({ length: 12 }, (_, i) => analyticsService.query(q(`m${i}`), 'ws')))
    expect(api.post).toHaveBeenCalledTimes(12)
    expect(peak).toBe(2)
  })
})
