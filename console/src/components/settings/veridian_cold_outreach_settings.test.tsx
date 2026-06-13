import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { i18n } from '@lingui/core'
import { I18nProvider } from '@lingui/react'
import { App as AntApp } from 'antd'
import { VeridianColdOutreachSettings } from './veridian_cold_outreach_settings'
import type { Workspace } from '../../services/api/types'

i18n.loadAndActivate({ locale: 'en', messages: {} })

vi.mock('../../services/api/workspace', async () => {
  const actual = await vi.importActual<typeof import('../../services/api/workspace')>(
    '../../services/api/workspace'
  )
  return {
    ...actual,
    workspaceService: {
      update: vi.fn().mockResolvedValue({}),
      get: vi.fn()
    }
  }
})

import { workspaceService } from '../../services/api/workspace'

function makeWorkspace(overrides?: Partial<Workspace['settings']>): Workspace {
  return {
    id: 'ws-1',
    name: 'WS',
    settings: {
      timezone: 'UTC',
      email_tracking_enabled: true,
      default_language: 'en',
      languages: ['en'],
      ...overrides
    },
    created_at: '',
    updated_at: ''
  } as Workspace
}

function renderCmp(props: {
  workspace: Workspace | null
  isOwner: boolean
  onWorkspaceUpdate?: (w: Workspace) => void
}) {
  return render(
    <I18nProvider i18n={i18n}>
      <AntApp>
        <VeridianColdOutreachSettings
          workspace={props.workspace}
          isOwner={props.isOwner}
          onWorkspaceUpdate={props.onWorkspaceUpdate ?? (() => {})}
        />
      </AntApp>
    </I18nProvider>
  )
}

describe('VeridianColdOutreachSettings', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    vi.mocked(workspaceService.get).mockResolvedValue({ workspace: makeWorkspace() } as never)
  })

  it('shows the section header and all 5 provider classes', () => {
    renderCmp({ workspace: makeWorkspace(), isOwner: true })
    expect(screen.getByText('Veridian — Cold outreach')).toBeInTheDocument()
    expect(screen.getByText(/Google \(Gmail/)).toBeInTheDocument()
    expect(screen.getByText(/Microsoft \(Outlook/)).toBeInTheDocument()
    expect(screen.getByText(/Yahoo \/ AOL/)).toBeInTheDocument()
    expect(screen.getByText(/French ISPs/)).toBeInTheDocument()
    expect(screen.getByText(/Corporate/)).toBeInTheDocument()
  })

  it('tags Google and Microsoft as pixel OFF by default (owner view)', () => {
    renderCmp({ workspace: makeWorkspace(), isOwner: true })
    // 2 tags "pixel OFF by default" (google + microsoft)
    expect(screen.getAllByText(/pixel OFF by default/i)).toHaveLength(2)
  })

  it('renders read-only descriptions for non-owner', () => {
    renderCmp({
      workspace: makeWorkspace({
        veridian_provider_class_rates: { google: 1, microsoft: 2, yahoo_aol: 3, freemail_fr: 4, corporate: 30 }
      }),
      isOwner: false
    })
    // pas de bouton Save en lecture seule
    expect(screen.queryByRole('button', { name: /Save Changes/i })).not.toBeInTheDocument()
    expect(screen.getAllByText(/Open pixel/i).length).toBeGreaterThan(0)
  })

  it('saves only positive rates and omits empty ones, with pixel booleans', async () => {
    const user = userEvent.setup()
    const onUpdate = vi.fn()
    renderCmp({
      workspace: makeWorkspace({
        veridian_provider_class_rates: { google: 1 } as never
      }),
      isOwner: true,
      onWorkspaceUpdate: onUpdate
    })

    // Modifie un champ pour activer le bouton (touched), puis sauve.
    const saveBtn = screen.getByRole('button', { name: /Save Changes/i })
    expect(saveBtn).toBeDisabled()

    // Le rate google est pré-rempli à 1 ; on bascule un toggle pixel pour toucher le form.
    const switches = screen.getAllByRole('switch')
    await user.click(switches[0]) // toggle pixel google

    await waitFor(() => expect(saveBtn).toBeEnabled())
    await user.click(saveBtn)

    await waitFor(() => {
      expect(workspaceService.update).toHaveBeenCalledTimes(1)
    })
    const arg = vi.mocked(workspaceService.update).mock.calls[0][0]
    // rates : google=1 conservé (positif)
    expect(arg.settings?.veridian_provider_class_rates?.google).toBe(1)
    // pixel map présente avec des booléens pour les 5 classes
    expect(arg.settings?.veridian_open_pixel_by_class).toBeDefined()
    expect(typeof arg.settings?.veridian_open_pixel_by_class?.corporate).toBe('boolean')
  })
})
