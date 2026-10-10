import { describe, it, expect, afterEach } from 'vitest'
import { i18n } from '@lingui/core'
import dayjs from '../../lib/dayjs'
import { setDayjsLocale } from '../../lib/dayjs'
import { formatDate, formatNumber, formatRatePercent } from './veridian_profile_labels'
import { formatDateTime } from '../send_queue/veridian_queue_labels'

describe('formats en français', () => {
  afterEach(() => {
    i18n.loadAndActivate({ locale: 'en', messages: {} })
    setDayjsLocale('en')
  })

  it('nombres : espace insécable des milliers', () => {
    i18n.loadAndActivate({ locale: 'fr', messages: {} })
    expect(formatNumber(12345)).toMatch(/^12\s345$/)
    expect(formatNumber(12345)).toMatch(/\u202f|\u00a0/)
  })

  it('taux : virgule décimale en français, point en anglais', () => {
    i18n.loadAndActivate({ locale: 'fr', messages: {} })
    expect(formatRatePercent(2.6)).toMatch(/^2,6\s%$/)
    i18n.loadAndActivate({ locale: 'en', messages: {} })
    expect(formatRatePercent(2.6)).toBe('2.6%')
  })

  it('dates : fuseau Europe/Paris, jamais UTC (23h30 UTC le 31/12 = 1er janvier à Paris)', () => {
    i18n.loadAndActivate({ locale: 'fr', messages: {} })
    expect(formatDate('2026-12-31T23:30:00Z')).toMatch(/1 janv\. 2027/)
    expect(formatDateTime('2026-12-31T23:30:00Z')).toMatch(/01\/01\/2027.*00:30/)
  })

  it('dates relatives : « il y a 3 jours » en français, jamais « days ago »', () => {
    setDayjsLocale('fr')
    const rel = dayjs().subtract(3, 'day').fromNow()
    expect(rel).toBe('il y a 3 jours')
    setDayjsLocale('en')
    expect(dayjs().subtract(3, 'day').fromNow()).toBe('3 days ago')
  })
})
