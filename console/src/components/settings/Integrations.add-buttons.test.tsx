import { describe, expect, it, vi, beforeEach } from 'vitest'
import { render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { i18n } from '@lingui/core'
import { I18nProvider } from '@lingui/react'
import { Integrations } from './Integrations'
import type { Workspace } from '../../services/api/types'

vi.mock('@tanstack/react-router', () => ({
  Link: ({ children }: { children: React.ReactNode }) => <a>{children}</a>
}))

const createIntegration = vi.fn()
const getWorkspace = vi.fn()
vi.mock('../../services/api/workspace', () => ({
  workspaceService: {
    createIntegration: (...a: unknown[]) => createIntegration(...a),
    updateIntegration: vi.fn(),
    deleteIntegration: vi.fn(),
    get: (...a: unknown[]) => getWorkspace(...a)
  }
}))
vi.mock('../../services/api/list', () => ({
  listsApi: { list: vi.fn().mockResolvedValue({ lists: [] }) }
}))

i18n.loadAndActivate({ locale: 'en', messages: {} })

const workspace = {
  id: 'ws1',
  name: 'WS',
  settings: { timezone: 'UTC' },
  integrations: []
} as unknown as Workspace

function renderIntegrations(isOwner: boolean) {
  return render(
    <I18nProvider i18n={i18n}>
      <Integrations workspace={workspace} onSave={vi.fn()} loading={false} isOwner={isOwner} />
    </I18nProvider>
  )
}

describe('Integrations: an owner can add LLM / Firecrawl / Supabase from the UI', () => {
  beforeEach(() => {
    createIntegration.mockReset().mockResolvedValue({ integration_id: 'i1' })
    getWorkspace.mockReset().mockResolvedValue({ workspace })
  })

  it('shows the four add buttons to the owner', () => {
    renderIntegrations(true)
    for (const name of ['Add Anthropic (Claude)', 'Add OpenAI', 'Add Firecrawl', 'Add Supabase']) {
      expect(screen.getByRole('button', { name })).toBeInTheDocument()
    }
  })

  it('shows no add button to a non-owner', () => {
    renderIntegrations(false)
    expect(screen.queryByTestId('add-integration-buttons')).not.toBeInTheDocument()
    expect(screen.queryByRole('button', { name: 'Add Firecrawl' })).not.toBeInTheDocument()
  })

  it('creates a Firecrawl integration with its API key (write-only field)', async () => {
    const user = userEvent.setup()
    renderIntegrations(true)
    await user.click(screen.getByRole('button', { name: 'Add Firecrawl' }))

    const key = await screen.findByPlaceholderText('fc-...')
    expect(key).toHaveAttribute('type', 'password')
    const nameInput = screen.getByPlaceholderText('e.g., My Firecrawl Integration')
    await user.clear(nameInput)
    await user.type(nameInput, 'Mon Firecrawl')
    await user.type(key, 'fc-secret-123')
    await user.click(screen.getByRole('button', { name: 'Save' }))

    await waitFor(() => expect(createIntegration).toHaveBeenCalledTimes(1))
    expect(createIntegration.mock.calls[0][0]).toMatchObject({
      workspace_id: 'ws1',
      name: 'Mon Firecrawl',
      type: 'firecrawl',
      firecrawl_settings: { api_key: 'fc-secret-123' }
    })
  })
})
