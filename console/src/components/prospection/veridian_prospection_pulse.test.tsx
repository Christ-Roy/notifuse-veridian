import React from 'react'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { act, render } from '@testing-library/react'
import { focusManager, QueryClient, QueryClientProvider, useQuery } from '@tanstack/react-query'

const box = vi.hoisted(() => ({ count: 3, loaded: true }))
vi.mock('../mail/veridian_mail_queries', () => ({ useUnhandledReplies: () => box }))

import { PROSPECTION_SLOW_MS, useProspectionPulse } from './veridian_prospection_pulse'

const totals = vi.fn(async () => 1)
const other = vi.fn(async () => 1)

function setup(sent: number | undefined, ws = 'ws') {
  const client = new QueryClient()
  const Page: React.FC<{ ws: string; sent?: number }> = (p) => {
    useProspectionPulse(p.ws, p.sent)
    useQuery({ queryKey: ['prospection', 'stats', p.ws], queryFn: totals, refetchInterval: PROSPECTION_SLOW_MS })
    useQuery({ queryKey: ['templates', p.ws], queryFn: other })
    return null
  }
  const ui = (s: number | undefined, w = ws) => (
    <QueryClientProvider client={client}>
      <Page ws={w} sent={s} />
    </QueryClientProvider>
  )
  const r = render(ui(sent))
  return { set: (s: number | undefined, w = ws) => r.rerender(ui(s, w)) }
}
const settle = () => act(async () => void (await vi.advanceTimersByTimeAsync(50)))

describe('relecture du tableau de bord', () => {
  beforeEach(() => {
    vi.useFakeTimers()
    vi.clearAllMocks()
    box.count = 3
    box.loaded = true
  })
  afterEach(() => {
    focusManager.setFocused(undefined)
    vi.useRealTimers()
  })

  it('rien ne bouge : aucune relecture en neuf minutes, une seule à dix', async () => {
    const p = setup(17)
    await settle()
    for (let m = 0; m < 9; m++) {
      await act(async () => void (await vi.advanceTimersByTimeAsync(60_000)))
      p.set(17)
    }
    expect(totals).toHaveBeenCalledTimes(1)
    await act(async () => void (await vi.advanceTimersByTimeAsync(60_000)))
    expect(totals).toHaveBeenCalledTimes(2)
  })

  it('un envoi ou une réponse de plus : les totaux sont relus, une fois, et rien d’autre', async () => {
    const p = setup(17)
    await settle()
    p.set(18)
    await settle()
    expect(totals).toHaveBeenCalledTimes(2)
    box.count = 4
    p.set(18)
    await settle()
    expect(totals).toHaveBeenCalledTimes(3)
    p.set(18)
    await settle()
    expect(totals).toHaveBeenCalledTimes(3)
    expect(other).toHaveBeenCalledTimes(1)
  })

  it('première lecture qui arrive, ou changement d’espace : pas de relecture de plus', async () => {
    const p = setup(undefined)
    await settle()
    p.set(17)
    await settle()
    expect(totals).toHaveBeenCalledTimes(1)
    p.set(5, 'autre')
    await settle()
    expect(totals).toHaveBeenCalledTimes(2) // la première lecture de l'autre espace
  })

  it('onglet caché : rien ne part, même à dix minutes', async () => {
    setup(17)
    await settle()
    focusManager.setFocused(false)
    await act(async () => void (await vi.advanceTimersByTimeAsync(3 * PROSPECTION_SLOW_MS)))
    expect(totals).toHaveBeenCalledTimes(1)
  })
})
