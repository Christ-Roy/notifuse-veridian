import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, fireEvent } from '@testing-library/react'
import { i18n } from '@lingui/core'
import { I18nProvider } from '@lingui/react'
import { BranchConfigForm } from './BranchConfigForm'
import type { BranchNodeConfig } from '../../../services/api/automation'

i18n.loadAndActivate({ locale: 'en', messages: {} })

// Mock the automation context (workspace + lists) — not under test here.
vi.mock('../context', () => ({
  useAutomation: () => ({
    workspace: { id: 'ws1' },
    lists: []
  })
}))

// Mock the heavy segment TreeNodeInput — we only assert branch path management.
vi.mock('../../segment/input', () => ({
  TreeNodeInput: () => <div data-testid="tree-node-input">conditions</div>
}))
vi.mock('../../segment/table_schemas', () => ({
  TableSchemas: {}
}))

const renderForm = (config: BranchNodeConfig, onChange = vi.fn()) =>
  render(
    <I18nProvider i18n={i18n}>
      <BranchConfigForm config={config} onChange={onChange} />
    </I18nProvider>
  )

const seedConfig = (): BranchNodeConfig => ({
  paths: [
    { id: 'p1', name: 'VIP', conditions: undefined, next_node_id: '' },
    { id: 'def', name: 'Otherwise', conditions: undefined, next_node_id: '' }
  ],
  default_path_id: 'def'
})

describe('BranchConfigForm', () => {
  beforeEach(() => {
    vi.clearAllMocks()
  })

  it('renders existing paths with non-empty labels', () => {
    renderForm(seedConfig())
    // Path name inputs reflect config (real rendered values, not DOM counting)
    expect(screen.getByDisplayValue('VIP')).toBeInTheDocument()
    expect(screen.getByDisplayValue('Otherwise')).toBeInTheDocument()
    // Branch-logic explainer is rendered with real text
    expect(screen.getByText('Branch Logic')).toBeInTheDocument()
  })

  it('initializes default paths when config is empty', () => {
    const onChange = vi.fn()
    renderForm({ paths: [], default_path_id: '' }, onChange)
    expect(onChange).toHaveBeenCalledTimes(1)
    const next = onChange.mock.calls[0][0] as BranchNodeConfig
    expect(next.paths.length).toBe(2)
    expect(next.default_path_id).toBe(next.paths[1].id)
  })

  it('adds a new path before the default catch-all', () => {
    const onChange = vi.fn()
    renderForm(seedConfig(), onChange)
    fireEvent.click(screen.getByText('Add Path'))
    expect(onChange).toHaveBeenCalledTimes(1)
    const next = onChange.mock.calls[0][0] as BranchNodeConfig
    expect(next.paths.length).toBe(3)
    // default stays last
    expect(next.paths[next.paths.length - 1].id).toBe('def')
    expect(next.default_path_id).toBe('def')
  })

  it('renaming a path propagates via onChange', () => {
    const onChange = vi.fn()
    renderForm(seedConfig(), onChange)
    fireEvent.change(screen.getByDisplayValue('VIP'), { target: { value: 'Premium' } })
    const next = onChange.mock.calls[0][0] as BranchNodeConfig
    expect(next.paths[0].name).toBe('Premium')
  })

  it('removing the default path promotes a new default', () => {
    const onChange = vi.fn()
    const cfg = seedConfig()
    renderForm(cfg, onChange)
    // The default path "Otherwise" is the 2nd; its delete button is the 2nd danger button.
    const deleteButtons = screen.getAllByRole('button').filter((b) =>
      b.className.includes('ant-btn-dangerous')
    )
    fireEvent.click(deleteButtons[1])
    const next = onChange.mock.calls[0][0] as BranchNodeConfig
    expect(next.paths.length).toBe(1)
    expect(next.default_path_id).toBe(next.paths[0].id)
  })
})
