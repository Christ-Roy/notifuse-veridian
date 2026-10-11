import { describe, it, expect, afterEach } from 'vitest'
import { render, screen, within } from '@testing-library/react'
import { i18n } from '@lingui/core'
import { I18nProvider } from '@lingui/react'

import dayjs, { setDayjsLocale } from '../../lib/dayjs'
import { VeridianProspectionRecentReplies } from './veridian_prospection_recent_replies'
import type { ProspectionStats } from '../../services/api/veridian_prospection'

const statsWith = (replies: ProspectionStats['recent_human_replies']): ProspectionStats =>
  ({ generated_at: '2026-10-11T08:00:00Z', since: null, until: null, sequences: [], segments: [], totals: { stock_remaining: 0, queued_first_mail: 0 }, recent_human_replies: replies }) as ProspectionStats

const renderIt = (stats: ProspectionStats | undefined, locale: 'fr' | 'en') => {
  i18n.loadAndActivate({ locale, messages: {} })
  setDayjsLocale(locale)
  return render(
    <I18nProvider i18n={i18n}>
      <VeridianProspectionRecentReplies stats={stats} loading={false} error={false} timezone="Europe/Paris" />
    </I18nProvider>
  )
}

describe('VeridianProspectionRecentReplies', () => {
  afterEach(() => {
    i18n.loadAndActivate({ locale: 'en', messages: {} })
    setDayjsLocale('en')
  })

  it('liste société, contact, séquence et date relative en français (« il y a 3 jours »)', () => {
    const when = dayjs().subtract(3, 'day').toISOString()
    renderIt(statsWith([{ contact_email: 'a@acme.fr', company: 'ACME SARL', sequence: 'E-commerce à devenir', replied_at: when }]), 'fr')
    const card = screen.getByTestId('recent-human-replies')
    expect(within(card).getByText('ACME SARL')).toBeInTheDocument()
    expect(within(card).getByText('a@acme.fr')).toBeInTheDocument()
    expect(within(card).getByText('E-commerce à devenir')).toBeInTheDocument()
    expect(screen.getByTestId('reply-relative-date')).toHaveTextContent('il y a 3 jours')
    expect(card).not.toHaveTextContent('days ago')
  })

  it('en anglais la même date dit « days ago »', () => {
    const when = dayjs().subtract(3, 'day').toISOString()
    renderIt(statsWith([{ contact_email: 'a@acme.fr', company: '', sequence: '', replied_at: when }]), 'en')
    expect(screen.getByTestId('reply-relative-date')).toHaveTextContent('3 days ago')
    expect(screen.getByText('Unknown company')).toBeInTheDocument()
  })

  it("état vide : le dit au lieu d'un tableau muet", () => {
    renderIt(statsWith([]), 'en')
    expect(screen.getByText('No human reply on this period')).toBeInTheDocument()
  })
})
