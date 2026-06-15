import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { i18n } from '@lingui/core'
import { I18nProvider } from '@lingui/react'
import { App as AntApp } from 'antd'
import { DeliverabilityPanel } from './TemplatePreviewDrawer'

i18n.loadAndActivate({ locale: 'en', messages: {} })

vi.mock('../../services/api/deliverability', () => ({
  deliverabilityApi: {
    score: vi.fn()
  }
}))

import { deliverabilityApi } from '../../services/api/deliverability'

function renderPanel(props?: Partial<React.ComponentProps<typeof DeliverabilityPanel>>) {
  return render(
    <I18nProvider i18n={i18n}>
      <AntApp>
        <DeliverabilityPanel
          workspaceId={props?.workspaceId ?? 'ws-1'}
          html={props?.html ?? '<p>Bonjour, je vous écris au sujet de votre projet web chez Veridian.</p>'}
          subject={props?.subject ?? 'Une question rapide'}
          fromDomain={props?.fromDomain ?? 'agences-veridian.fr'}
        />
      </AntApp>
    </I18nProvider>
  )
}

describe('DeliverabilityPanel', () => {
  beforeEach(() => {
    vi.clearAllMocks()
  })

  it('scores the rendered HTML on mount and shows the score + risk verdict', async () => {
    vi.mocked(deliverabilityApi.score).mockResolvedValue({
      score: 1.5,
      is_risky: false,
      mode: 'default',
      rules: [{ name: 'MIME_HTML_ONLY_RISK', weight: 1.5, message: 'Send multipart text+HTML.' }],
      summary: 'Good deliverability profile.'
    })

    renderPanel()

    await waitFor(() => expect(deliverabilityApi.score).toHaveBeenCalledTimes(1))
    // Le HTML rendu est envoyé en is_html=true avec le sujet.
    const arg = vi.mocked(deliverabilityApi.score).mock.calls[0][0]
    expect(arg.workspace_id).toBe('ws-1')
    expect(arg.is_html).toBe(true)
    expect(arg.body).toContain('Veridian')
    expect(arg.subject).toBe('Une question rapide')
    expect(arg.from_domain).toBe('agences-veridian.fr')

    // Score affiché + verdict "Looks good"
    await waitFor(() => expect(screen.getByText('1.5')).toBeInTheDocument())
    expect(screen.getByText('Looks good')).toBeInTheDocument()
    // Règle déclenchée affichée avec son message d'aide
    expect(screen.getByText('MIME_HTML_ONLY_RISK')).toBeInTheDocument()
    expect(screen.getByText('Send multipart text+HTML.')).toBeInTheDocument()
  })

  it('flags a risky template with the High spam risk tag', async () => {
    vi.mocked(deliverabilityApi.score).mockResolvedValue({
      score: 7.2,
      is_risky: true,
      mode: 'strict',
      rules: [
        { name: 'HTML_IMAGE_ONLY', weight: 3, message: 'Add real text.' },
        { name: 'LINK_IN_STRICT_MODE', weight: 1.2, message: 'Remove links for Google.' }
      ],
      summary: 'High spam risk.'
    })

    renderPanel()

    await waitFor(() => expect(screen.getByText('7.2')).toBeInTheDocument())
    expect(screen.getByText('High spam risk')).toBeInTheDocument()
    expect(screen.getByText('HTML_IMAGE_ONLY')).toBeInTheDocument()
  })

  it('re-scores with the selected provider class (strict mode for Google)', async () => {
    vi.mocked(deliverabilityApi.score).mockResolvedValue({
      score: 0,
      is_risky: false,
      mode: 'default',
      rules: [],
      summary: 'Clean.'
    })

    const user = userEvent.setup()
    renderPanel()

    await waitFor(() => expect(deliverabilityApi.score).toHaveBeenCalledTimes(1))
    // Premier appel : pas de classe (mode default explicite).
    expect(vi.mocked(deliverabilityApi.score).mock.calls[0][0].provider_class).toBeUndefined()
    expect(vi.mocked(deliverabilityApi.score).mock.calls[0][0].mode).toBe('default')

    // Sélectionne la classe Google → re-score en strict (provider_class envoyé).
    // Le Select Ant expose un combobox (role) ; on l'ouvre et on clique l'option.
    const select = screen.getByRole('combobox')
    await user.click(select)
    const option = await screen.findByText('Google (Gmail / Workspace) — strict')
    await user.click(option)

    await waitFor(() => expect(deliverabilityApi.score).toHaveBeenCalledTimes(2))
    const secondArg = vi.mocked(deliverabilityApi.score).mock.calls[1][0]
    expect(secondArg.provider_class).toBe('google')
    // Avec une classe, on ne force PAS le mode (déduit côté backend).
    expect(secondArg.mode).toBeUndefined()
  })

  it('shows an error when scoring fails', async () => {
    vi.mocked(deliverabilityApi.score).mockRejectedValue(new Error('boom'))
    renderPanel()
    await waitFor(() => expect(screen.getByText('Could not score the template')).toBeInTheDocument())
  })

  it('shows a clean banner when no rule is triggered', async () => {
    vi.mocked(deliverabilityApi.score).mockResolvedValue({
      score: 0,
      is_risky: false,
      mode: 'lenient',
      rules: [],
      summary: 'Clean.'
    })
    renderPanel()
    await waitFor(() =>
      expect(screen.getByText('No spam rule triggered. Clean template.')).toBeInTheDocument()
    )
  })
})
