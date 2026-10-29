import { describe, expect, it } from 'vitest'

import { canApply, countBySeverity, evidenceLines, notReached, sortFindings } from './veridian_reconcile_rules'
import { finding } from './veridian_reconcile_fixtures'

describe('veridian_reconcile_rules', () => {
  it('trie les plus graves d’abord, puis les plus récents', () => {
    const sorted = sortFindings([
      finding({ id: 'info', severity: 'info', last_seen_at: '2026-10-29T13:00:00Z' }),
      finding({ id: 'warn-old', severity: 'warning', last_seen_at: '2026-10-29T09:00:00Z' }),
      finding({ id: 'crit', severity: 'critical', last_seen_at: '2026-10-29T08:00:00Z' }),
      finding({ id: 'warn-new', severity: 'warning', last_seen_at: '2026-10-29T11:00:00Z' })
    ])
    expect(sorted.map((f) => f.id)).toEqual(['crit', 'warn-new', 'warn-old', 'info'])
  })

  it('compte par gravité sans les écarts résolus', () => {
    const counts = countBySeverity([
      finding({ severity: 'critical' }),
      finding({ severity: 'warning' }),
      finding({ severity: 'warning', resolved_at: '2026-10-29T13:00:00Z' })
    ])
    expect(counts).toEqual({ critical: 1, warning: 1, info: 0 })
  })

  it('un contrôle non abouti est isolé, jamais compté comme ok', () => {
    const checks = [
      { check: 'a', number: 1, title: 'A', status: 'ok' as const, count: 0, duration_ms: 1 },
      { check: 'b', number: 2, title: 'B', status: 'non_abouti' as const, count: 0, duration_ms: 1, error: 'table absente' }
    ]
    expect(notReached(checks).map((c) => c.check)).toEqual(['b'])
  })

  it('lit la preuve : tableaux joints, vides ignorés', () => {
    expect(evidenceLines({ count: 3, sample: ['a', 'b'], empty: [], none: null, text: '' })).toEqual([
      { key: 'count', value: '3' },
      { key: 'sample', value: 'a, b' }
    ])
  })

  it('n’applique qu’un écart ouvert qui a une action', () => {
    expect(canApply(finding())).toBe(true)
    expect(canApply(finding({ proposed_action: null }))).toBe(false)
    expect(canApply(finding({ resolved_at: '2026-10-29T13:00:00Z' }))).toBe(false)
  })
})
