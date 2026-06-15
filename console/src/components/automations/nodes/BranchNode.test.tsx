import { describe, it, expect } from 'vitest'
import { render, screen } from '@testing-library/react'
import { i18n } from '@lingui/core'
import { I18nProvider } from '@lingui/react'
import type { NodeProps } from '@xyflow/react'
import { BranchNode } from './BranchNode'
import type { AutomationNodeData } from '../utils/flowConverter'
import type { BranchNodeConfig } from '../../../services/api/automation'

i18n.loadAndActivate({ locale: 'en', messages: {} })

// Mock @xyflow/react primitives so the node renders standalone (no ReactFlow provider).
vi.mock('@xyflow/react', () => ({
  Handle: ({ id }: { id?: string }) => <div data-testid="handle" data-handle-id={id || 'default'} />,
  Position: { Top: 'top', Bottom: 'bottom' },
  useConnection: () => ({ inProgress: false })
}))

const cfg = (paths: BranchNodeConfig['paths'], defaultId = ''): BranchNodeConfig => ({
  paths,
  default_path_id: defaultId
})

const renderNode = (config: BranchNodeConfig) => {
  const props = {
    data: { nodeType: 'branch', config, label: 'Branch' } as AutomationNodeData,
    selected: false
  } as unknown as NodeProps<AutomationNodeData>
  return render(
    <I18nProvider i18n={i18n}>
      <BranchNode {...props} />
    </I18nProvider>
  )
}

describe('BranchNode', () => {
  it('renders the Branch label (non-empty)', () => {
    renderNode(cfg([{ id: 'p1', name: 'A', next_node_id: '' }], 'p1'))
    expect(screen.getByText('Branch')).toBeInTheDocument()
  })

  it('renders one source handle per path plus the target handle', () => {
    renderNode(
      cfg(
        [
          { id: 'p1', name: 'VIP', next_node_id: '' },
          { id: 'p2', name: 'Regular', next_node_id: '' },
          { id: 'def', name: 'Otherwise', next_node_id: '' }
        ],
        'def'
      )
    )
    const handles = screen.getAllByTestId('handle')
    // 1 target (top) + 3 source (one per path)
    expect(handles).toHaveLength(4)
    const sourceIds = handles.map((h) => h.getAttribute('data-handle-id'))
    expect(sourceIds).toContain('p1')
    expect(sourceIds).toContain('p2')
    expect(sourceIds).toContain('def')
  })

  it('shows path names and a configure hint when empty', () => {
    renderNode(cfg([{ id: 'p1', name: 'Premium', next_node_id: '' }], 'p1'))
    expect(screen.getByText('Premium')).toBeInTheDocument()

    renderNode(cfg([], ''))
    expect(screen.getByText('Configure paths')).toBeInTheDocument()
  })
})
