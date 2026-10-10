import { describe, it, expect, vi } from 'vitest'
import { render, screen } from '@testing-library/react'
import { i18n } from '@lingui/core'
import { I18nProvider } from '@lingui/react'
import type { NodeProps } from '@xyflow/react'

import { StatNode, type StatNodeData } from './StatNode'

i18n.loadAndActivate({ locale: 'en', messages: {} })

vi.mock('@xyflow/react', () => ({
  Handle: () => <div />,
  Position: { Top: 'top', Bottom: 'bottom' }
}))

const renderNode = (data: StatNodeData) =>
  render(
    <I18nProvider i18n={i18n}>
      <StatNode {...({ data, selected: false } as unknown as NodeProps<StatNodeData>)} />
    </I18nProvider>
  )

const valueOf = (title: string) =>
  screen.getByText(title).closest('.ant-statistic')!.querySelector('.ant-statistic-content-value')!.textContent

describe('StatNode : noeud email (LOT 1)', () => {
  it('affiche trois compteurs distincts : en file, envoyés, échoués', () => {
    renderNode({
      nodeType: 'email',
      stats: { node_id: 'j0a', node_type: 'email', entered: 30, completed: 5, queued: 20, failed: 2, skipped: 0 }
    })
    expect(valueOf('Queued')).toBe('20')
    expect(valueOf('Sent')).toBe('5')
    expect(valueOf('Failed')).toBe('2')
    expect(screen.queryByText('Completed')).toBeNull()
  })

  it('sans statistiques : trois zéros', () => {
    renderNode({ nodeType: 'email' })
    expect(valueOf('Queued')).toBe('0')
    expect(valueOf('Sent')).toBe('0')
    expect(valueOf('Failed')).toBe('0')
  })

  it('les autres types de noeud ne changent pas : complété, pas de compteur en file', () => {
    renderNode({
      nodeType: 'delay',
      stats: { node_id: 'w', node_type: 'delay', entered: 4, completed: 3, queued: 0, failed: 0, skipped: 0 }
    })
    expect(valueOf('Completed')).toBe('3')
    expect(screen.queryByText('Queued')).toBeNull()
    expect(screen.queryByText('Sent')).toBeNull()
  })
})
