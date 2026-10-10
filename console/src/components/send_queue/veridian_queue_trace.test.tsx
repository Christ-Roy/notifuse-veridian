import { describe, it, expect } from 'vitest'
import { render, screen, within } from '@testing-library/react'
import { i18n } from '@lingui/core'
import { I18nProvider } from '@lingui/react'

import { VeridianQueueTrace } from './veridian_queue_trace'
import { trace } from './veridian_queue_test_fixtures'

i18n.loadAndActivate({ locale: 'en', messages: {} })

const renderTrace = (t = trace()) =>
  render(
    <I18nProvider i18n={i18n}>
      <VeridianQueueTrace trace={t} />
    </I18nProvider>
  )

describe('trace gate par gate', () => {
  it('un bloc par profil candidat, avec son issue', () => {
    renderTrace()
    const blocks = screen.getAllByTestId('queue-candidate')
    expect(blocks).toHaveLength(2)
    expect(within(blocks[0]).getByText('Relais nord')).toBeInTheDocument()
    expect(within(blocks[0]).getAllByText('Blocked').length).toBeGreaterThan(0)
    expect(within(blocks[1]).getByText('Relais sud')).toBeInTheDocument()
    expect(within(blocks[1]).getByText('Selected')).toBeInTheDocument()
  })

  it('chaque porte : nom, verdict en pastille colorée, valeur, limite, délai, nom de la limite', () => {
    renderTrace()
    const first = screen.getAllByTestId('queue-candidate')[0]
    const rows = within(first).getAllByRole('row').slice(1)
    expect(rows).toHaveLength(4)

    const reputation = rows[1]
    expect(within(reputation).getByText('Reputation fuse')).toBeInTheDocument()
    expect(within(reputation).getByText('Slowed').className).toContain('ant-tag-orange')
    expect(within(reputation).getByText('0.07')).toBeInTheDocument()
    expect(within(reputation).getByText('0.05')).toBeInTheDocument()
    expect(within(reputation).getByText('5m')).toBeInTheDocument()

    const daily = rows[2]
    expect(within(daily).getByText('Daily cap')).toBeInTheDocument()
    expect(within(daily).getByText('Blocked').className).toContain('ant-tag-red')
    expect(within(daily).getByText('warmup')).toBeInTheDocument()

    expect(within(rows[0]).getByText('Pass').className).toContain('ant-tag-green')
    // valeur JSON structurée rendue telle quelle, sans lever
    expect(within(rows[3]).getByText('{"hour":22}')).toBeInTheDocument()
  })

  it('montre la décision finale et la trace brute repliée en JSON', () => {
    renderTrace()
    expect(screen.getByText('Sending window closed')).toBeInTheDocument()
    expect(screen.getByText('Raw trace (JSON)')).toBeInTheDocument()
  })

  it('trace réduite : le dit, et trace sans candidat : le dit aussi', () => {
    renderTrace(trace({ level: 'reduced', candidates: [] }))
    expect(screen.getByText('Reduced trace: only the gates that decided are recorded.')).toBeInTheDocument()
    expect(screen.getByText('No candidate profile was evaluated.')).toBeInTheDocument()
  })
})
