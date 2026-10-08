import { describe, it, expect } from 'vitest'
import { EMAIL_SERIES, buildSeriesQuery } from './email_metrics_series'

describe('buildSeriesQuery : filtre par famille', () => {
  const range: [string, string] = ['2024-01-01', '2024-01-31']

  it('commercial : message_type = commercial', () => {
    const q = buildSeriesQuery(EMAIL_SERIES[0], 'commercial', range, 'UTC')
    expect(q.filters).toEqual([{ member: 'message_type', operator: 'equals', values: ['commercial'] }])
  })

  it('transactionnel : message_type = transactional', () => {
    const q = buildSeriesQuery(EMAIL_SERIES[0], 'transactional', range, 'UTC')
    expect(q.filters).toEqual([{ member: 'message_type', operator: 'equals', values: ['transactional'] }])
  })

  it("n'utilise plus le filtre broadcast_id", () => {
    for (const f of ['commercial', 'transactional'] as const) {
      const q = buildSeriesQuery(EMAIL_SERIES[1], f, range, 'UTC')
      expect(JSON.stringify(q.filters)).not.toContain('broadcast_id')
    }
  })
})
