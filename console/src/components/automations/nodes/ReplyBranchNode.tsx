import React from 'react'
import { Handle, Position, useConnection, type NodeProps } from '@xyflow/react'
import { MailQuestion } from 'lucide-react'
import { useLingui } from '@lingui/react/macro'
import { BaseNode } from './BaseNode'
import { nodeTypeColors } from './constants'
import type { AutomationNodeData } from '../utils/flowConverter'

type ReplyBranchNodeProps = NodeProps<AutomationNodeData>

export const ReplyBranchNode: React.FC<ReplyBranchNodeProps> = ({ data, selected }) => {
  const { t } = useLingui()

  const connection = useConnection()
  const isConnecting = connection.inProgress
  const targetHandleSize = isConnecting ? 16 : 10
  const targetHandleColor = isConnecting ? '#22c55e' : data.isOrphan ? '#f97316' : '#3b82f6'

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
        type="reply_branch"
        label={t`Reply Branch`}
        icon={<MailQuestion size={14} style={{ color: selected ? undefined : nodeTypeColors.reply_branch }} />}
        selected={selected}
        isOrphan={data.isOrphan}
        onDelete={data.onDelete}
      >
        <div className="text-xs text-gray-600 truncate max-w-[180px]">{t`Did the contact reply?`}</div>
        {/* Replied / No reply labels for handles */}
        <div className="flex justify-between text-xs mt-2 px-4">
          <span className="text-teal-600 font-medium">{t`Replied`}</span>
          <span className="text-gray-500 font-medium">{t`No reply`}</span>
        </div>
      </BaseNode>
      {/* Two fixed source handles: replied (left) and not_replied (right) */}
      <Handle
        type="source"
        position={Position.Bottom}
        id="replied"
        style={{
          background: '#08979c', // teal = replied
          width: 10,
          height: 10,
          left: '30%'
        }}
      />
      <Handle
        type="source"
        position={Position.Bottom}
        id="not_replied"
        style={{
          background: '#6b7280', // gray = no reply
          width: 10,
          height: 10,
          left: '70%'
        }}
      />
    </>
  )
}
