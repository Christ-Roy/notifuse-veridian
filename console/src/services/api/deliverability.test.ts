import { describe, it, expect, vi, beforeEach } from 'vitest'

vi.mock('./client', () => ({
  api: {
    post: vi.fn()
  }
}))

import { api } from './client'
import { deliverabilityApi } from './deliverability'
import type { VeridianDeliverabilityResult } from './deliverability'

describe('deliverabilityApi', () => {
  beforeEach(() => vi.clearAllMocks())

  it('POSTs the rendered template to the deliverability score endpoint', async () => {
    const result: VeridianDeliverabilityResult = {
      score: 2.5,
      is_risky: false,
      mode: 'strict',
      rules: [{ name: 'LINK_IN_STRICT_MODE', weight: 1.2, message: 'Liens présents' }],
      summary: 'Profil correct'
    }
    vi.mocked(api.post).mockResolvedValue(result)

    const res = await deliverabilityApi.score({
      workspace_id: 'ws-1',
      subject: 'Hello',
      body: '<p>Bonjour</p>',
      is_html: true,
      provider_class: 'google'
    })

    expect(api.post).toHaveBeenCalledWith('/api/veridian/templates.deliverabilityScore', {
      workspace_id: 'ws-1',
      subject: 'Hello',
      body: '<p>Bonjour</p>',
      is_html: true,
      provider_class: 'google'
    })
    expect(res.score).toBe(2.5)
    expect(res.rules[0].name).toBe('LINK_IN_STRICT_MODE')
  })

  it('forwards an explicit mode override', async () => {
    vi.mocked(api.post).mockResolvedValue({
      score: 0,
      is_risky: false,
      mode: 'lenient',
      rules: [],
      summary: 'Bon profil'
    })
    await deliverabilityApi.score({ workspace_id: 'ws-1', mode: 'lenient' })
    expect(api.post).toHaveBeenCalledWith('/api/veridian/templates.deliverabilityScore', {
      workspace_id: 'ws-1',
      mode: 'lenient'
    })
  })
})
