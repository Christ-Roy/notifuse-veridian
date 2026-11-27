import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { renderHook, act } from '@testing-library/react'
import type { TreeNode } from '../../services/api/segment'

const previewSegment = vi.fn()
vi.mock('../../services/api/segment', () => ({
  previewSegment: (req: unknown) => previewSegment(req)
}))

import { useLiveSegmentCount, LIVE_COUNT_DELAY_MS } from './veridian_live_count'

const arbre = (jours: string): TreeNode =>
  ({
    kind: 'leaf',
    leaf: {
      source: 'contacts',
      contact: {
        filters: [{ field_name: 'created_at', field_type: 'time', operator: 'in_the_last_days', string_values: [jours] }]
      }
    }
  }) as unknown as TreeNode

describe('useLiveSegmentCount (compte en direct, reprise amont 37.0)', () => {
  beforeEach(() => {
    vi.useFakeTimers()
    previewSegment.mockReset()
  })
  afterEach(() => {
    vi.useRealTimers()
  })

  it('met le compte à jour pendant l édition : un nouvel arbre donne un nouveau compte après la pause', async () => {
    previewSegment.mockResolvedValueOnce({ total_count: 5 }).mockResolvedValueOnce({ total_count: 9 })

    const { result, rerender } = renderHook(
      ({ tree }: { tree: TreeNode }) => useLiveSegmentCount(tree, 'ws1', true),
      { initialProps: { tree: arbre('30') } }
    )

    await act(async () => {
      vi.advanceTimersByTime(LIVE_COUNT_DELAY_MS)
    })
    expect(result.current.response?.total_count).toBe(5)
    expect(result.current.pending).toBe(false)

    // La condition change (7 jours au lieu de 30) : le compte ne bouge pas avant la pause
    rerender({ tree: arbre('7') })
    expect(result.current.pending).toBe(true)
    expect(result.current.response?.total_count).toBe(5)
    expect(previewSegment).toHaveBeenCalledTimes(1)

    await act(async () => {
      vi.advanceTimersByTime(LIVE_COUNT_DELAY_MS)
    })
    expect(previewSegment).toHaveBeenCalledTimes(2)
    expect(previewSegment.mock.calls[1][0].tree).toEqual(arbre('7'))
    expect(result.current.response?.total_count).toBe(9)
    expect(result.current.pending).toBe(false)
  })

  it('garde le dernier compte quand la condition est encore incomplète (erreur serveur)', async () => {
    previewSegment.mockResolvedValueOnce({ total_count: 5 }).mockRejectedValueOnce(new Error('incomplet'))

    const { result, rerender } = renderHook(
      ({ tree }: { tree: TreeNode }) => useLiveSegmentCount(tree, 'ws1', true),
      { initialProps: { tree: arbre('30') } }
    )
    await act(async () => {
      vi.advanceTimersByTime(LIVE_COUNT_DELAY_MS)
    })
    rerender({ tree: arbre('') })
    await act(async () => {
      vi.advanceTimersByTime(LIVE_COUNT_DELAY_MS)
    })
    expect(result.current.response?.total_count).toBe(5)
    expect(result.current.pending).toBe(false)
  })

  it('ne demande rien sans espace ou sans condition comptable', () => {
    renderHook(() => useLiveSegmentCount(arbre('30'), 'ws1', false))
    act(() => {
      vi.advanceTimersByTime(LIVE_COUNT_DELAY_MS * 2)
    })
    expect(previewSegment).not.toHaveBeenCalled()
  })
})
