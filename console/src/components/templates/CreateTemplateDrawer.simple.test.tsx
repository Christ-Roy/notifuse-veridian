import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, fireEvent, waitFor } from '@testing-library/react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { App } from 'antd'

// Les editeurs lourds ne servent pas a ce test (formulaire de l'etape 1 seulement).
vi.mock('../email_builder/EmailBuilder', () => ({ default: () => null }))
vi.mock('../email_builder/MjmlCodeEditor', () => ({
  default: () => null,
  STARTER_TEMPLATE: '<mjml></mjml>'
}))
vi.mock('../email_builder/EmailAIAssistant', () => ({ EmailAIAssistant: () => null }))
vi.mock('../../contexts/AuthContext', () => ({ useAuth: () => ({ refreshWorkspaces: vi.fn() }) }))
vi.mock('../../services/api/template', async (orig) => ({
  ...(await orig<typeof import('../../services/api/template')>()),
  templatesApi: {
    list: vi.fn().mockResolvedValue({ templates: [] }),
    get: vi.fn().mockRejectedValue(new Error('404')),
    create: vi.fn().mockResolvedValue({}),
    update: vi.fn(),
    delete: vi.fn(),
    compile: vi.fn()
  }
}))

import { CreateTemplateDrawer } from './CreateTemplateDrawer'
import { templatesApi } from '../../services/api/template'

const workspace = {
  id: 'ws1',
  name: 'WS',
  settings: { languages: [], default_language: 'en' },
  integrations: []
} as never

const renderDrawer = () =>
  render(
    <QueryClientProvider client={new QueryClient()}>
      <App>
        <CreateTemplateDrawer workspace={workspace} />
      </App>
    </QueryClientProvider>
  )

// Audit UI 19/10/2026 (C1, E3) : creer un modele demandait 5 champs avant d'ecrire une ligne
// et testait l'unicite de l'identifiant par un GET 404 a chaque pause de frappe.
describe('CreateTemplateDrawer : creation simple', () => {
  beforeEach(() => { vi.mocked(templatesApi.list).mockClear(); vi.mocked(templatesApi.get).mockClear() })
  it("propose le mode Texte en premier et par defaut", async () => {
    renderDrawer()
    fireEvent.click(screen.getByText('Create Template'))
    const radios = await screen.findAllByRole('radio')
    expect(radios.map((r) => (r as HTMLInputElement).value)).toEqual(['text', 'visual', 'code'])
    expect((radios[0] as HTMLInputElement).checked).toBe(true)
  })

  it("n'exige que le nom et l'objet : identifiant, categorie et apercu sont sous « Advanced options »", async () => {
    renderDrawer()
    fireEvent.click(screen.getByText('Create Template'))
    await screen.findByText('Advanced options')
    // Un seul geste pour valider : on laisse tout vide et on ne doit voir d'erreur QUE sur nom et objet.
    fireEvent.click(screen.getByText('Next')) // 'Suivant' valide l'etape 1 (B4)
    await waitFor(() => {
      const errors = Array.from(document.querySelectorAll('.ant-form-item-explain-error')).map(
        (e) => e.textContent
      )
      expect(errors.length).toBe(2)
    })
  })

  it("ne fait aucun GET templates.get par frappe (identifiant calcule depuis la liste, un seul appel)", async () => {
    renderDrawer()
    fireEvent.click(screen.getByText('Create Template'))
    const name = await screen.findByPlaceholderText('i.e: Welcome Email')
    fireEvent.change(name, { target: { value: 'Premier contact' } })
    fireEvent.change(name, { target: { value: 'Premier contact 2' } })
    expect(templatesApi.get).not.toHaveBeenCalled()
    expect(templatesApi.list).toHaveBeenCalledTimes(1)
    const id = (await screen.findByPlaceholderText('i.e: welcome-email')) as HTMLInputElement
    expect(id.value).toBe('premier-contact-2')
  })
})
