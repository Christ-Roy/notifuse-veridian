import { describe, expect, it, vi } from 'vitest'
import { render, screen, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { i18n } from '@lingui/core'
import { I18nProvider } from '@lingui/react'

import type { EmailProvider } from '../../services/api/types'
import { clearProfileSetting, seedClassTable, settingSource } from './veridian_profile_inheritance'
import { VeridianProfileAdvanced } from './veridian_profile_advanced'

i18n.loadAndActivate({ locale: 'en', messages: {} })

const base: EmailProvider = { kind: 'smtp', rate_limit_per_minute: 60, senders: [] }

describe('origine d\'un réglage', () => {
  it('profil d\'abord, puis workspace, puis défaut', () => {
    expect(settingSource('class_caps', { ...base, veridian_provider_class_daily_cap: { google: 20 } as never }, { veridian_provider_class_daily_cap: { google: 5 } as never })).toBe('profile')
    expect(settingSource('class_caps', base, { veridian_provider_class_daily_cap: { google: 5 } as never })).toBe('workspace')
    expect(settingSource('class_caps', base, {})).toBe('default')
  })

  it('jitter 0 est une valeur explicite (opt-out), pas une absence', () => {
    expect(settingSource('jitter', { ...base, veridian_jitter_pct: 0 }, { veridian_jitter_pct: 0.3 })).toBe('profile')
    expect(settingSource('anti_hash', { ...base, veridian_anti_hash_enabled: false }, { veridian_anti_hash_enabled: true })).toBe('profile')
  })

  it('un plafond à 0 n\'est pas posé', () => {
    expect(settingSource('per_sender_cap', { ...base, veridian_per_sender_daily_cap: 0 }, { veridian_per_sender_daily_cap: 10 })).toBe('workspace')
  })

  it('remettre l\'héritage retire la valeur du profil', () => {
    const cleared = clearProfileSetting({ ...base, veridian_jitter_pct: 0.1 }, 'jitter')
    expect(cleared).not.toHaveProperty('veridian_jitter_pct')
  })

  it('personnaliser copie TOUTE la table héritée (aucune classe ne perd sa limite en silence)', () => {
    const ws = { veridian_provider_class_daily_cap: { google: 5, microsoft: 3 } as never }
    const seeded = seedClassTable('class_caps', base, ws)
    expect(seeded.veridian_provider_class_daily_cap).toEqual({ google: 5, microsoft: 3 })
    expect(seeded.veridian_provider_class_daily_cap).not.toBe(ws.veridian_provider_class_daily_cap)
  })
})

describe('réglages avancés', () => {
  const workspaceSettings = {
    timezone: 'Europe/Paris',
    veridian_provider_class_daily_cap: { google: 5 } as never,
    veridian_sending_window: { days: [1, 2, 3, 4, 5], start_hour: 9, end_hour: 18, timezone: 'Europe/Paris' },
    veridian_jitter_pct: 0.2
  }

  it('dit « hérité du workspace » au lieu de cacher le repli', () => {
    render(
      <I18nProvider i18n={i18n}>
        <VeridianProfileAdvanced value={base} onChange={vi.fn()} workspaceSettings={workspaceSettings} />
      </I18nProvider>
    )
    for (const title of ['Daily cap per provider', 'Sending window', 'Jitter (random spread of the pacing)']) {
      expect(within(screen.getByTestId(`setting-${title}`)).getByText('Inherited from the workspace')).toBeInTheDocument()
    }
    // un réglage sans repli annonce son défaut
    expect(within(screen.getByTestId('setting-Excluded provider classes')).getByText('Default')).toBeInTheDocument()
  })

  it('un réglage posé sur le profil le dit, avec le retour à l\'héritage', async () => {
    const onChange = vi.fn()
    render(
      <I18nProvider i18n={i18n}>
        <VeridianProfileAdvanced
          value={{ ...base, veridian_jitter_pct: 0.1 }}
          onChange={onChange}
          workspaceSettings={workspaceSettings}
        />
      </I18nProvider>
    )
    const row = screen.getByTestId('setting-Jitter (random spread of the pacing)')
    expect(within(row).getByText('Set on this profile')).toBeInTheDocument()
    await userEvent.click(within(row).getByRole('button', { name: 'Use the inherited value' }))
    expect(onChange).toHaveBeenCalledTimes(1)
    expect(onChange.mock.calls[0][0]).not.toHaveProperty('veridian_jitter_pct')
  })

  it('porte le frein technique SMTP, replié sous « Réglages avancés » avec le seuil du fusible', () => {
    render(
      <I18nProvider i18n={i18n}>
        <VeridianProfileAdvanced value={base} onChange={vi.fn()} workspaceSettings={workspaceSettings} />
      </I18nProvider>
    )
    expect(screen.getByText('SMTP technical brake (messages/minute)')).toBeInTheDocument()
    expect(screen.getByText('Reputation circuit breaker threshold')).toBeInTheDocument()
    expect(screen.getByText('Warmup')).toBeInTheDocument()
    expect(screen.getByText('Identical content guard')).toBeInTheDocument()
  })

  it('le seuil du fusible s\'écrit en proportion (8 % -> 0.08)', async () => {
    const onChange = vi.fn()
    render(
      <I18nProvider i18n={i18n}>
        <VeridianProfileAdvanced value={base} onChange={onChange} workspaceSettings={workspaceSettings} />
      </I18nProvider>
    )
    const input = screen.getByLabelText('freeze-threshold')
    await userEvent.type(input, '8')
    const last = onChange.mock.calls[onChange.mock.calls.length - 1][0] as EmailProvider
    expect(last.veridian_hard_bounce_freeze_threshold).toBeCloseTo(0.08, 5)
  })
})
