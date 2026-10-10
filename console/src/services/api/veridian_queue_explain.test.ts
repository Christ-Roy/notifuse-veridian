import { describe, it, expect, vi, beforeEach } from 'vitest'

vi.mock('./client', () => ({ api: { get: vi.fn(), post: vi.fn() } }))

import { api } from './client'
import {
  decisionsService,
  queueExitService,
  queueExplainService,
  QUEUE_REASON_CODES,
  RECOMPUTE_LIMIT
} from './veridian_queue_explain'

describe('queueExplainService', () => {
  beforeEach(() => vi.clearAllMocks())

  it('lit queue.explain avec le workspace seul', async () => {
    vi.mocked(api.get).mockResolvedValue({ groups: [] })
    await queueExplainService.explain({ workspace_id: 'ws-1' })
    expect(api.get).toHaveBeenCalledWith('/api/veridian/queue.explain?workspace_id=ws-1')
  })

  it('passe les filtres et group_by (liste séparée par des virgules), sans les vides', async () => {
    vi.mocked(api.get).mockResolvedValue({ groups: [] })
    await queueExplainService.explain({
      workspace_id: 'ws-1',
      group_by: ['automation', 'reason'],
      automation_id: 'a1',
      node_id: 'j0a',
      reason: 'capacity',
      profile_id: '',
      class: 'ovh'
    })
    const url = vi.mocked(api.get).mock.calls[0][0] as string
    const params = new URLSearchParams(url.split('?')[1])
    expect(params.get('group_by')).toBe('automation,reason')
    expect(params.get('automation_id')).toBe('a1')
    expect(params.get('node_id')).toBe('j0a')
    expect(params.get('reason')).toBe('capacity')
    expect(params.get('class')).toBe('ovh')
    expect(params.has('profile_id')).toBe(false)
  })

  it("lit le détail d'une entrée avec entry_id", async () => {
    vi.mocked(api.get).mockResolvedValue({ groups: [], entry: null })
    await queueExplainService.explain({ workspace_id: 'ws-1', entry_id: 'e1' })
    expect(api.get).toHaveBeenCalledWith('/api/veridian/queue.explain?workspace_id=ws-1&entry_id=e1')
  })

  it('recompute : POST avec la limite, bornée à 5000 et au moins 1', async () => {
    vi.mocked(api.post).mockResolvedValue({ recomputed: 3 })
    await queueExplainService.recompute({ workspace_id: 'ws-1', automation_id: 'a1', limit: 999999 })
    expect(api.post).toHaveBeenCalledWith('/api/veridian/queue.recompute', {
      workspace_id: 'ws-1',
      automation_id: 'a1',
      limit: RECOMPUTE_LIMIT
    })
    await queueExplainService.recompute({ workspace_id: 'ws-1', entry_ids: ['e1'], limit: 0 })
    expect(vi.mocked(api.post).mock.calls[1][1]).toMatchObject({ limit: 1 })
  })

  it('connaît les 16 codes de raison du contrat', () => {
    expect([...QUEUE_REASON_CODES].sort()).toEqual(
      [
        'not_examined',
        'window_closed',
        'capacity',
        'class_rate',
        'reputation_stopped',
        'excluded_class',
        'profile_paused',
        'no_profile_in_pool',
        'circuit_open',
        'anchor_wait',
        'quota_denied',
        'render_failed',
        'guard_retry',
        'automation_paused',
        'send_error',
        'deferred_legacy'
      ].sort()
    )
  })
})

describe('decisionsService', () => {
  beforeEach(() => vi.clearAllMocks())

  it('lit decisions.list : filtres, limite, curseur, trace=1', async () => {
    vi.mocked(api.get).mockResolvedValue({ decisions: [], next_cursor: '' })
    await decisionsService.list({
      workspace_id: 'ws-1',
      email: 'a@example.test',
      outcome: 'deferred',
      reason: 'capacity',
      limit: 50,
      cursor: 'abc',
      trace: true
    })
    const url = vi.mocked(api.get).mock.calls[0][0] as string
    expect(url.startsWith('/api/veridian/decisions.list?')).toBe(true)
    const params = new URLSearchParams(url.split('?')[1])
    expect(Object.fromEntries(params)).toEqual({
      workspace_id: 'ws-1',
      email: 'a@example.test',
      reason: 'capacity',
      outcome: 'deferred',
      cursor: 'abc',
      limit: '50',
      trace: '1'
    })
  })

  it('sans trace : pas de paramètre trace', async () => {
    vi.mocked(api.get).mockResolvedValue({ decisions: [], next_cursor: '' })
    await decisionsService.list({ workspace_id: 'ws-1' })
    expect(api.get).toHaveBeenCalledWith('/api/veridian/decisions.list?workspace_id=ws-1')
  })
})

describe('queueExitService', () => {
  it('sort un contact par la route existante automations.exitContact', async () => {
    vi.mocked(api.post).mockResolvedValue({})
    await queueExitService.exitContact({ workspace_id: 'ws-1', automation_id: 'a1', email: 'a@example.test' })
    expect(api.post).toHaveBeenCalledWith('/api/automations.exitContact', {
      workspace_id: 'ws-1',
      automation_id: 'a1',
      email: 'a@example.test'
    })
  })
})
