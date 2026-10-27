import { describe, expect, it, vi } from 'vitest'
import { render, screen, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { i18n } from '@lingui/core'
import { I18nProvider } from '@lingui/react'
import { App as AntApp } from 'antd'

import { SegmentsFilter } from './SegmentsFilter'
import type { Segment } from '../../services/api/segment'

i18n.loadAndActivate({ locale: 'en', messages: {} })

// Machine de CI chargée: les rendus antd peuvent dépasser 5 s sans que rien ne soit cassé.
vi.setConfig({ testTimeout: 30000 })

vi.mock('../segment/button_upsert', () => ({
  default: () => <button type="button">New segment</button>
}))

vi.mock('../../services/api/task', () => ({
  taskApi: { findBySegmentId: vi.fn() }
}))

const seg = (id: string, name: string): Segment =>
  ({ id, name, color: 'blue', status: 'active', users_count: 1 }) as unknown as Segment

const renderFilter = (segments: Segment[], selectedSegmentIds: string[] = []) => {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  return render(
    <QueryClientProvider client={client}>
      <I18nProvider i18n={i18n}>
        <AntApp>
          <SegmentsFilter
            workspaceId="ws"
            segments={segments}
            selectedSegmentIds={selectedSegmentIds}
            onSegmentToggle={vi.fn()}
          />
        </AntApp>
      </I18nProvider>
    </QueryClientProvider>
  )
}

const FLAT = [seg('a', 'Zèbre'), seg('b', 'Alpha'), seg('c', 'Contacts de test (zz-test)')]

const NESTED = [
  seg('p1', '1 Priorité / P1 · à contacter en premier'),
  seg('ani', '2 Cibles / Animaleries indépendantes / P1'),
  seg('gro', '2 Cibles / Grossistes'),
  seg('test', 'Contacts de test (zz-test)')
]

describe('filtre des segments: liste plate (tenant sans dossier)', () => {
  it('affiche les segments dans l ordre reçu, sans aucun dossier', () => {
    const { container } = renderFilter(FLAT)
    const text = container.textContent ?? ''
    expect(text.indexOf('Zèbre')).toBeGreaterThanOrEqual(0)
    expect(text.indexOf('Zèbre')).toBeLessThan(text.indexOf('Alpha'))
    expect(text.indexOf('Alpha')).toBeLessThan(text.indexOf('Contacts de test'))
    expect(document.querySelectorAll('[aria-expanded]')).toHaveLength(0)
    expect(screen.queryByPlaceholderText('Search')).not.toBeInTheDocument()
  })
})

describe('filtre des segments: dossiers', () => {
  it('replie les dossiers par défaut et affiche leur effectif', () => {
    renderFilter(NESTED)
    const cibles = screen.getByRole('button', { name: /2 Cibles/ })
    expect(cibles).toHaveAttribute('aria-expanded', 'false')
    expect(within(cibles).getByText('2')).toBeInTheDocument()
    expect(screen.queryByText('Grossistes')).not.toBeInTheDocument()
  })

  it('déplie un dossier au clic, puis le replie', async () => {
    const user = userEvent.setup()
    renderFilter(NESTED)
    const cibles = screen.getByRole('button', { name: /2 Cibles/ })
    await user.click(cibles)
    expect(cibles).toHaveAttribute('aria-expanded', 'true')
    expect(screen.getByText('Grossistes')).toBeInTheDocument()
    expect(screen.getByRole('button', { name: /Animaleries indépendantes/ })).toBeInTheDocument()
    await user.click(cibles)
    expect(screen.queryByText('Grossistes')).not.toBeInTheDocument()
  })

  it('ouvre les dossiers qui contiennent un segment sélectionné', () => {
    renderFilter(NESTED, ['gro'])
    expect(screen.getByRole('button', { name: /2 Cibles/ })).toHaveAttribute('aria-expanded', 'true')
    expect(screen.getByText('Grossistes')).toBeInTheDocument()
  })

  it('la recherche déplie les dossiers correspondants et filtre les segments', async () => {
    const user = userEvent.setup()
    renderFilter(NESTED)
    await user.type(screen.getByPlaceholderText('Search'), 'grossistes')
    expect(screen.getByRole('button', { name: /2 Cibles/ })).toHaveAttribute('aria-expanded', 'true')
    expect(screen.getByText('Grossistes')).toBeInTheDocument()
    expect(screen.queryByRole('button', { name: /1 Priorité/ })).not.toBeInTheDocument()
  })
})

describe('filtre des segments: accessibilité des dossiers', () => {
  it('le bouton du dossier pointe vers le conteneur de ses enfants quand il est ouvert', async () => {
    const user = userEvent.setup()
    renderFilter(NESTED)
    const cibles = screen.getByRole('button', { name: /2 Cibles/ })
    expect(cibles).not.toHaveAttribute('aria-controls')
    await user.click(cibles)
    const panelId = cibles.getAttribute('aria-controls')
    expect(panelId).toBeTruthy()
    expect(document.getElementById(panelId as string)).toHaveTextContent('Grossistes')
  })
})
