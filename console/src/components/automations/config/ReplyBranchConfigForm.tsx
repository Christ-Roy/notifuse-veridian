import React from 'react'
import { Form, Alert } from 'antd'
import { useLingui } from '@lingui/react/macro'
import type { ReplyBranchNodeConfig } from '../../../services/api/automation'

interface ReplyBranchConfigFormProps {
  config: ReplyBranchNodeConfig
  onChange: (config: ReplyBranchNodeConfig) => void
}

// Reply branch has no editable fields: both targets (replied / not_replied) are wired
// via the node's output handles on the canvas, like Filter's Yes/No. This form only
// explains the routing so the admin understands the two outputs.
export const ReplyBranchConfigForm: React.FC<ReplyBranchConfigFormProps> = ({ config, onChange }) => {
  const { t } = useLingui()

  // Ensure the config shape exists so the canvas can populate it from handles.
  const initializedRef = React.useRef(false)
  React.useEffect(() => {
    if (!initializedRef.current && (config?.replied_node_id === undefined || config?.not_replied_node_id === undefined)) {
      initializedRef.current = true
      onChange({
        replied_node_id: config?.replied_node_id || '',
        not_replied_node_id: config?.not_replied_node_id || ''
      })
    }
  }, [config, onChange])

  return (
    <Form layout="vertical" className="nodrag">
      <Alert
        type="info"
        showIcon
        message={t`Route on reply`}
        description={
          <div className="text-xs space-y-2 mt-1">
            <p>
              {t`A prospect who replied is never re-emailed. Use the two outputs to route them to an action instead.`}
            </p>
            <ul className="space-y-1 list-disc pl-4">
              <li>
                <strong className="text-teal-600">{t`Replied`}:</strong>{' '}
                {t`Contact answered — route to an action (add to a "to call back" list, webhook to an AI agent, tag).`}
              </li>
              <li>
                <strong className="text-gray-500">{t`No reply`}:</strong>{' '}
                {t`Contact has not answered yet — continue the sequence.`}
              </li>
            </ul>
            <p className="text-gray-400">
              {t`Connect each output to the next node using the handles on the block.`}
            </p>
          </div>
        }
      />
    </Form>
  )
}
