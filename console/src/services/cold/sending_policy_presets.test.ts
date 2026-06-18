import { describe, it, expect } from 'vitest'
import {
  VERIDIAN_SENDING_POLICY_PRESETS,
  WARMUP_PRESET,
  CRUISE_PRESET,
  MICROSOFT_PRUDENCE_PRESET,
  veridianPresetById
} from './sending_policy_presets'
import { VERIDIAN_PROVIDER_CLASSES, VERIDIAN_WARMUP_PRESET } from '../api/workspace'

describe('sending_policy_presets', () => {
  it('exposes exactly the 3 presets, ordered prudent → large', () => {
    expect(VERIDIAN_SENDING_POLICY_PRESETS.map((p) => p.id)).toEqual([
      'warmup',
      'cruise',
      'microsoft_prudence'
    ])
  })

  it('warmup preset mirrors the canonical VERIDIAN_WARMUP_PRESET and excludes microsoft', () => {
    expect(WARMUP_PRESET.classDailyCap).toEqual(
      VERIDIAN_WARMUP_PRESET.veridian_provider_class_daily_cap
    )
    // Doctrine warm-up 2026-06-18 : le cap par sender individuel n'est PLUS posé en
    // warmup (le cap-classe par infra émettrice couvre la réputation par domaine).
    expect(WARMUP_PRESET.perSenderDailyCap).toBeUndefined()
    expect(WARMUP_PRESET.perRecipientDailyCap).toBe(1)
    expect(WARMUP_PRESET.rates?.google).toBe(0.5)
    expect(WARMUP_PRESET.excludedClasses).toEqual(['microsoft'])
    expect(WARMUP_PRESET.sendingWindow?.start_hour).toBe(9)
    expect(WARMUP_PRESET.sendingWindow?.end_hour).toBe(18)
  })

  it('cruise preset keeps microsoft (no exclusion) and uses a wider window', () => {
    expect(CRUISE_PRESET.excludedClasses).toEqual([])
    // Microsoft réactivé mais à un débit plus prudent que les autres classes.
    expect(CRUISE_PRESET.rates?.microsoft).toBe(2)
    expect(CRUISE_PRESET.rates?.corporate).toBe(5)
    expect(CRUISE_PRESET.classDailyCap?.microsoft).toBe(200)
    expect(CRUISE_PRESET.sendingWindow?.start_hour).toBe(8)
    expect(CRUISE_PRESET.sendingWindow?.end_hour).toBe(20)
  })

  it('microsoft-prudence is cruise + microsoft excluded', () => {
    expect(MICROSOFT_PRUDENCE_PRESET.excludedClasses).toEqual(['microsoft'])
    // identique à la croisière sur les autres dimensions
    expect(MICROSOFT_PRUDENCE_PRESET.classDailyCap).toEqual(CRUISE_PRESET.classDailyCap)
    expect(MICROSOFT_PRUDENCE_PRESET.rates).toEqual(CRUISE_PRESET.rates)
    expect(MICROSOFT_PRUDENCE_PRESET.perSenderDailyCap).toBe(CRUISE_PRESET.perSenderDailyCap)
  })

  it('every rate/cap map covers all known provider classes (no missing class)', () => {
    for (const preset of VERIDIAN_SENDING_POLICY_PRESETS) {
      for (const c of VERIDIAN_PROVIDER_CLASSES) {
        expect(preset.rates?.[c]).toBeTypeOf('number')
        expect(preset.classDailyCap?.[c]).toBeTypeOf('number')
      }
    }
  })

  it('all presets keep jitter and anti-hash on (cold hygiene)', () => {
    for (const preset of VERIDIAN_SENDING_POLICY_PRESETS) {
      expect(preset.jitterPct).toBeGreaterThan(0)
      expect(preset.antiHashEnabled).toBe(true)
      expect(preset.antiHashWindowHours).toBeGreaterThan(0)
    }
  })

  it('veridianPresetById resolves a known id and returns undefined otherwise', () => {
    expect(veridianPresetById('cruise')?.id).toBe('cruise')
    // @ts-expect-error — id inconnu volontaire
    expect(veridianPresetById('nope')).toBeUndefined()
  })
})
