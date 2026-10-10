import { describe, it, expect } from 'vitest'
import { render, screen } from '@testing-library/react'
import { i18n } from '@lingui/core'
import { I18nProvider } from '@lingui/react'

import { VeridianProspectionReplies } from './veridian_prospection_replies'
import type { ProspectionSequence, ProspectionStats } from '../../services/api/veridian_prospection'

i18n.loadAndActivate({ locale: 'en', messages: {} })

const seq = (over: Partial<ProspectionSequence>): ProspectionSequence =>
  ({
    automation_id: 'a1',
    name: 'ecom-devenir',
    status: 'live',
    list_id: 'l1',
    enrolled: 0,
    completed: 0,
    failed: 0,
    stages: [],
    exits: { rejected: 0, unsubscribed: 0, excluded: 0, other: 0 },
    replies_human: 0,
    replies_auto: 0,
    sent_contacts: 0,
    reply_rate_human: null,
    ...over
  }) as ProspectionSequence

const statsOf = (sequences: ProspectionSequence[]): ProspectionStats =>
  ({ generated_at: '2026-10-08T08:00:00Z', since: null, sequences, segments: [] }) as unknown as ProspectionStats

const renderIt = (props: Parameters<typeof VeridianProspectionReplies>[0]) =>
  render(
    <I18nProvider i18n={i18n}>
      <VeridianProspectionReplies {...props} />
    </I18nProvider>
  )

describe('VeridianProspectionReplies', () => {
  it('ajoute la ligne « Non rattachées » quand le total dépasse la somme des séquences', () => {
    const stats = statsOf([seq({ replies_human: 5, sent_contacts: 400, reply_rate_human: 0.0125 })])
    renderIt({ stats, loading: false, error: false, totalHuman: 9, totalAuto: 2 })
    expect(screen.getByTestId('replies-unattached')).toHaveTextContent('Unattached')
  })

  it('pas de ligne « Non rattachées » quand tout est rattaché', () => {
    const stats = statsOf([seq({ replies_human: 5, sent_contacts: 400, reply_rate_human: 0.0125 })])
    renderIt({ stats, loading: false, error: false, totalHuman: 5, totalAuto: 0 })
    expect(screen.queryByTestId('replies-unattached')).toBeNull()
  })

  it('marque « base faible » un taux sous 30 contacts joints, et pas au-dessus', () => {
    const stats = statsOf([
      seq({ automation_id: 'a1', name: 'petite', replies_human: 2, sent_contacts: 2, reply_rate_human: 1 }),
      seq({ automation_id: 'a2', name: 'grande', replies_human: 8, sent_contacts: 400, reply_rate_human: 0.02 })
    ])
    renderIt({ stats, loading: false, error: false })
    expect(screen.getAllByTestId('low-base-rate')).toHaveLength(1)
    expect(screen.getByText('low base')).toBeInTheDocument()
  })
})
