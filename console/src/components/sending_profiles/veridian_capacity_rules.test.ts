import { describe, expect, it } from 'vitest'

import { basePlan, planWithCapacity } from './veridian_profile_test_fixtures'
import { capacityBar, capacityLimit, capacityRows } from './veridian_profile_rules'

const plan = (cap = {}, over = {}) => basePlan(planWithCapacity(cap, over))

describe('capacité du jour: barre', () => {
  it('envoyé + restant + inutilisable = capacité configurée', () => {
    const bar = capacityBar(plan())
    expect(bar).toEqual({ sent: 20, remaining: 50, unusable: 50, total: 120, useful: 70, stockKnown: true })
    expect(bar!.sent + bar!.remaining + bar!.unusable).toBe(bar!.total)
  })

  it('aucune barre sans capacité du serveur (serveur plus ancien)', () => {
    expect(capacityBar(basePlan())).toBeNull()
  })

  it('aucune barre quand la capacité est illimitée', () => {
    expect(capacityBar(plan({ configured: null, remaining_configured: null }))).toBeNull()
  })

  it('profil non applicable: pas de barre', () => {
    expect(capacityBar(plan({ applicable: false }))).toBeNull()
  })
})

describe('capacité du jour: limite en clair', () => {
  it('une porte de profil, la chauffe avec son jour', () => {
    expect(capacityLimit(plan({ limiting: 'warmup' }))).toEqual({ kind: 'warmup', day: 3, of: 5 })
  })

  it('le plafond plat, avec sa valeur', () => {
    expect(capacityLimit(plan({ limiting: 'profile_cap', flat_cap: 300 }))).toEqual({ kind: 'profile_cap', cap: 300 })
  })

  it('les débits, avec la classe qui pèse le plus', () => {
    expect(capacityLimit(plan())).toEqual({ kind: 'class_rates', class: 'ovh' })
  })

  it.each([
    ['class_caps', 'class_caps'],
    ['window_closed', 'window_closed'],
    ['classes_blocked', 'classes_blocked'],
    ['per_sender', 'per_sender'],
    ['none', 'none'],
    ['inconnu', 'none']
  ])('%s', (limiting, kind) => {
    expect(capacityLimit(plan({ limiting })).kind).toBe(kind)
  })
})

describe('capacité du jour: détail par fournisseur', () => {
  it('trié par capacité décroissante, restant et inutilisable déduits', () => {
    const rows = capacityRows(plan())
    expect(rows.map((r) => r.class)).toEqual(['ovh', 'google', 'ionos'])
    expect(rows[0]).toMatchObject({ sent: 20, configured: 99, useful: 70, remaining: 50, unusable: 29, stock: 50 })
    expect(rows[1]).toMatchObject({ remaining: 0, unusable: 41, stock: 0 })
    expect(rows[2]).toMatchObject({ excluded: true, configured: 0, limiter: 'excluded' })
  })

  it('une classe sans limite de volume reste nulle, jamais 0', () => {
    const first = { ...planWithCapacity().classes![0], capacity_configured: null, capacity_useful: null }
    const rows = capacityRows(plan({}, { classes: [first] }))
    expect(rows[0]).toMatchObject({ configured: null, useful: null, remaining: null, unusable: null })
  })
})
