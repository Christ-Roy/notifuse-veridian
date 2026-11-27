import { beforeEach, describe, expect, it, vi } from 'vitest'
import { render, screen, waitFor } from '@testing-library/react'
import { i18n } from '@lingui/core'
import { I18nProvider } from '@lingui/react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'

i18n.loadAndActivate({ locale: 'en', messages: {} })

vi.mock('../../services/api/template', () => ({
  templatesApi: {
    get: vi.fn()
  }
}))

import { TemplateNameCell } from './MessageHistoryTable'
import { templatesApi } from '../../services/api/template'
import { ApiError } from '../../services/api/client'

const rendu = (templateId: string, client = new QueryClient({ defaultOptions: { queries: { retry: false } } })) =>
  render(
    <QueryClientProvider client={client}>
      <I18nProvider i18n={i18n}>
        <TemplateNameCell workspaceId="ws-1" templateId={templateId} version={2} />
      </I18nProvider>
    </QueryClientProvider>
  )

describe('journal : modèle affiché par son nom', () => {
  beforeEach(() => {
    vi.mocked(templatesApi.get).mockReset()
  })

  it('affiche le nom du modèle, pas son identifiant', async () => {
    vi.mocked(templatesApi.get).mockResolvedValue({
      template: { id: 'tpl-j0', name: 'Bienvenue J0', version: 2 }
    } as never)
    rendu('tpl-j0')
    expect(await screen.findByText('Bienvenue J0')).toBeInTheDocument()
    expect(screen.queryByText('tpl-j0')).not.toBeInTheDocument()
  })

  it('un modèle absent de l’espace (404) s’affiche en clair, sans identifiant brut ni nouvelle tentative', async () => {
    vi.mocked(templatesApi.get).mockRejectedValue(new ApiError('Template not found', 404))
    rendu('mail-api')
    expect(await screen.findByText('Template not found')).toBeInTheDocument()
    expect(screen.queryByText('mail-api')).not.toBeInTheDocument()
    expect(templatesApi.get).toHaveBeenCalledTimes(1)
  })

  it('plusieurs lignes au même couple (modèle, version) : une seule requête', async () => {
    vi.mocked(templatesApi.get).mockResolvedValue({
      template: { id: 'tpl-j0', name: 'Bienvenue J0', version: 2 }
    } as never)
    const client = new QueryClient({ defaultOptions: { queries: { retry: false } } })
    render(
      <QueryClientProvider client={client}>
        <I18nProvider i18n={i18n}>
          <TemplateNameCell workspaceId="ws-1" templateId="tpl-j0" version={2} />
          <TemplateNameCell workspaceId="ws-1" templateId="tpl-j0" version={2} />
          <TemplateNameCell workspaceId="ws-1" templateId="tpl-j0" version={2} />
        </I18nProvider>
      </QueryClientProvider>
    )
    await waitFor(() => expect(screen.getAllByText('Bienvenue J0')).toHaveLength(3))
    expect(templatesApi.get).toHaveBeenCalledTimes(1)
  })
})
