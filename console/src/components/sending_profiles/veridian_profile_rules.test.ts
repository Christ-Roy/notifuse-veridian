import { describe, expect, it } from 'vitest'

import { basePlan, planClass, profile } from './veridian_profile_test_fixtures'
import {
  canAddToRotation,
  canRemoveFromRotation,
  excludedClassesOf,
  groupProfiles,
  limitingFactor,
  profileBlockers,
  reputationIssues,
  todayProgress,
  windowReopening
} from './veridian_profile_rules'

describe('porte limitante', () => {
  it('nomme la chauffe avec son jour', () => {
    expect(limitingFactor(basePlan())).toEqual({ kind: 'warmup', day: 3, of: 5 })
  })

  it.each([
    ['profile_cap', 'profile_cap'],
    ['per_sender', 'per_sender'],
    ['class_cap', 'class_cap'],
    ['none', 'none'],
    ['inconnue', 'none']
  ])('%s -> %s', (gate, kind) => {
    expect(limitingFactor(basePlan({ limiting_gate: gate })).kind).toBe(kind)
  })
})

describe('progression du jour', () => {
  it('donne envoyés, réservés, plafond et pourcentage lus du serveur', () => {
    expect(todayProgress(basePlan())).toEqual({ sent: 149, reserved: 149, cap: 300, percent: 50 })
  })

  it('sans plafond: aucun pourcentage inventé', () => {
    expect(todayProgress(basePlan({ daily_cap_today: null }))).toMatchObject({ cap: null, percent: null })
  })

  it('plafond à zéro: barre pleine, pas de division par zéro', () => {
    expect(todayProgress(basePlan({ daily_cap_today: 0 })).percent).toBe(100)
  })

  it('ne dépasse jamais 100', () => {
    expect(todayProgress(basePlan({ daily_cap_today: 100, sent_today: 140 })).percent).toBe(100)
  })
})

describe('réouverture de la fenêtre', () => {
  const closed = {
    configured: true,
    open_now: false,
    next_open_at: '2026-10-08T06:00:00Z',
    timezone: 'Europe/Paris',
    source: 'profile'
  }

  it('lit l\'heure dans le fuseau de la fenêtre, le lendemain', () => {
    const reopening = windowReopening(closed, new Date('2026-10-07T19:28:00Z'))
    expect(reopening).toEqual({ time: '08:00', weekday: 4, daysAhead: 1 })
  })

  it('même jour: 0 jour d\'écart', () => {
    const reopening = windowReopening(
      { ...closed, next_open_at: '2026-10-07T06:00:00Z' },
      new Date('2026-10-07T02:00:00Z')
    )
    expect(reopening?.daysAhead).toBe(0)
    expect(reopening?.time).toBe('08:00')
  })

  it('rien à dire si la fenêtre est ouverte, absente ou sans prochaine ouverture', () => {
    expect(windowReopening({ ...closed, open_now: true })).toBeNull()
    expect(windowReopening({ ...closed, configured: false })).toBeNull()
    expect(windowReopening({ ...closed, next_open_at: undefined })).toBeNull()
  })

  it('fuseau inconnu: repli sur UTC sans lever', () => {
    expect(windowReopening({ ...closed, timezone: 'Nulle/Part' }, new Date('2026-10-07T19:00:00Z'))?.time).toBe('06:00')
  })
})

describe('ce qui bloque le profil maintenant', () => {
  const now = new Date('2026-10-07T19:28:00Z')

  it('fenêtre fermée: porte la réouverture', () => {
    const p = profile('a', {}, {
      blocked_by: ['window_closed'],
      window: {
        configured: true,
        open_now: false,
        next_open_at: '2026-10-08T06:00:00Z',
        timezone: 'Europe/Paris',
        source: 'profile'
      }
    })
    expect(profileBlockers(p, now)).toEqual([
      { kind: 'window_closed', reopening: { time: '08:00', weekday: 4, daysAhead: 1 } }
    ])
  })

  it('pause, non vérifié et classe arrêtée', () => {
    const p = profile(
      'b',
      { paused: true, verified: false },
      { blocked_by: ['paused', 'unverified'], classes: [planClass('google', { stopped: true, slowdown_factor: 1 })] }
    )
    expect(profileBlockers(p, now).map((b) => b.kind)).toEqual(['paused', 'unverified', 'reputation_stopped'])
  })

  it('profil transactionnel: aucune porte commerciale', () => {
    const p = profile('t', { usage: 'transactional' }, { applicable: false, mode: 'transactional', blocked_by: ['paused'] })
    expect(profileBlockers(p, now)).toEqual([])
  })

  it('profil sain: rien', () => {
    expect(profileBlockers(profile('c'), now)).toEqual([])
  })
})

describe('réputation par fournisseur destinataire', () => {
  it('ne garde que les classes ralenties ou arrêtées, avec facteur, raison et taux', () => {
    const plan = basePlan({
      classes: [
        planClass('google'),
        planClass('security_gateway', {
          slowdown_factor: 2,
          slowdown_reason: 'hard_bounce_rate',
          slowdown_rate: 0.099,
          sent_7d: 101
        }),
        planClass('microsoft', { stopped: true, slowdown_reason: 'bulk_policy_refusal' })
      ]
    })
    expect(reputationIssues(plan)).toEqual([
      { class: 'security_gateway', factor: 2, stopped: false, reason: 'hard_bounce_rate', rate: 0.099, sent7d: 101 },
      { class: 'microsoft', factor: 1, stopped: true, reason: 'bulk_policy_refusal', rate: null, sent7d: 0 }
    ])
  })

  it('aucune classe saine n\'est listée', () => {
    expect(reputationIssues(basePlan())).toEqual([])
  })

  it('classes exclues triées', () => {
    expect(excludedClassesOf(basePlan({ excluded_classes: ['ovh', 'ionos'] }))).toEqual(['ionos', 'ovh'])
  })
})

describe('exclusivité commercial / transactionnel et rotation', () => {
  it('un profil transactionnel ne peut pas rejoindre la rotation', () => {
    expect(canAddToRotation(profile('t', { usage: 'transactional', in_rotation: false }))).toEqual({
      allowed: false,
      reason: 'transactional'
    })
  })

  it('un profil non vérifié ne peut pas rejoindre la rotation', () => {
    expect(canAddToRotation(profile('u', { verified: false, in_rotation: false }))).toEqual({
      allowed: false,
      reason: 'unverified'
    })
  })

  it('un profil vérifié et commercial le peut', () => {
    expect(canAddToRotation(profile('v', { in_rotation: false }))).toEqual({ allowed: true })
  })

  it('le dernier profil de la rotation ne se retire pas', () => {
    const only = profile('a')
    const other = profile('b', { in_rotation: false })
    expect(canRemoveFromRotation(only, [only, other])).toEqual({ allowed: false, reason: 'last_profile' })
    const second = profile('c')
    expect(canRemoveFromRotation(only, [only, second])).toEqual({ allowed: true })
  })

  it('un profil transactionnel compte pour zéro dans la rotation', () => {
    const a = profile('a')
    const t = profile('t', { usage: 'transactional', in_rotation: false })
    expect(canRemoveFromRotation(a, [a, t])).toEqual({ allowed: false, reason: 'last_profile' })
  })

  it('un profil non affecté (créé, hors rotation) se range avec le commercial et peut rejoindre la rotation', () => {
    const fresh = profile('g', { usage: 'unassigned', in_rotation: false })
    expect(groupProfiles([fresh]).commercial).toHaveLength(1)
    expect(groupProfiles([fresh]).transactional).toHaveLength(0)
    expect(canAddToRotation(fresh)).toEqual({ allowed: true })
  })

  it('un profil n\'est jamais dans les deux groupes', () => {
    const a = profile('a')
    const b = profile('b', { in_rotation: false })
    const t = profile('t', { usage: 'transactional', in_rotation: false })
    const groups = groupProfiles([b, t, a])
    expect(groups.commercial.map((p) => p.integration_id)).toEqual(['a', 'b'])
    expect(groups.transactional.map((p) => p.integration_id)).toEqual(['t'])
  })
})
