import { describe, it, expect } from 'vitest'

import {
  REJECTION_SERIES,
  buildSendsSeries,
  bucketKey,
  dayInTimezone,
  daySlots,
  hourSlots,
  nonReplyExits,
  rejectionTotals,
  reputationCouples,
  sumMeasure
} from './veridian_prospection_rules'
import { basePlan, overviewOf, planClass, profile } from '../sending_profiles/veridian_profile_test_fixtures'

describe('créneaux', () => {
  it('24 heures, 00 à 23', () => {
    const slots = hourSlots()
    expect(slots).toHaveLength(24)
    expect(slots[0]).toBe('00')
    expect(slots[23]).toBe('23')
  })

  it('jours de start inclus à end exclu (le tableau de bord borne à demain)', () => {
    expect(daySlots(['2026-10-06', '2026-10-09'])).toEqual(['2026-10-06', '2026-10-07', '2026-10-08'])
    expect(daySlots(['2026-10-06', '2026-10-06'])).toEqual([])
    expect(daySlots(['pas', 'une date'])).toEqual([])
  })

  it("date du jour dans un fuseau, avec décalage", () => {
    const now = new Date('2026-10-08T22:30:00Z') // 00h30 le 9 à Paris, 18h30 le 8 à New York
    expect(dayInTimezone('Europe/Paris', 0, now)).toBe('2026-10-09')
    expect(dayInTimezone('America/New_York', 0, now)).toBe('2026-10-08')
    expect(dayInTimezone('Europe/Paris', -1, now)).toBe('2026-10-08')
    expect(dayInTimezone('Pas/UnFuseau', 0, now)).toBe('2026-10-08')
  })

  it('clé de seau : jour ou heure de la colonne temporelle', () => {
    expect(bucketKey('2026-10-08T09:00:00Z', 'day')).toBe('2026-10-08')
    expect(bucketKey('2026-10-08T09:00:00Z', 'hour')).toBe('09')
    expect(bucketKey(undefined, 'day')).toBe('')
  })
})

describe('buildSendsSeries : envois par relais', () => {
  const names: Record<string, string> = { a: 'relais-a', b: 'relais-b' }
  const nameOf = (id: string) => (id === '' ? 'non attribué' : (names[id] ?? id))

  it('par heure : 24 créneaux, zéros remplis, un relais par série', () => {
    const rows = [
      { veridian_profile_id: 'a', sent_at_hour: '2026-10-08T09:00:00', count_sent: 12 },
      { veridian_profile_id: 'a', sent_at_hour: '2026-10-08T10:00:00', count_sent: '3' },
      { veridian_profile_id: 'b', sent_at_hour: '2026-10-08T09:00:00', count_sent: 5 }
    ]
    const out = buildSendsSeries(rows, 'hour', hourSlots(), nameOf)
    expect(out.buckets).toHaveLength(24)
    expect(out.series.map((s) => s.name)).toEqual(['relais-a', 'relais-b'])
    const a = out.series[0]
    expect(a.values[9]).toBe(12)
    expect(a.values[10]).toBe(3)
    expect(a.values[11]).toBe(0)
    expect(a.total).toBe(15)
    expect(out.total).toBe(20)
  })

  it('par jour : somme des lignes du même jour et du même relais, ligne hors créneaux conservée', () => {
    const rows = [
      { veridian_profile_id: 'a', sent_at_day: '2026-10-07T00:00:00', count_sent: 4 },
      { veridian_profile_id: 'a', sent_at_day: '2026-10-07T00:00:00', count_sent: 6 },
      { veridian_profile_id: '', sent_at_day: '2026-10-01T00:00:00', count_sent: 2 }
    ]
    const out = buildSendsSeries(rows, 'day', daySlots(['2026-10-06', '2026-10-09']), nameOf)
    expect(out.buckets).toEqual(['2026-10-01', '2026-10-06', '2026-10-07', '2026-10-08'])
    expect(out.series[0].name).toBe('relais-a')
    expect(out.series[0].values).toEqual([0, 0, 10, 0])
    expect(out.series[1].name).toBe('non attribué')
    expect(out.series[1].values).toEqual([2, 0, 0, 0])
  })

  it('aucune donnée : aucune série, total nul, créneaux gardés', () => {
    const out = buildSendsSeries(undefined, 'hour', hourSlots(), nameOf)
    expect(out.series).toEqual([])
    expect(out.total).toBe(0)
    expect(out.buckets).toHaveLength(24)
  })
})

describe('rejets, désinscriptions, plaintes', () => {
  it('une famille de mesures par date d’événement, refus de politique datés de l’envoi', () => {
    expect(REJECTION_SERIES.map((s) => s.dimension)).toEqual(['bounced_at', 'sent_at', 'complained_at', 'unsubscribed_at'])
    expect(REJECTION_SERIES[1].measures).toEqual(['count_policy_refused'])
  })

  it('somme les lignes de chaque famille', () => {
    const totals = rejectionTotals([
      [
        { count_bounced_hard: 2, count_bounced_soft: 1 },
        { count_bounced_hard: '3', count_bounced_soft: 0 }
      ],
      [{ count_policy_refused: 4 }],
      [{ count_complained: 1 }],
      [{ count_unsubscribed: 7 }, { count_unsubscribed: 1 }]
    ])
    expect(totals).toEqual({ hard: 5, soft: 1, policy: 4, complained: 1, unsubscribed: 8 })
    expect(rejectionTotals([undefined, undefined, undefined, undefined])).toEqual({
      hard: 0,
      soft: 0,
      policy: 0,
      complained: 0,
      unsubscribed: 0
    })
    expect(sumMeasure([{ x: 'abc' }, { x: 2 }], 'x')).toBe(2)
  })
})

describe('reputationCouples', () => {
  it('seulement les couples ralentis ou arrêtés des profils commerciaux, arrêts d’abord', () => {
    const overview = overviewOf([
      profile('a', {
        name: 'relais-a',
        plan: basePlan({
          classes: [
            planClass('google'),
            planClass('microsoft', { slowdown_factor: 4, slowdown_reason: 'hard_bounce_rate', slowdown_rate: 0.099, sent_7d: 40 }),
            planClass('yahoo_aol', { stopped: true, slowdown_factor: 4, slowdown_reason: 'bulk_policy_refusal' })
          ]
        })
      }),
      profile('b', {
        name: 'relais-b',
        plan: basePlan({ classes: [planClass('google', { slowdown_factor: 2, slowdown_reason: 'complaint' })] })
      }),
      profile('tx', {
        name: 'transactionnel',
        usage: 'transactional',
        plan: basePlan({ mode: 'transactional', classes: [planClass('google', { slowdown_factor: 4 })] })
      })
    ])
    const couples = reputationCouples(overview)
    expect(couples.map((c) => `${c.profileName}/${c.issue.class}`)).toEqual([
      'relais-a/yahoo_aol',
      'relais-a/microsoft',
      'relais-b/google'
    ])
    expect(couples[1].issue.rate).toBeCloseTo(0.099)
    expect(reputationCouples(undefined)).toEqual([])
  })
})

describe('sorties de séquence', () => {
  it('somme les sorties qui ne sont pas des réponses', () => {
    expect(nonReplyExits({ rejected: 1, unsubscribed: 2, excluded: 3, other: 4 })).toBe(10)
  })
})
