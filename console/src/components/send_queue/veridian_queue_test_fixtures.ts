// Fixtures du LOT 1 : forme exacte du contrat (fiche 62). Aucun secret.

import type {
  DecisionTrace,
  QueueDecision,
  QueueEntryDetail,
  QueueExplain,
  QueueGroup
} from '../../services/api/veridian_queue_explain'

export const group = (overrides: Partial<QueueGroup> = {}): QueueGroup => ({
  automation_id: 'auto-1',
  automation_name: 'E-commerce a devenir',
  node_id: 'j0a',
  reason: 'window_closed',
  reason_detail: '',
  profile_id: 'p-nord',
  profile_name: 'Relais nord',
  class: '',
  count: 1200,
  never_examined: 0,
  oldest_created_at: '2026-10-01T08:00:00Z',
  next_attempt_min: '2026-10-12T06:00:00Z',
  next_attempt_max: '2026-10-12T06:00:00Z',
  sample_entry_ids: ['entry-aaaa1111', 'entry-bbbb2222'],
  ...overrides
})

export const explainFixture = (overrides: Partial<QueueExplain> = {}): QueueExplain => ({
  workspace_id: 'ws-1',
  generated_at: '2026-10-10T12:00:00Z',
  total: 1200 + 574 + 300,
  groups: [
    group(),
    group({
      reason: 'not_examined',
      profile_id: '',
      profile_name: '',
      count: 574,
      never_examined: 574,
      next_attempt_min: null,
      next_attempt_max: null,
      sample_entry_ids: ['entry-cccc3333']
    }),
    group({
      node_id: 'j4',
      reason: 'capacity',
      reason_detail: 'warmup',
      count: 300,
      next_attempt_min: '2026-10-11T06:00:00Z',
      next_attempt_max: '2026-10-12T06:00:00Z',
      sample_entry_ids: ['entry-dddd4444']
    })
  ],
  orphans: { count: 0, by_node: [] },
  entry: null,
  ...overrides
})

export const trace = (overrides: Partial<DecisionTrace> = {}): DecisionTrace => ({
  class: 'ovh',
  level: 'full',
  anchor: null,
  candidates: [
    {
      profile: 'p-nord',
      profile_name: 'Relais nord',
      from: 'nord@example.test',
      outcome: 'blocked',
      gates: [
        { gate: 'excluded', verdict: 'pass', value: false, limit: null },
        { gate: 'reputation', verdict: 'slowed', value: 0.07, limit: 0.05, delay_s: 300 },
        { gate: 'daily_cap', verdict: 'block', value: 60, limit: 60, name: 'warmup' },
        { gate: 'window', verdict: 'skipped', value: { hour: 22 }, limit: { from: 8, to: 19 }, detail: 'closed' }
      ]
    },
    {
      profile: 'p-sud',
      profile_name: 'Relais sud',
      from: 'sud@example.test',
      outcome: 'selected',
      gates: [{ gate: 'class_rate', verdict: 'pass', value: 3, limit: 10 }]
    }
  ],
  decision: { outcome: 'deferred', reason: 'window_closed', detail: '', until: '2026-10-12T06:00:00Z', delay_s: 165600 },
  ...overrides
})

export const decision = (overrides: Partial<QueueDecision> = {}): QueueDecision => ({
  id: 'dec-1',
  at: '2026-10-10T11:00:00Z',
  entry_id: 'entry-aaaa1111',
  message_id: '',
  contact_email: 'contact@example.test',
  automation_id: 'auto-1',
  node_id: 'j0a',
  outcome: 'deferred',
  reason: 'window_closed',
  detail: '',
  until: '2026-10-12T06:00:00Z',
  profile_id: 'p-nord',
  profile_name: 'Relais nord',
  sampled: false,
  trace: trace(),
  ...overrides
})

export const entryDetail = (overrides: Partial<QueueEntryDetail> = {}): QueueEntryDetail => ({
  id: 'entry-aaaa1111',
  status: 'pending',
  automation_id: 'auto-1',
  automation_name: 'E-commerce a devenir',
  node_id: 'j0a',
  contact_email: 'contact@example.test',
  integration_id: 'int-1',
  profile_name: 'Relais nord',
  class: 'ovh',
  created_at: '2026-10-01T08:00:00Z',
  attempts: 0,
  max_attempts: 3,
  next_retry_at: '2026-10-12T06:00:00Z',
  reason: 'window_closed',
  reason_detail: '',
  deferred_at: '2026-10-10T11:00:00Z',
  defer_until: '2026-10-12T06:00:00Z',
  defer_count: 2,
  first_examined_at: '2026-10-09T08:00:00Z',
  last_examined_at: '2026-10-10T11:00:00Z',
  last_error: '',
  last_decision: decision(),
  ...overrides
})
