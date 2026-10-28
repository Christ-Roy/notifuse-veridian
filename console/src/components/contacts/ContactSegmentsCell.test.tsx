import { describe, expect, it, vi } from 'vitest'
import { render, screen, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { i18n } from '@lingui/core'
import { I18nProvider } from '@lingui/react'

import { ContactSegmentsCell } from './ContactSegmentsCell'

i18n.loadAndActivate({ locale: 'en', messages: {} })
vi.setConfig({ testTimeout: 30000 })

const NAMES: Record<string, string> = {
  p1: '1 Priorité / P1 · à contacter en premier',
  p2: '1 Priorité / P2 · plus tard',
  c1: '2 Cibles / Animaleries / P1',
  c2: '2 Cibles / Animaleries / P2',
  c3: '2 Cibles / Jardineries / P1',
  c4: '2 Cibles / Jardineries / P3'
}
const segments = Object.entries(NAMES).map(([id, name]) => ({ id, name, color: 'blue' }))
const contactSegments = ['c2', 'c1', 'p2', 'p1', 'c3', 'c4'].map((segment_id) => ({
  segment_id,
  matched_at: '2026-05-01T08:00:00Z'
}))

const renderCell = (
  cs: Array<{ segment_id: string; matched_at?: string }> = contactSegments,
  list = segments
) =>
  render(
    <I18nProvider i18n={i18n}>
      <ContactSegmentsCell contactSegments={cs} segments={list} timezone="Europe/Paris" />
    </I18nProvider>
  )

describe('ContactSegmentsCell', () => {
  it('affiche 2 pastilles au nom court et un bouton +4', () => {
    const { container } = renderCell()
    const tags = container.querySelectorAll('.ant-tag')
    expect(tags).toHaveLength(2)
    expect(tags[0]).toHaveTextContent('P1 · à contacter en premier')
    expect(tags[1]).toHaveTextContent('P1')
    expect(tags[1]).not.toHaveTextContent('Animaleries')
    const more = screen.getByRole('button', { name: 'Show 4 more segments' })
    expect(more).toHaveTextContent('+4')
  })

  it('le bouton +4 ouvre la liste complète au clavier, chemins complets en texte', async () => {
    const user = userEvent.setup({ delay: null })
    renderCell()
    const more = screen.getByRole('button', { name: 'Show 4 more segments' })
    expect(more).toHaveAttribute('aria-expanded', 'false')
    more.focus()
    await user.keyboard('{Enter}')
    expect(more).toHaveAttribute('aria-expanded', 'true')
    const popup = await screen.findByRole('tooltip')
    for (const id of ['p2', 'c2', 'c3', 'c4']) {
      expect(within(popup).getByText(NAMES[id])).toBeInTheDocument()
    }
    expect(within(popup).queryByText(NAMES.p1)).not.toBeInTheDocument()
  })

  it('un seul segment : une pastille et aucun bouton +N', () => {
    const { container } = renderCell([contactSegments[0]])
    expect(container.querySelectorAll('.ant-tag')).toHaveLength(1)
    expect(screen.queryByRole('button')).not.toBeInTheDocument()
  })

  it('segment introuvable dans la liste : on affiche l identifiant', () => {
    const { container } = renderCell([{ segment_id: 'seg_inconnu' }], [])
    expect(container.querySelector('.ant-tag')).toHaveTextContent('seg_inconnu')
  })

  it('aucun segment : cellule vide, aucun bouton', () => {
    const { container } = renderCell([])
    expect(container.querySelectorAll('.ant-tag')).toHaveLength(0)
    expect(screen.queryByRole('button')).not.toBeInTheDocument()
  })

  it('un nom contenant du HTML reste du texte', async () => {
    const evil = { id: 'x', name: '<img src=x onerror=alert(1)> / <b>gras</b>', color: 'blue' }
    const { container } = renderCell([{ segment_id: 'x' }], [evil])
    expect(container.querySelector('img')).toBeNull()
    expect(container.querySelector('b')).toBeNull()
    expect(container.querySelector('.ant-tag')).toHaveTextContent('<b>gras</b>')
  })
})
