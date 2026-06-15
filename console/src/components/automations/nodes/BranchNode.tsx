import React from 'react'
import { Handle, Position, useConnection, type NodeProps } from '@xyflow/react'
import { GitBranch } from 'lucide-react'
import { useLingui } from '@lingui/react/macro'
import { BaseNode } from './BaseNode'
import { nodeTypeColors } from './constants'
import type { AutomationNodeData } from '../utils/flowConverter'
import type { BranchNodeConfig } from '../../../services/api/automation'

type BranchNodeProps = NodeProps<AutomationNodeData>

export const BranchNode: React.FC<BranchNodeProps> = ({ data, selected }) => {
  const { t } = useLingui()
  const config = data.config as BranchNodeConfig
  const paths = config?.paths || []

  const connection = useConnection()
  const isConnecting = connection.inProgress
  const targetHandleSize = isConnecting ? 16 : 10
  const targetHandleColor = isConnecting ? '#22c55e' : data.isOrphan ? '#f97316' : '#3b82f6'
  const sourceHandleColor = data.isOrphan ? '#f97316' : '#722ed1' // purple = branch color

  // Spread handles evenly across bottom (same pattern as ABTestNode)
  const getHandlePosition = (index: number, total: number): number => {
    if (total === 1) return 50
    const start = 20
    const end = 80
    return start + (index * (end - start)) / (total - 1)
  }

  return (
    <>
      <Handle
        type="target"
        position={Position.Top}
        style={{
          background: targetHandleColor,
          width: targetHandleSize,
          height: targetHandleSize,
          transition: 'all 0.15s ease'
        }}
      />
      <BaseNode
        type="branch"
        label={t`Branch`}
        icon={<GitBranch size={14} style={{ color: selected ? undefined : nodeTypeColors.branch }} />}
        selected={selected}
        isOrphan={data.isOrphan}
        onDelete={data.onDelete}
      >
        {paths.length === 0 ? (
          <div className="text-orange-500 text-xs">{t`Configure paths`}</div>
        ) : (
          <div className="flex flex-wrap gap-1 mt-1">
            {paths.map((path) => (
              <div
                key={path.id}
                className={`text-xs px-2 py-1 rounded ${
                  path.id === config?.default_path_id
                    ? 'bg-gray-200 text-gray-700'
                    : 'bg-purple-100 text-purple-700'
                }`}
              >
                {path.name}
              </div>
            ))}
          </div>
        )}
      </BaseNode>
      {/* One source handle per path */}
      {paths.map((path, index) => (
        <Handle
          key={path.id}
          type="source"
          position={Position.Bottom}
          id={path.id}
          style={{
            background: sourceHandleColor,
            width: 10,
            height: 10,
            left: `${getHandlePosition(index, paths.length)}%`
          }}
        />
      ))}
      {/* Fallback handle when no paths configured yet */}
      {paths.length === 0 && (
        <Handle
          type="source"
          position={Position.Bottom}
          style={{ background: sourceHandleColor, width: 10, height: 10 }}
        />
      )}
    </>
  )
}
