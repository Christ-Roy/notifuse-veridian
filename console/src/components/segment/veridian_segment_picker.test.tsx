import { describe, expect, it, vi } from 'vitest'
import { render as rtlRender, screen, fireEvent } from '@testing-library/react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import type { ReactElement } from 'react'

import { VeridianSegmentPicker, type PickableSegment } from './veridian_segment_picker'

vi.setConfig({ testTimeout: 30000 })

// Le sélecteur lit les dossiers natifs (react-query) : un client par rendu, sans workspace il reste sur les noms.
const render = (ui: ReactElement) =>
  rtlRender(<QueryClientProvider client={new QueryClient({ defaultOptions: { queries: { retry: false } } })}>{ui}</QueryClientProvider>)

const seg = (id: string, name: string): PickableSegment => ({ id, name, color: 'blue', users_count: 1 })

const FLAT: PickableSegment[] = [seg('a', 'Zèbre'), seg('b', 'Alpha')]
const NESTED: PickableSegment[] = [
  seg('p1', '2 Cibles / Animaleries / P1'),
  seg('g', '2 Cibles / Grossistes'),
  seg('t', 'Contacts de test')
]

const openDropdown = () => {
  const combobox = screen.getByRole('combobox')
  fireEvent.mouseDown(combobox)
  return combobox
}

describe('sélecteur de segments: liste plate (tenant sans dossier)', () => {
  it('affiche les segments comme options d un Select, sans aucun dossier', () => {
    render(
      <VeridianSegmentPicker
        multiple
        segments={FLAT}
        formatNumber={String}
        placeholder="Choisir"
      />
    )
    openDropdown()
    expect(screen.getByText('Zèbre')).toBeInTheDocument()
    expect(screen.getByText('Alpha')).toBeInTheDocument()
    expect(document.querySelector('.ant-select-tree')).toBeNull()
  })
})

describe('sélecteur de segments: avec dossiers', () => {
  it('affiche les dossiers avec leur effectif dans un TreeSelect', () => {
    render(
      <VeridianSegmentPicker
        multiple
        segments={NESTED}
        formatNumber={String}
        placeholder="Choisir"
      />
    )
    openDropdown()
    expect(screen.getByText('2 Cibles (2)')).toBeInTheDocument()
    expect(screen.getByText('Contacts de test')).toBeInTheDocument()
    expect(document.querySelector('.ant-select-tree')).not.toBeNull()
  })

  it('en mode simple (un seul choix), le dossier n est pas sélectionnable', () => {
    const onChange = vi.fn()
    render(
      <VeridianSegmentPicker
        segments={NESTED}
        value={undefined}
        onChange={onChange}
        formatNumber={String}
        placeholder="Choisir"
      />
    )
    openDropdown()
    const folder = screen.getByText('2 Cibles (2)')
    fireEvent.click(folder)
    expect(onChange).not.toHaveBeenCalled()
    expect(document.querySelector('.ant-select-tree')).not.toBeNull()
  })
})
