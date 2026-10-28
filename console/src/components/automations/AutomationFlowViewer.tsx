import React, { useMemo, useEffect, useState } from 'react'
import { useQuery } from '@tanstack/react-query'
import {
  ReactFlow,
  Background,
  Controls,
  ReactFlowProvider,
  type Node,
  type Edge,
  type NodeTypes,
  BackgroundVariant
} from '@xyflow/react'
import { Spin } from 'antd'
import { useLingui } from '@lingui/react/macro'
import { StatNode, FilterStatNode, ABTestStatNode, type StatNodeData } from './nodes/StatNode'
import { VeridianFlowNodeDrawer } from './veridian_flow_node_drawer'
import { emailNodeTitle, replyRatePercent, stageForNode } from './veridian_flow_labels'
import { prospectionStatsService, type ProspectionSequence } from '../../services/api/veridian_prospection'
import type { Template } from '../../services/api/template'
import type { Workspace } from '../../services/api/types'
import { layoutNodes } from './utils/layoutNodes'
import type {
  Automation,
  AutomationNodeStats,
  BranchNodeConfig,
  FilterNodeConfig,
  ABTestNodeConfig,
  ListStatusBranchNodeConfig,
  ReplyBranchNodeConfig
} from '../../services/api/automation'
import type { DelayNodeConfig, EmailNodeConfig } from '../../services/api/automation'

import '@xyflow/react/dist/style.css'

// Node types for the viewer
const nodeTypes: NodeTypes = {
  trigger: StatNode,
  delay: StatNode,
  email: StatNode,
  branch: StatNode,
  filter: FilterStatNode,
  add_to_list: StatNode,
  remove_from_list: StatNode,
  ab_test: ABTestStatNode,
  webhook: StatNode,
  list_status_branch: StatNode,
  reply_branch: StatNode
}

interface AutomationFlowViewerProps {
  automation: Automation
  nodeStats: Record<string, AutomationNodeStats> | null
  loading?: boolean
  onHeightCalculated?: (height: number) => void
  // Pour nommer les nœuds email (modèle, étape) et ouvrir le tiroir (modifier le modèle).
  workspace?: Workspace
  templates?: Template[]
  canEditTemplates?: boolean
}

const NO_TEMPLATES: Template[] = []

interface ViewerLabels {
  emailFallback: string
  openTemplate: string
  details: string
  onOpen?: (nodeId: string) => void
  sequence?: ProspectionSequence
  templates: Template[]
}

// Stats viewer uses smaller nodes
const STATS_NODE_WIDTH = 220

// Données d'un nœud du flux : un email porte « J+5 · Relance 1 », son objet et les réponses de
// son étape ; un délai porte sa durée ; les autres types gardent leurs compteurs seuls.
function nodeDataFor(
  node: Automation['nodes'][number],
  nodeStats: Record<string, AutomationNodeStats> | null,
  labels: ViewerLabels
): StatNodeData {
  const base: StatNodeData = {
    nodeType: node.type,
    label: node.type === 'trigger' ? 'Trigger' : undefined,
    stats: nodeStats?.[node.id],
    config: node.config
  }
  if (node.type === 'email') {
    const templateId = (node.config as EmailNodeConfig | undefined)?.template_id
    const template = labels.templates.find((tpl) => tpl.id === templateId)
    const stage = stageForNode(labels.sequence, node.id)
    return {
      ...base,
      label: emailNodeTitle(stage?.label, template?.name, labels.emailFallback),
      subtitle: template?.email?.subject || undefined,
      repliesHuman: stage?.replies_human,
      replyRate: stage ? replyRatePercent(stage.replies_human, stage.sent) : undefined,
      onOpen: labels.onOpen ? () => labels.onOpen?.(node.id) : undefined,
      openLabel: labels.openTemplate
    }
  }
  if (node.type === 'delay') {
    const delay = node.config as DelayNodeConfig | undefined
    const duration = delay?.duration ?? 0
    return {
      ...base,
      subtitle: duration > 0 ? `${duration} ${delay?.unit ?? ''}` : undefined,
      onOpen: labels.onOpen ? () => labels.onOpen?.(node.id) : undefined,
      openLabel: labels.details
    }
  }
  return base
}

// Convert automation to ReactFlow nodes with stats
function automationToViewerFlow(
  automation: Automation,
  nodeStats: Record<string, AutomationNodeStats> | null,
  labels: ViewerLabels
): { nodes: Node<StatNodeData>[]; edges: Edge[] } {
  if (!automation.nodes || automation.nodes.length === 0) {
    return { nodes: [], edges: [] }
  }

  // Convert automation nodes to ReactFlow nodes with stats
  const nodes: Node<StatNodeData>[] = automation.nodes.map((node) => ({
    id: node.id,
    type: node.type,
    position: node.position,
    data: nodeDataFor(node, nodeStats, labels)
  }))

  // Generate edges from next_node_id relationships
  const edges: Edge[] = []

  automation.nodes.forEach((node) => {
    // Standard next_node_id connection
    if (node.next_node_id) {
      edges.push({
        id: `${node.id}-${node.next_node_id}`,
        source: node.id,
        target: node.next_node_id,
        type: 'smoothstep',
        animated: false,
        style: { stroke: '#94a3b8', strokeWidth: 2 }
      })
    }

    // Handle branch nodes with multiple paths
    if (node.type === 'branch' && node.config) {
      const config = node.config as BranchNodeConfig
      if (config.paths) {
        config.paths.forEach((path) => {
          if (path.next_node_id) {
            edges.push({
              id: `${node.id}-${path.id}-${path.next_node_id}`,
              source: node.id,
              sourceHandle: path.id,
              target: path.next_node_id,
              type: 'smoothstep',
              label: path.name,
              style: { stroke: '#94a3b8', strokeWidth: 2 }
            })
          }
        })
      }
    }

    // Handle filter nodes with continue/exit paths
    if (node.type === 'filter' && node.config) {
      const config = node.config as FilterNodeConfig
      if (config.continue_node_id) {
        edges.push({
          id: `${node.id}-continue-${config.continue_node_id}`,
          source: node.id,
          sourceHandle: 'yes',
          target: config.continue_node_id,
          type: 'smoothstep',
          label: 'Yes',
          style: { stroke: '#22c55e', strokeWidth: 2 }
        })
      }
      if (config.exit_node_id) {
        edges.push({
          id: `${node.id}-exit-${config.exit_node_id}`,
          source: node.id,
          sourceHandle: 'no',
          target: config.exit_node_id,
          type: 'smoothstep',
          label: 'No',
          style: { stroke: '#ef4444', strokeWidth: 2 }
        })
      }
    }

    // Handle A/B test nodes with multiple variants
    if (node.type === 'ab_test' && node.config) {
      const config = node.config as ABTestNodeConfig
      if (config.variants) {
        config.variants.forEach((variant) => {
          if (variant.next_node_id) {
            edges.push({
              id: `${node.id}-${variant.id}-${variant.next_node_id}`,
              source: node.id,
              sourceHandle: variant.id,
              target: variant.next_node_id,
              type: 'smoothstep',
              label: `${variant.name} (${variant.weight}%)`,
              style: { stroke: '#94a3b8', strokeWidth: 2 }
            })
          }
        })
      }
    }

    // Handle list status branch nodes with three paths
    if (node.type === 'list_status_branch' && node.config) {
      const config = node.config as ListStatusBranchNodeConfig
      if (config.not_in_list_node_id) {
        edges.push({
          id: `${node.id}-not_in_list-${config.not_in_list_node_id}`,
          source: node.id,
          sourceHandle: 'not_in_list',
          target: config.not_in_list_node_id,
          type: 'smoothstep',
          style: { stroke: '#9ca3af', strokeWidth: 2 }
        })
      }
      if (config.active_node_id) {
        edges.push({
          id: `${node.id}-active-${config.active_node_id}`,
          source: node.id,
          sourceHandle: 'active',
          target: config.active_node_id,
          type: 'smoothstep',
          style: { stroke: '#22c55e', strokeWidth: 2 }
        })
      }
      if (config.non_active_node_id) {
        edges.push({
          id: `${node.id}-non_active-${config.non_active_node_id}`,
          source: node.id,
          sourceHandle: 'non_active',
          target: config.non_active_node_id,
          type: 'smoothstep',
          style: { stroke: '#f97316', strokeWidth: 2 }
        })
      }
    }

    // Handle reply branch nodes with two paths (replied / not_replied)
    if (node.type === 'reply_branch' && node.config) {
      const config = node.config as ReplyBranchNodeConfig
      if (config.replied_node_id) {
        edges.push({
          id: `${node.id}-replied-${config.replied_node_id}`,
          source: node.id,
          sourceHandle: 'replied',
          target: config.replied_node_id,
          type: 'smoothstep',
          label: 'Replied',
          style: { stroke: '#08979c', strokeWidth: 2 }
        })
      }
      if (config.not_replied_node_id) {
        edges.push({
          id: `${node.id}-not_replied-${config.not_replied_node_id}`,
          source: node.id,
          sourceHandle: 'not_replied',
          target: config.not_replied_node_id,
          type: 'smoothstep',
          label: 'No reply',
          style: { stroke: '#6b7280', strokeWidth: 2 }
        })
      }
    }
  })

  // Apply hierarchical layout to nodes
  const layoutedNodes = layoutNodes(nodes, edges, { nodeWidth: STATS_NODE_WIDTH })

  return { nodes: layoutedNodes, edges }
}

const AutomationFlowViewerInner: React.FC<AutomationFlowViewerProps> = ({
  automation,
  nodeStats,
  loading,
  onHeightCalculated
}) => {
  const { t } = useLingui()
  const { workspace, templates = NO_TEMPLATES, canEditTemplates = false } = props
  const [openNodeId, setOpenNodeId] = useState<string | null>(null)

  // Stats de séquence (étapes, réponses par étape) : nécessaires au libellé et aux réponses.
  // Une automation qui n'est pas une séquence n'a pas d'étape : le flux garde ses titres d'origine.
  const { data: prospection } = useQuery({
    queryKey: ['prospection', 'stats', workspace?.id ?? ''],
    queryFn: () => prospectionStatsService.get({ workspace_id: workspace?.id ?? '' }),
    enabled: !!workspace,
    retry: false
  })
  const sequence = prospection?.sequences.find((s) => s.automation_id === automation.id)

  const { nodes, edges } = useMemo(
    () =>
      automationToViewerFlow(automation, nodeStats, {
        emailFallback: t`Email`,
        openTemplate: t`Open template`,
        details: t`Details`,
        onOpen: workspace ? setOpenNodeId : undefined,
        sequence,
        templates
      }),
    [automation, nodeStats, sequence, templates, workspace, t]
  )
  const openNode = openNodeId ? automation.nodes.find((n) => n.id === openNodeId) : undefined
  const openDelay = openNode?.type === 'delay' ? (openNode.config as DelayNodeConfig | undefined) : undefined

  // Calculate and report height based on node positions
  useEffect(() => {
    if (nodes.length > 0 && onHeightCalculated) {
      const maxY = Math.max(...nodes.map((n) => n.position.y))
      const nodeHeight = 100 // approximate rendered node height
      const calculatedHeight = Math.max(300, maxY + nodeHeight + 80)
      onHeightCalculated(calculatedHeight)
    }
  }, [nodes, onHeightCalculated])

  if (loading) {
    return (
      <div className="flex items-center justify-center h-full">
        <Spin tip={t`Loading stats...`} />
      </div>
    )
  }

  if (nodes.length === 0) {
    return (
      <div className="flex items-center justify-center h-full text-gray-400">
        {t`No nodes in this automation`}
      </div>
    )
  }

  return (
    <>
      <ReactFlow
        nodes={nodes}
        edges={edges}
        nodeTypes={nodeTypes}
        onNodeClick={(_, node) => {
          if ((node.type === 'email' || node.type === 'delay') && workspace) setOpenNodeId(node.id)
        }}
        nodesDraggable={false}
        nodesConnectable={false}
        elementsSelectable={false}
        panOnDrag={true}
        panOnScroll={true}
        zoomOnScroll={true}
        zoomOnPinch={true}
        zoomOnDoubleClick={false}
        fitView
        fitViewOptions={{ padding: 0.2, maxZoom: 1.5 }}
        minZoom={0.2}
        maxZoom={2}
        proOptions={{ hideAttribution: true }}
      >
        <Controls position="top-left" showInteractive={false} />
        <Background variant={BackgroundVariant.Dots} gap={16} size={1} color="#e5e7eb" />
      </ReactFlow>
    {workspace && openNode && (
      <VeridianFlowNodeDrawer
        open
        onClose={() => setOpenNodeId(null)}
        kind={openNode.type === 'delay' ? 'delay' : 'email'}
        workspace={workspace}
        canEdit={canEditTemplates}
        templateId={(openNode.config as EmailNodeConfig | undefined)?.template_id}
        stageLabel={stageForNode(sequence, openNode.id)?.label}
        stage={stageForNode(sequence, openNode.id)}
        nodeStats={nodeStats?.[openNode.id]}
        delay={openDelay ? { duration: openDelay.duration, unit: openDelay.unit } : undefined}
      />
    )}
    </>
  )
}

export const AutomationFlowViewer: React.FC<AutomationFlowViewerProps> = (props) => {
  return (
    <ReactFlowProvider>
      <AutomationFlowViewerInner {...props} />
    </ReactFlowProvider>
  )
}
