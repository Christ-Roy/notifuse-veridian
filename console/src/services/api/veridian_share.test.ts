import { describe, it, expect, vi } from 'vitest'
import { shareInFlight } from './veridian_share'

describe('shareInFlight', () => {
  it('même demande en cours : une requête, même réponse ; terminée : elle repart', async () => {
    const run = vi.fn().mockResolvedValueOnce(1).mockResolvedValueOnce(2)
    const [a, b] = await Promise.all([shareInFlight('k', run), shareInFlight('k', run)])
    expect([a, b, run.mock.calls.length]).toEqual([1, 1, 1])
    expect(await shareInFlight('k', run)).toBe(2)
  })
  it('demandes différentes : chacune la sienne', async () => {
    const run = vi.fn().mockResolvedValue(0)
    await Promise.all([shareInFlight('a', run), shareInFlight('b', run)])
    expect(run).toHaveBeenCalledTimes(2)
  })
  it('un échec est rendu à tous, puis oublié', async () => {
    const run = vi.fn().mockRejectedValueOnce(new Error('x')).mockResolvedValueOnce(3)
    const p = shareInFlight('e', run)
    const q = shareInFlight('e', run)
    await expect(p).rejects.toThrow('x')
    await expect(q).rejects.toThrow('x')
    expect(await shareInFlight('e', run)).toBe(3)
  })
})
