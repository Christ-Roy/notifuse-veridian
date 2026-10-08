import { describe, expect, it, vi } from 'vitest'

vi.mock('./client', () => ({ api: { get: vi.fn() } }))

import { api } from './client'
import { listMessages } from './messages_history'

describe('listMessages : message_type', () => {
  it('envoie message_type=transactional', async () => {
    vi.mocked(api.get).mockResolvedValue({ messages: [], has_more: false })
    await listMessages('ws-1', { message_type: 'transactional', limit: 20 })
    const url = vi.mocked(api.get).mock.calls.at(-1)![0] as string
    expect(url).toContain('message_type=transactional')
    expect(url).toContain('workspace_id=ws-1')
  })

  it('envoie message_type=commercial', async () => {
    vi.mocked(api.get).mockResolvedValue({ messages: [], has_more: false })
    await listMessages('ws-1', { message_type: 'commercial' })
    expect(vi.mocked(api.get).mock.calls.at(-1)![0]).toContain('message_type=commercial')
  })

  it("n'envoie rien quand le type est absent (autres appelants inchanges)", async () => {
    vi.mocked(api.get).mockResolvedValue({ messages: [], has_more: false })
    await listMessages('ws-1', { is_failed: true })
    expect(vi.mocked(api.get).mock.calls.at(-1)![0]).not.toContain('message_type')
  })
})
