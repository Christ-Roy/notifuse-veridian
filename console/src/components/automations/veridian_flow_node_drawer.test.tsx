import { describe, it, expect, vi } from 'vitest'
import { render, screen, waitFor } from '@testing-library/react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { i18n } from '@lingui/core'
import { I18nProvider } from '@lingui/react'

import { VeridianFlowNodeDrawer } from './veridian_flow_node_drawer'
import { templatesApi } from '../../services/api/template'

i18n.loadAndActivate({ locale: 'en', messages: {} })

vi.mock('../templates/CreateTemplateDrawer', () => ({
  CreateTemplateDrawer: (p: { buttonContent?: React.ReactNode; buttonProps?: { disabled?: boolean } }) => (
    <button type="button" disabled={p.buttonProps?.disabled}>
      {p.buttonContent}
    </button>
  )
}))
vi.mock('../../services/api/template', () => ({ templatesApi: { get: vi.fn() } }))

const workspace = { id: 'ws1', name: 'animalsd', settings: { timezone: 'Europe/Paris' } } as never

const template = {
  id: 'tplB',
  name: 'Relance 1',
  version: 3,
  updated_at: '2026-10-20T10:00:00Z',
  email: {
    subject: 'Votre site en une semaine',
    compiled_preview: '<p>Bonjour</p><script>alert(1)</script>',
    text: 'Bonjour, texte brut',
    visual_editor_tree: {}
  }
}

const renderDrawer = (props: Partial<React.ComponentProps<typeof VeridianFlowNodeDrawer>> = {}) => {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  return render(
    <QueryClientProvider client={client}>
      <I18nProvider i18n={i18n}>
        <VeridianFlowNodeDrawer
          open
          onClose={() => {}}
          kind="email"
          workspace={workspace}
          canEdit
          templateId="tplB"
          stageLabel="J+5"
          stage={{ sent: 74, replies_human: 6 } as never}
          nodeStats={{ node_id: 'j5', node_type: 'email', entered: 80, completed: 74, queued: 0, failed: 2, skipped: 0 }}
          {...props}
        />
      </I18nProvider>
    </QueryClientProvider>
  )
}

describe('tiroir d un nœud du flux', () => {
  it('nœud email : nom, objet, version, compteurs et texte brut du modèle', async () => {
    vi.mocked(templatesApi.get).mockResolvedValue({ template } as never)
    renderDrawer()
    await waitFor(() => expect(screen.getByText('Votre site en une semaine')).toBeInTheDocument())
    expect(screen.getByText('Relance 1')).toBeInTheDocument()
    expect(screen.getByText('3')).toBeInTheDocument()
    expect(screen.getByText('Bonjour, texte brut')).toBeInTheDocument()
    expect(screen.getByText('74')).toBeInTheDocument()
  })

  it('aperçu dans un iframe sans script (sandbox vide)', async () => {
    vi.mocked(templatesApi.get).mockResolvedValue({ template } as never)
    renderDrawer()
    const frame = await waitFor(() => {
      const el = document.querySelector('iframe')
      expect(el).not.toBeNull()
      return el as HTMLIFrameElement
    })
    expect(frame.getAttribute('sandbox')).toBe('')
    expect(frame.getAttribute('srcdoc')).toContain('<p>Bonjour</p>')
  })

  it('bouton Modifier désactivé sans droit d écriture sur les modèles', async () => {
    vi.mocked(templatesApi.get).mockResolvedValue({ template } as never)
    renderDrawer({ canEdit: false })
    await waitFor(() => expect(screen.getByText('Votre site en une semaine')).toBeInTheDocument())
    expect(screen.getByRole('button', { name: 'Edit template' })).toBeDisabled()
  })

  it('nœud délai : durée et étape d arrivée', () => {
    renderDrawer({ kind: 'delay', templateId: undefined, delay: { duration: 5, unit: 'days' }, stageLabel: 'J+5' })
    expect(screen.getByText('5 days')).toBeInTheDocument()
    expect(screen.getByText('J+5')).toBeInTheDocument()
  })
})
