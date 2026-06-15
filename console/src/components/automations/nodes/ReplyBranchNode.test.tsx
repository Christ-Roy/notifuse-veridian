import { describe, it, expect } from 'vitest'
import { render, screen } from '@testing-library/react'
import { i18n } from '@lingui/core'
import { I18nProvider } from '@lingui/react'
import type { NodeProps } from '@xyflow/react'
import { ReplyBranchNode } from './ReplyBranchNode'
import type { AutomationNodeData } from '../utils/flowConverter'
import type { ReplyBranchNodeConfig } from '../../../services/api/automation'

i18n.loadAndActivate({ locale: 'en', messages: {} })

vi.mock('@xyflow/react', () => ({
  Handle: ({ id }: { id?: string }) => <div data-testid="handle" data-handle-id={id || 'default'} />,
  Position: { Top: 'top', Bottom: 'bottom' },
  useConnection: () => ({ inProgress: false })
}))

const renderNode = (config: ReplyBranchNodeConfig) => {
  const props = {
    data: { nodeType: 'reply_branch', config, label: 'Reply Branch' } as AutomationNodeData,
    selected: false
  } as unknown as NodeProps<AutomationNodeData>
  return render(
    <I18nProvider i18n={i18n}>
      <ReplyBranchNode {...props} />
    </I18nProvider>
  )
}

describe('ReplyBranchNode', () => {
  it('renders the Reply Branch label and handle labels (non-empty)', () => {
    renderNode({ replied_node_id: '', not_replied_node_id: '' })
    expect(screen.getByText('Reply Branch')).toBeInTheDocument()
    expect(screen.getByText('Replied')).toBeInTheDocument()
    expect(screen.getByText('No reply')).toBeInTheDocument()
    expect(screen.getByText('Did the contact reply?')).toBeInTheDocument()
  })

  it('renders exactly two source handles (replied / not_replied) plus the target', () => {
    renderNode({ replied_node_id: '', not_replied_node_id: '' })
    const handles = screen.getAllByTestId('handle')
    // 1 target (top) + 2 source (replied, not_replied)
    expect(handles).toHaveLength(3)
    const ids = handles.map((h) => h.getAttribute('data-handle-id'))
    expect(ids).toContain('replied')
    expect(ids).toContain('not_replied')
  })
})
