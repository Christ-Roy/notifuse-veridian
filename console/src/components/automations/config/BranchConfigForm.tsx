import React from 'react'
import { Form, Input, Button, Select, Alert } from 'antd'
import { PlusOutlined, DeleteOutlined } from '@ant-design/icons'
import { useLingui } from '@lingui/react/macro'
import { v4 as uuidv4 } from 'uuid'
import { TreeNodeInput } from '../../segment/input'
import { TableSchemas } from '../../segment/table_schemas'
import { useAutomation } from '../context'
import type { BranchNodeConfig, BranchPath } from '../../../services/api/automation'
import type { TreeNode } from '../../../services/api/segment'

interface BranchConfigFormProps {
  config: BranchNodeConfig
  onChange: (config: BranchNodeConfig) => void
}

// Empty tree structure required by TreeNodeInput (same as FilterConfigForm)
const EMPTY_TREE: TreeNode = {
  kind: 'branch',
  branch: {
    operator: 'and',
    leaves: []
  }
}

const MAX_PATHS = 6
const MIN_PATHS = 1

// createDefaultPaths builds the initial paths + default path id for a fresh branch node.
// One conditional path + one default ("Otherwise") catch-all path.
function createDefaultPaths(): { paths: BranchPath[]; default_path_id: string } {
  const defaultId = uuidv4()
  return {
    paths: [
      { id: uuidv4(), name: 'Path 1', conditions: EMPTY_TREE, next_node_id: '' },
      { id: defaultId, name: 'Otherwise', conditions: undefined, next_node_id: '' }
    ],
    default_path_id: defaultId
  }
}

export const BranchConfigForm: React.FC<BranchConfigFormProps> = ({ config, onChange }) => {
  const { t } = useLingui()
  const { lists, workspace } = useAutomation()

  // Initialize with defaults if empty (run once)
  const initializedRef = React.useRef(false)
  React.useEffect(() => {
    if (!initializedRef.current && (!config?.paths || config.paths.length === 0)) {
      initializedRef.current = true
      onChange(createDefaultPaths())
    }
  }, [config?.paths, onChange])

  const paths = config?.paths && config.paths.length > 0 ? config.paths : createDefaultPaths().paths
  const defaultPathId = config?.default_path_id || ''

  const updatePath = (index: number, patch: Partial<BranchPath>) => {
    const updated = paths.map((p, i) => (i === index ? { ...p, ...patch } : p))
    onChange({ ...config, paths: updated, default_path_id: defaultPathId })
  }

  const handleAddPath = () => {
    if (paths.length >= MAX_PATHS) return
    const newPath: BranchPath = {
      id: uuidv4(),
      name: `Path ${paths.length}`,
      conditions: EMPTY_TREE,
      next_node_id: ''
    }
    // Insert the new conditional path BEFORE the default catch-all path so the
    // default always stays last (evaluation order matters: conditional first).
    const defaultIdx = paths.findIndex((p) => p.id === defaultPathId)
    let updated: BranchPath[]
    if (defaultIdx >= 0) {
      updated = [...paths.slice(0, defaultIdx), newPath, ...paths.slice(defaultIdx)]
    } else {
      updated = [...paths, newPath]
    }
    onChange({ ...config, paths: updated, default_path_id: defaultPathId })
  }

  const handleRemovePath = (index: number) => {
    if (paths.length <= MIN_PATHS) return
    const removed = paths[index]
    const updated = paths.filter((_, i) => i !== index)
    // If we removed the default path, promote the (new) last path as default.
    let nextDefault = defaultPathId
    if (removed.id === defaultPathId) {
      nextDefault = updated.length > 0 ? updated[updated.length - 1].id : ''
    }
    onChange({ ...config, paths: updated, default_path_id: nextDefault })
  }

  const handleDefaultChange = (value: string) => {
    onChange({ ...config, paths, default_path_id: value })
  }

  return (
    <Form layout="vertical" className="nodrag">
      <Alert
        type="info"
        showIcon
        className="!mb-3"
        message={t`Branch Logic`}
        description={t`Each path is evaluated top to bottom. The contact follows the first path whose conditions match. If none match, the default path is taken.`}
      />

      <div className="space-y-3">
        {paths.map((path, index) => {
          const isDefault = path.id === defaultPathId
          return (
            <div key={path.id} className="border border-gray-200 rounded p-2">
              <div className="flex items-center gap-2 mb-2">
                <Input
                  value={path.name}
                  onChange={(e) => updatePath(index, { name: e.target.value })}
                  placeholder={t`Path name`}
                  style={{ flex: 1 }}
                  maxLength={60}
                />
                <Button
                  type="text"
                  icon={<DeleteOutlined />}
                  onClick={() => handleRemovePath(index)}
                  disabled={paths.length <= MIN_PATHS}
                  danger
                />
              </div>
              {isDefault ? (
                <div className="text-xs text-gray-500">
                  {t`Default path - taken when no other path matches.`}
                </div>
              ) : (
                <Form.Item
                  label={<span>{t`Conditions`} <span className="text-red-500">*</span></span>}
                  required={false}
                  className="!mb-0"
                >
                  <TreeNodeInput
                    value={path.conditions || EMPTY_TREE}
                    onChange={(newConditions: TreeNode) =>
                      updatePath(index, { conditions: newConditions })
                    }
                    schemas={TableSchemas}
                    lists={lists}
                    workspaceId={workspace.id}
                  />
                </Form.Item>
              )}
            </div>
          )
        })}
      </div>

      {paths.length < MAX_PATHS && (
        <Button
          type="primary"
          ghost
          block
          size="small"
          onClick={handleAddPath}
          icon={<PlusOutlined />}
          className="!mt-2"
        >
          {t`Add Path`}
        </Button>
      )}

      <Form.Item label={t`Default Path`} className="!mt-3 !mb-0" extra={t`Path taken when no conditions match`}>
        <Select
          value={defaultPathId || undefined}
          onChange={handleDefaultChange}
          style={{ width: '100%' }}
          options={paths.map((p) => ({ label: p.name, value: p.id }))}
          placeholder={t`Select a default path...`}
        />
      </Form.Item>
    </Form>
  )
}
