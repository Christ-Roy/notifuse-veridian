import React, { useId, useMemo, useState } from 'react'
import { useMutation, useQueryClient, useQuery } from '@tanstack/react-query'
import { Space, Dropdown, Modal, Badge, Tag, Popover, message, Progress, Button, Input } from 'antd'
import { FolderOutlined, FolderOpenOutlined, DownOutlined, RightOutlined, SearchOutlined } from '@ant-design/icons'
import { useLingui } from '@lingui/react/macro'
import { deleteSegment, rebuildSegment, type Segment } from '../../services/api/segment'
import { taskApi } from '../../services/api/task'
import ButtonUpsertSegment from '../segment/button_upsert'
import {
  buildSegmentTree,
  filterSegmentTree,
  folderKeysContaining,
  hasFolders,
  segmentsIn,
  type SegmentFolderNode,
  type SegmentTreeNode
} from '../segment/veridian_segment_folders'
import numbro from 'numbro'

interface SegmentsFilterProps {
  workspaceId: string
  segments: Segment[]
  selectedSegmentIds?: string[]
  totalContacts?: number
  onSegmentToggle: (segmentId: string) => void
}

// Separate component for each segment button to handle individual task fetching
interface SegmentButtonProps {
  segment: Segment
  // Libellé affiché (dernier niveau du chemin dans un dossier). Par défaut : le nom complet.
  label?: string
  workspaceId: string
  isSelected: boolean
  totalContacts?: number
  onToggle: () => void
  onDelete: (segmentId: string) => void
  onRebuild: (segmentId: string) => void
}

function SegmentButton({
  segment,
  label,
  workspaceId,
  isSelected,
  totalContacts,
  onToggle,
  onDelete,
  onRebuild
}: SegmentButtonProps) {
  const { t } = useLingui()
  const queryClient = useQueryClient()

  // Fetch task for building segments
  const { data: task } = useQuery({
    queryKey: ['segment-task', workspaceId, segment.id],
    queryFn: () => taskApi.findBySegmentId(workspaceId, segment.id),
    enabled: segment.status === 'building',
    refetchInterval: segment.status === 'building' ? 15000 : false // Poll every 15 seconds when building
  })

  // Get status badge color and content for popover
  const getStatusBadge = () => {
    switch (segment.status) {
      case 'active':
        return { status: 'success', title: t`Active`, content: t`Ready to use` }
      case 'building': {
        if (task?.state?.build_segment) {
          const buildState = task.state.build_segment
          const progress = task.progress || 0
          return {
            status: 'processing',
            title: t`Building segment`,
            content: (
              <div>
                <Progress
                  percent={Math.round(progress)}
                  size="small"
                  style={{ marginBottom: '12px' }}
                />
                <div>
                  {t`Processed`}:{' '}
                  {numbro(buildState.processed_count).format({ thousandSeparated: true })}
                </div>
                <div>
                  {t`Matched`}: {numbro(buildState.matched_count).format({ thousandSeparated: true })}
                </div>
                {buildState.total_contacts > 0 && (
                  <div>
                    {t`Total`}: {numbro(buildState.total_contacts).format({ thousandSeparated: true })}
                  </div>
                )}
              </div>
            )
          }
        }
        return { status: 'processing', title: t`Building`, content: t`Processing contacts` }
      }
      case 'deleted':
        return { status: 'error', title: t`Deleted`, content: t`Will be removed` }
      default:
        return { status: 'default', title: t`Unknown`, content: t`Unknown status` }
    }
  }

  const statusBadge = getStatusBadge()

  return (
    <Dropdown.Button
      key={segment.id}
      size="small"
      onClick={onToggle}
      buttonsRender={([leftButton, rightButton]) => [
        React.cloneElement(leftButton as React.ReactElement, {
          color: isSelected ? 'primary' : 'default',
          variant: 'outlined'
        }),
        React.cloneElement(rightButton as React.ReactElement, {
          color: isSelected ? 'primary' : 'default',
          variant: 'outlined'
        })
      ]}
      menu={{
        items: [
          {
            key: 'update',
            label: (
              <ButtonUpsertSegment
                segment={segment}
                totalContacts={totalContacts}
                onSuccess={() => {
                  queryClient.invalidateQueries({ queryKey: ['segments', workspaceId] })
                }}
              >
                <span>{t`Update`}</span>
              </ButtonUpsertSegment>
            )
          },
          {
            key: 'rebuild',
            label: t`Rebuild`,
            onClick: () => {
              Modal.confirm({
                title: t`Rebuild segment`,
                content: t`Are you sure you want to rebuild "${segment.name}"? This will recalculate segment membership.`,
                okText: t`Yes`,
                cancelText: t`No`,
                onOk: () => {
                  onRebuild(segment.id)
                }
              })
            }
          },
          {
            key: 'delete',
            label: <span style={{ color: '#ff4d4f' }}>{t`Delete`}</span>,
            onClick: () => {
              Modal.confirm({
                title: t`Delete segment`,
                content: t`Are you sure you want to delete "${segment.name}"?`,
                okText: t`Yes`,
                cancelText: t`No`,
                okButtonProps: { danger: true },
                onOk: () => {
                  onDelete(segment.id)
                }
              })
            }
          }
        ]
      }}
    >
      <Space size="small">
        <Popover title={statusBadge.title} content={statusBadge.content}>
          <span>
            <Badge status={statusBadge.status as "success" | "processing" | "error" | "default" | "warning"} />
          </span>
        </Popover>
        <Tag
          bordered={false}
          color={segment.color}
          style={{ margin: 0, opacity: segment.users_count === 0 ? 0.5 : undefined }}
        >
          {label ?? segment.name}
          {segment.users_count !== undefined && (
            <span style={{ marginLeft: '4px', opacity: 0.8 }}>
              (
              {numbro(segment.users_count).format({
                thousandSeparated: true,
                mantissa: 0
              })}
              )
            </span>
          )}
        </Tag>
      </Space>
    </Dropdown.Button>
  )
}

export function SegmentsFilter({
  workspaceId,
  segments,
  selectedSegmentIds = [],
  totalContacts,
  onSegmentToggle
}: SegmentsFilterProps) {
  const { t } = useLingui()
  const queryClient = useQueryClient()

  // Delete segment mutation
  const deleteSegmentMutation = useMutation({
    mutationFn: (segmentId: string) =>
      deleteSegment({
        workspace_id: workspaceId,
        id: segmentId
      }),
    onSuccess: () => {
      message.success(t`Segment deleted successfully`)
      queryClient.invalidateQueries({ queryKey: ['segments', workspaceId] })
    },
    onError: (error: Error) => {
      message.error(error?.message || t`Failed to delete segment`)
    }
  })

  // Rebuild segment mutation
  const rebuildSegmentMutation = useMutation({
    mutationFn: (segmentId: string) =>
      rebuildSegment({
        workspace_id: workspaceId,
        segment_id: segmentId
      }),
    onSuccess: (data) => {
      message.success(data.message || t`Segment rebuild started successfully`)
      queryClient.invalidateQueries({ queryKey: ['segments', workspaceId] })
    },
    onError: (error: Error) => {
      message.error(error?.message || t`Failed to rebuild segment`)
    }
  })

  // Dossiers : l'arbre est dérivé du nom (voir veridian_segment_folders.ts).
  const [query, setQuery] = useState('')
  const panelPrefix = useId()
  const panelId = (key: string) => `${panelPrefix}-${key.replace(/[^a-zA-Z0-9_-]/g, '_')}`
  // Choix explicite de l'utilisateur par dossier. Sinon : ouvert si un segment sélectionné s'y trouve.
  const [overrides, setOverrides] = useState<Map<string, boolean>>(() => new Map())
  const tree = useMemo(() => buildSegmentTree(segments), [segments])
  const nested = hasFolders(tree)
  const visibleTree = useMemo(() => filterSegmentTree(tree, query), [tree, query])
  const searching = query.trim() !== ''
  const selectedSet = new Set(selectedSegmentIds)
  const withSelection = new Set(folderKeysContaining(tree, selectedSet))

  const isOpen = (key: string) => {
    if (searching) return true
    return overrides.get(key) ?? withSelection.has(key)
  }
  const toggleFolder = (key: string) => {
    if (searching) return
    const next = !isOpen(key)
    setOverrides((prev) => new Map(prev).set(key, next))
  }

  const renderSegment = (leaf: Extract<SegmentTreeNode<Segment>, { kind: 'segment' }>) => (
    <SegmentButton
      key={leaf.key}
      segment={leaf.segment}
      label={leaf.label}
      workspaceId={workspaceId}
      isSelected={selectedSet.has(leaf.segment.id)}
      totalContacts={totalContacts}
      onToggle={() => onSegmentToggle(leaf.segment.id)}
      onDelete={(segmentId) => deleteSegmentMutation.mutate(segmentId)}
      onRebuild={(segmentId) => rebuildSegmentMutation.mutate(segmentId)}
    />
  )

  const renderFolderButton = (folderNode: SegmentFolderNode<Segment>) => {
    const open = isOpen(folderNode.key)
    const selectedInside = segmentsIn(folderNode.children).filter((s) =>
      selectedSet.has(s.id)
    ).length
    return (
      <Button
        key={folderNode.key}
        size="small"
        type={selectedInside > 0 ? 'primary' : 'default'}
        ghost={selectedInside > 0}
        icon={open ? <FolderOpenOutlined /> : <FolderOutlined />}
        onClick={() => toggleFolder(folderNode.key)}
        disabled={searching}
        aria-expanded={open}
        aria-controls={open ? panelId(folderNode.key) : undefined}
      >
        <Space size={4}>
          {folderNode.label}
          <span style={{ opacity: 0.7 }}>{folderNode.count}</span>
          {selectedInside > 0 && <Badge count={selectedInside} size="small" />}
          {open ? <DownOutlined style={{ fontSize: 10 }} /> : <RightOutlined style={{ fontSize: 10 }} />}
        </Space>
      </Button>
    )
  }

  // Un niveau : dossiers et segments sur une même ligne, puis le contenu des dossiers ouverts.
  const renderLevel = (nodes: SegmentTreeNode<Segment>[], trailing?: React.ReactNode) => {
    const folders = nodes.filter(
      (node): node is SegmentFolderNode<Segment> => node.kind === 'folder'
    )
    const leaves = nodes.filter(
      (node): node is Extract<SegmentTreeNode<Segment>, { kind: 'segment' }> => node.kind === 'segment'
    )
    return (
      <div className="flex flex-col gap-2 min-w-0">
        <Space wrap>
          {folders.map(renderFolderButton)}
          {leaves.map(renderSegment)}
          {trailing}
        </Space>
        {folders
          .filter((folderNode) => isOpen(folderNode.key) && folderNode.children.length > 0)
          .map((folderNode) => (
            <div
              key={`${folderNode.key}:children`}
              id={panelId(folderNode.key)}
              className="ml-2 pl-3 border-l-2"
              style={{ borderColor: 'var(--ant-color-border, #d9d9d9)' }}
            >
              {renderLevel(folderNode.children)}
            </div>
          ))}
      </div>
    )
  }

  const upsertButton = (
    <ButtonUpsertSegment
      btnType="primary"
      btnSize="small"
      totalContacts={totalContacts}
      onSuccess={() => {
        queryClient.invalidateQueries({ queryKey: ['segments', workspaceId] })
      }}
    />
  )

  // Sans dossier : même rendu qu'avant (une seule rangée de segments).
  if (!nested) {
    return (
      <div className="flex items-center gap-2 mb-6">
        <div className="text-sm font-medium">{t`Segments`}:</div>
        <Space wrap>
          {segments.map((segment: Segment) => renderSegment({ kind: 'segment', key: segment.id, label: segment.name, segment }))}
          {upsertButton}
        </Space>
      </div>
    )
  }

  return (
    <div className="flex items-start gap-2 mb-6">
      <div className="text-sm font-medium pt-1">{t`Segments`}:</div>
      <div className="flex flex-col gap-2 min-w-0 flex-1">
        <div>
          <Input
            allowClear
            size="small"
            prefix={<SearchOutlined />}
            placeholder={t`Search`}
            aria-label={t`Search`}
            value={query}
            onChange={(e) => setQuery(e.target.value)}
            style={{ width: 240 }}
          />
        </div>
        {renderLevel(visibleTree, upsertButton)}
      </div>
    </div>
  )
}
