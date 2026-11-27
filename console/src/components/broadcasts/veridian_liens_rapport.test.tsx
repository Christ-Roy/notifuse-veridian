import { describe, expect, it, vi, beforeEach } from 'vitest'
import { render as rtlRender, screen, waitFor } from '@testing-library/react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import type { ReactElement } from 'react'

const rapport = vi.fn()
vi.mock('../../services/api/veridian_liens', () => ({
  veridianLiensApi: { rapport: (ws: string, bc: string) => rapport(ws, bc) }
}))

import { VeridianLiensRapport } from './veridian_liens_rapport'

// Reprise amont 35.0 : le détail des clics par lien s'affiche depuis l'API (clics totaux et cliqueurs uniques).
const render = (ui: ReactElement) =>
  rtlRender(
    <QueryClientProvider client={new QueryClient({ defaultOptions: { queries: { retry: false } } })}>{ui}</QueryClientProvider>
  )

describe('VeridianLiensRapport (clics par lien)', () => {
  beforeEach(() => {
    rapport.mockReset()
  })

  it('affiche une ligne par URL avec ses clics totaux et ses cliqueurs uniques', async () => {
    rapport.mockResolvedValue({
      lignes: [
        { url: 'https://a.test/offre?utm_content=haut', clics: 4, cliqueurs: 2 },
        { url: 'https://a.test/offre?utm_content=bas', clics: 1, cliqueurs: 1 }
      ]
    })
    render(<VeridianLiensRapport workspaceId="ws1" broadcastId="bc1" />)

    await waitFor(() => expect(screen.getByText('https://a.test/offre?utm_content=haut')).toBeInTheDocument())
    expect(rapport).toHaveBeenCalledWith('ws1', 'bc1')
    const ligne = screen.getByText('https://a.test/offre?utm_content=haut').closest('tr') as HTMLElement
    expect(ligne).toHaveTextContent('4')
    expect(ligne).toHaveTextContent('2')
  })

  it('affiche l état vide quand aucun lien n a été cliqué', async () => {
    rapport.mockResolvedValue({ lignes: [] })
    render(<VeridianLiensRapport workspaceId="ws1" broadcastId="bc1" />)

    await waitFor(() => expect(screen.getByText('No link clicked yet')).toBeInTheDocument())
  })
})
