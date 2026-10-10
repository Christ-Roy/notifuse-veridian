import { describe, it, expect } from 'vitest'

import {
  buildQueueTree,
  collectFilterOptions,
  defaultExpandedKeys,
  formatDelaySeconds,
  formatGateValue,
  hasOrphans,
  outcomeColor,
  reasonTotals,
  verdictColor
} from './veridian_queue_rules'
import { explainFixture, group } from './veridian_queue_test_fixtures'

describe('reasonTotals', () => {
  it('somme par raison, trié du plus gros au plus petit, dates en min / max', () => {
    const groups = [
      group({ reason: 'window_closed', count: 10, oldest_created_at: '2026-10-03T00:00:00Z', next_attempt_min: '2026-10-12T06:00:00Z', next_attempt_max: '2026-10-12T06:00:00Z' }),
      group({ reason: 'window_closed', node_id: 'j4', count: 5, oldest_created_at: '2026-10-01T00:00:00Z', next_attempt_min: '2026-10-11T06:00:00Z', next_attempt_max: '2026-10-13T06:00:00Z' }),
      group({ reason: 'capacity', count: 40, never_examined: 3, next_attempt_min: null, next_attempt_max: null })
    ]
    const totals = reasonTotals(groups)
    expect(totals.map((r) => [r.reason, r.count])).toEqual([
      ['capacity', 40],
      ['window_closed', 15]
    ])
    const closed = totals[1]
    expect(closed.oldest).toBe('2026-10-01T00:00:00Z')
    expect(closed.next_min).toBe('2026-10-11T06:00:00Z')
    expect(closed.next_max).toBe('2026-10-13T06:00:00Z')
    expect(totals[0].never_examined).toBe(3)
    expect(totals[0].next_min).toBeNull()
  })

  it('le total des raisons égale le total du contrat', () => {
    const f = explainFixture()
    expect(reasonTotals(f.groups).reduce((a, r) => a + r.count, 0)).toBe(f.total)
  })
})

describe('buildQueueTree', () => {
  it('range automation > noeud > raison > profil, agrégats des parents = somme des enfants', () => {
    const tree = buildQueueTree(explainFixture().groups)
    expect(tree).toHaveLength(1)
    const auto = tree[0]
    expect(auto.level).toBe('automation')
    expect(auto.count).toBe(2074)
    expect(auto.children!.map((n) => [n.value, n.count])).toEqual([
      ['j0a', 1774],
      ['j4', 300]
    ])
    const j0a = auto.children![0]
    expect(j0a.children!.map((r) => [r.value, r.count])).toEqual([
      ['window_closed', 1200],
      ['not_examined', 574]
    ])
    const leaf = j0a.children![0].children![0]
    expect(leaf.level).toBe('profile')
    expect(leaf.sample_entry_ids).toEqual(['entry-aaaa1111', 'entry-bbbb2222'])
  })

  it("porte les filtres cumulés de la racine à la ligne, pour borner un recalcul", () => {
    const tree = buildQueueTree(explainFixture().groups)
    const reasonRow = tree[0].children![0].children![0]
    expect(reasonRow).toMatchObject({ automation_id: 'auto-1', node_id: 'j0a', reason: 'window_closed' })
    expect(reasonRow.profile_id).toBeUndefined()
    expect(reasonRow.children![0]).toMatchObject({ profile_id: 'p-nord', reason: 'window_closed' })
    expect(tree[0]).toMatchObject({ automation_id: 'auto-1' })
    expect(tree[0].node_id).toBeUndefined()
  })

  it('un groupe sans profil reste une feuille avec son échantillon', () => {
    const tree = buildQueueTree(explainFixture().groups)
    const notExamined = tree[0].children![0].children![1]
    expect(notExamined.value).toBe('not_examined')
    const leaf = notExamined.children![0]
    expect(leaf.value).toBe('')
    expect(leaf.sample_entry_ids).toEqual(['entry-cccc3333'])
    expect(leaf.profile_id).toBeUndefined()
  })

  it('liste vide : arbre vide', () => {
    expect(buildQueueTree([])).toEqual([])
    expect(defaultExpandedKeys([])).toEqual([])
  })

  it('déplie par défaut tout sauf les feuilles', () => {
    const tree = buildQueueTree(explainFixture().groups)
    const keys = defaultExpandedKeys(tree)
    expect(keys).toContain(tree[0].key)
    expect(keys).toContain(tree[0].children![0].children![0].key)
    expect(keys).not.toContain(tree[0].children![0].children![0].children![0].key)
  })
})

describe('collectFilterOptions', () => {
  it('relève automations, noeuds, profils et classes, et garde ceux déjà vus', () => {
    const first = collectFilterOptions(explainFixture().groups)
    expect(first.automations).toEqual([{ id: 'auto-1', name: 'E-commerce a devenir' }])
    expect(first.nodes).toEqual(['j0a', 'j4'])
    expect(first.profiles).toEqual([{ id: 'p-nord', name: 'Relais nord' }])
    const second = collectFilterOptions([group({ node_id: 'j10', class: 'ovh' })], first)
    expect(second.nodes).toEqual(['j0a', 'j10', 'j4'])
    expect(second.classes).toEqual(['ovh'])
  })
})

describe('petites règles', () => {
  it('hasOrphans', () => {
    expect(hasOrphans(null)).toBe(false)
    expect(hasOrphans({ count: 0, by_node: [] })).toBe(false)
    expect(hasOrphans({ count: 89, by_node: [] })).toBe(true)
  })

  it('couleurs de verdict et d issue', () => {
    expect(verdictColor('pass')).toBe('green')
    expect(verdictColor('block')).toBe('red')
    expect(verdictColor('slowed')).toBe('orange')
    expect(verdictColor('skipped')).toBe('default')
    expect(outcomeColor('selected')).toBe('green')
    expect(outcomeColor('deferred')).toBe('orange')
    expect(outcomeColor('failed')).toBe('red')
  })

  it('formatGateValue ne lève jamais', () => {
    expect(formatGateValue(null)).toBe('—')
    expect(formatGateValue(60)).toBe('60')
    expect(formatGateValue('warmup')).toBe('warmup')
    expect(formatGateValue({ hour: 22 })).toBe('{"hour":22}')
    const circular: Record<string, unknown> = {}
    circular.self = circular
    expect(() => formatGateValue(circular)).not.toThrow()
  })

  it('formatDelaySeconds : deux unités au plus', () => {
    expect(formatDelaySeconds(300, 'en')).toBe('5m')
    expect(formatDelaySeconds(165600, 'en')).toBe('1d 22h')
    expect(formatDelaySeconds(0, 'en')).toBe('—')
    expect(formatDelaySeconds(undefined, 'en')).toBe('—')
  })
})
