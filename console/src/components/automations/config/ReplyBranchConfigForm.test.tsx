import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen } from '@testing-library/react'
import { i18n } from '@lingui/core'
import { I18nProvider } from '@lingui/react'
import { ReplyBranchConfigForm } from './ReplyBranchConfigForm'
import type { ReplyBranchNodeConfig } from '../../../services/api/automation'

i18n.loadAndActivate({ locale: 'en', messages: {} })

const renderForm = (config: Partial<ReplyBranchNodeConfig>, onChange = vi.fn()) =>
  render(
    <I18nProvider i18n={i18n}>
      <ReplyBranchConfigForm config={config as ReplyBranchNodeConfig} onChange={onChange} />
    </I18nProvider>
  )

describe('ReplyBranchConfigForm', () => {
  beforeEach(() => {
    vi.clearAllMocks()
  })

  it('renders the routing explainer with non-empty labels', () => {
    renderForm({ replied_node_id: 'n1', not_replied_node_id: 'n2' })
    expect(screen.getByText('Route on reply')).toBeInTheDocument()
    expect(screen.getByText('Replied:')).toBeInTheDocument()
    expect(screen.getByText('No reply:')).toBeInTheDocument()
  })

  it('initializes config shape when targets are undefined', () => {
    const onChange = vi.fn()
    renderForm({}, onChange)
    expect(onChange).toHaveBeenCalledTimes(1)
    const next = onChange.mock.calls[0][0] as ReplyBranchNodeConfig
    expect(next).toEqual({ replied_node_id: '', not_replied_node_id: '' })
  })

  it('does not re-initialize when both targets are already defined', () => {
    const onChange = vi.fn()
    renderForm({ replied_node_id: '', not_replied_node_id: '' }, onChange)
    expect(onChange).not.toHaveBeenCalled()
  })
})
