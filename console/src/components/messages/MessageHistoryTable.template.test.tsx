import { describe, expect, it, vi } from 'vitest'
import { render, screen } from '@testing-library/react'
import { i18n } from '@lingui/core'
import { I18nProvider } from '@lingui/react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'

i18n.loadAndActivate({ locale: 'en', messages: {} })

vi.mock('../../services/api/template', () => ({
  templatesApi: {
    get: vi.fn().mockResolvedValue({ template: { id: 'tpl-j0', name: 'Bienvenue J0', version: 2 } })
  }
}))

import { TemplateNameCell } from './MessageHistoryTable'

const rendu = () =>
  render(
    <QueryClientProvider client={new QueryClient({ defaultOptions: { queries: { retry: false } } })}>
      <I18nProvider i18n={i18n}>
        <TemplateNameCell workspaceId="ws-1" templateId="tpl-j0" version={2} />
      </I18nProvider>
    </QueryClientProvider>
  )

describe('journal : modèle affiché par son nom', () => {
  it('affiche le nom du modèle, pas son identifiant', async () => {
    rendu()
    expect(await screen.findByText('Bienvenue J0')).toBeInTheDocument()
    expect(screen.queryByText('tpl-j0')).not.toBeInTheDocument()
  })
})
