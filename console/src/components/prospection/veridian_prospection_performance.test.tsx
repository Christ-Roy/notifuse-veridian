import { describe, it, expect } from 'vitest'
import { render, screen, fireEvent, within } from '@testing-library/react'
import { i18n } from '@lingui/core'
import { I18nProvider } from '@lingui/react'

import { VeridianProspectionPerformance } from './veridian_prospection_performance'
import type { ProspectionSequence, ProspectionStats, ProspectionStage } from '../../services/api/veridian_prospection'
import type { Template } from '../../services/api/template'

i18n.loadAndActivate({ locale: 'en', messages: {} })

const stage = (over: Partial<ProspectionStage> & Pick<ProspectionStage, 'stage' | 'label'>): ProspectionStage => ({
  node_ids: [],
  sent: 0,
  queued: 0,
  waiting: 0,
  exited_after: 0,
  template_ids: [],
  replies_human: 0,
  exited_replied: 0,
  exited_unsubscribed: 0,
  exited_rejected: 0,
  ...over
})

const sequence = (over: Partial<ProspectionSequence>): ProspectionSequence => ({
  automation_id: 'a',
  name: 'seq',
  status: 'live',
  list_id: 'l',
  enrolled: 0,
  completed: 0,
  failed: 0,
  stages: [],
  exits: { replied: 0, rejected: 0, unsubscribed: 0, excluded: 0, other: 0, total: 0, before_first_mail: 0 },
  replies_human: 0,
  replies_auto: 0,
  sent_contacts: 0,
  reply_rate_human: null,
  ...over
})

const templates = [
  { id: 'tplA', name: 'Accroche' },
  { id: 'tplB', name: 'Relance 1' },
  { id: 'tplC', name: 'Relance 2' }
] as unknown as Template[]

// prospection5mails : J0 peu de données (sous le seuil de 100), J+10 meilleur taux.
// seq2 : une étape à 100 envois (éligible, donc meilleure), une à 99 (non éligible, taux plus haut).
const stats = {
  generated_at: '',
  since: null,
  until: null,
  segments: [],
  totals: { stock_remaining: 0, queued_first_mail: 0 },
  sequences: [
    sequence({
      automation_id: 'seq1',
      name: 'prospection5mails',
      enrolled: 1797,
      stages: [
        stage({ stage: 1, label: 'J0', sent: 490, replies_human: 9, template_ids: ['tplA'], exited_unsubscribed: 3, exited_rejected: 4, exited_replied: 9 }),
        stage({ stage: 2, label: 'J+5', sent: 74, replies_human: 6, template_ids: ['tplB'] }),
        stage({ stage: 3, label: 'J+10', sent: 150, replies_human: 12, template_ids: ['tplC'] })
      ]
    }),
    sequence({
      automation_id: 'seq2',
      name: 'petite',
      enrolled: 300,
      stages: [
        stage({ stage: 1, label: 'J0', sent: 100, replies_human: 1, template_ids: ['tplA'] }),
        stage({ stage: 2, label: 'J+4', sent: 99, replies_human: 50, template_ids: ['tplB'] })
      ]
    })
  ]
} as unknown as ProspectionStats

const renderTable = (props: { stats?: ProspectionStats; error?: boolean; loading?: boolean } = {}) =>
  render(
    <I18nProvider i18n={i18n}>
      <VeridianProspectionPerformance stats={props.stats ?? stats} templates={templates} loading={props.loading ?? false} error={props.error ?? false} />
    </I18nProvider>
  )

const bodyRows = () => screen.getAllByRole('row').slice(1)
const rateValues = () => screen.getAllByTestId('perf-rate').map((el) => Number(el.getAttribute('data-value')))

describe('tableau Performance des campagnes', () => {
  it('une ligne par séquence et par étape, avec le nom des modèles', () => {
    renderTable()
    expect(bodyRows()).toHaveLength(5)
    expect(screen.getAllByText('Accroche')).toHaveLength(2)
    expect(screen.getByText('Relance 2')).toBeInTheDocument()
  })

  it('affiche inscrits, envois, réponses humaines, désinscriptions, rebonds et sorties sur réponse', () => {
    renderTable()
    const row = bodyRows()[0]
    const cells = within(row).getAllByRole('cell').map((c) => c.textContent)
    expect(cells.join(' | ')).toContain('1,797')
    expect(cells.join(' | ')).toContain('490')
    expect(cells.join(' | ')).toContain('9')
  })

  it('désigne le meilleur taux parmi les étapes d une séquence, à partir de 100 envois', () => {
    renderTable()
    const badges = screen.getAllByText('Best')
    expect(badges).toHaveLength(2)
    // seq1 : J+10 (8 %) est le seul éligible meilleur ; seq2 : J0 (100 envois, seul éligible), J+4 (99) jamais.
    expect(bodyRows()[2].textContent).toContain('Best')
    expect(bodyRows()[3].textContent).toContain('Best')
    expect(bodyRows()[4].textContent).not.toContain('Best')
  })

  it('sous le seuil : « trop peu de données », jamais désigné meilleur', () => {
    renderTable()
    expect(screen.getAllByText('Too little data')).toHaveLength(2)
    expect(screen.getByText(/100 sends/)).toBeInTheDocument()
  })

  it('trie par taux de réponse au clic sur l en-tête', () => {
    renderTable()
    const header = screen.getByRole('columnheader', { name: /Reply rate/ })
    fireEvent.click(header)
    const ascending = rateValues()
    fireEvent.click(header)
    const descending = rateValues()
    expect(ascending).toEqual([...ascending].sort((a, b) => a - b))
    expect(descending).toEqual([...descending].sort((a, b) => b - a))
  })

  it('erreur : message d erreur explicite', () => {
    renderTable({ error: true })
    expect(screen.getByText('Unable to load the prospection figures')).toBeInTheDocument()
  })
})
