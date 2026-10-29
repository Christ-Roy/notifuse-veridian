import React, { useId } from 'react'
import { useMutation, useQueryClient, useQuery } from '@tanstack/react-query'
import { Space, Dropdown, Modal, Badge, Tag, Popover, message, Progress, Button } from 'antd'
import { FolderOutlined, FolderOpenOutlined, DownOutlined, RightOutlined, FolderAddOutlined } from '@ant-design/icons'
import { useLingui } from '@lingui/react/macro'
import { deleteSegment, rebuildSegment, type Segment } from '../../services/api/segment'
import { taskApi } from '../../services/api/task'
import ButtonUpsertSegment from '../segment/button_upsert'
import {
  folderKeysContaining,
  segmentsIn,
  type SegmentFolderNode,
  type SegmentTreeNode
} from '../segment/veridian_segment_folders'
import { useVeridianFolders } from '../common/veridian_folder_manager'
import { FolderMenu } from '../common/veridian_folder_ui'
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
  // Dossiers natifs : « Déplacer vers… » (alternative clavier du glisser-déposer).
  onMove?: () => void
}

function SegmentButton({
  segment,
  label,
  workspaceId,
  isSelected,
  totalContacts,
  onToggle,
  onDelete,
  onRebuild,
  onMove
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
          ...(onMove ? [{ key: 'move', label: t`Move to…`, onClick: onMove }] : []),
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

  // Dossiers natifs (lot L2) : arbre serveur, pli mémorisé, recherche, glisser-déposer et clavier.
  // Voir components/common/veridian_folder_manager.tsx ; repli sur les noms « A / B » si l'API échoue.
  const folders = useVeridianFolders<Segment>({ workspaceId, kind: 'segments', items: segments, canWrite: true })
  const { tree, visibleTree, nested, searching, dnd } = folders
  const panelPrefix = useId()
  const panelId = (key: string) => `${panelPrefix}-${key.replace(/[^a-zA-Z0-9_-]/g, '_')}`
  const selectedSet = new Set(selectedSegmentIds)
  // Sans choix mémorisé : ouvert si un segment sélectionné s'y trouve.
  const withSelection = new Set(folderKeysContaining(tree, selectedSet))
  const isOpen = (node: SegmentFolderNode<Segment>) => folders.isOpen(node, withSelection.has(node.key))
  const overStyle = (key: string): React.CSSProperties =>
    dnd.overKey === key ? { outline: '2px dashed var(--ant-color-primary, #1677ff)', outlineOffset: 2, borderRadius: 6 } : {}

  const renderSegment = (leaf: Extract<SegmentTreeNode<Segment>, { kind: 'segment' }>) => {
    const src = { type: 'item', id: leaf.segment.id } as const
    return (
      <span
        key={leaf.key}
        style={overStyle(leaf.key)}
        {...dnd.dragProps(src)}
        {...dnd.dropProps({ type: 'before-item', id: leaf.segment.id }, leaf.key)}
        {...dnd.keyProps(src, folders.startMove)}
      >
        <SegmentButton
          segment={leaf.segment}
          label={leaf.label}
          workspaceId={workspaceId}
          isSelected={selectedSet.has(leaf.segment.id)}
          totalContacts={totalContacts}
          onToggle={() => onSegmentToggle(leaf.segment.id)}
          onDelete={(segmentId) => deleteSegmentMutation.mutate(segmentId)}
          onRebuild={(segmentId) => rebuildSegmentMutation.mutate(segmentId)}
          onMove={nested ? () => folders.startMove(src) : undefined}
        />
      </span>
    )
  }

  const renderFolderButton = (folderNode: SegmentFolderNode<Segment>) => {
    const open = isOpen(folderNode)
    const selectedInside = segmentsIn(folderNode.children).filter((s) => selectedSet.has(s.id)).length
    const src = { type: 'folder', id: folderNode.folderId ?? folderNode.key } as const
    const dropTarget = (e: React.DragEvent<HTMLElement>) => {
      // Bord gauche du dossier = avant lui (ordre) ; le reste = dedans.
      const box = e.currentTarget.getBoundingClientRect()
      return e.clientX < box.left + box.width * 0.2
        ? ({ type: 'before-folder', id: src.id } as const)
        : ({ type: 'folder', id: src.id } as const)
    }
    return (
      <span
        key={folderNode.key}
        data-folder-id={folderNode.folderId}
        style={{ whiteSpace: 'nowrap', opacity: folderNode.count === 0 && !searching ? 0.55 : 1, ...overStyle(folderNode.key) }}
        {...(folderNode.folderId ? dnd.dragProps(src) : {})}
        {...(folderNode.folderId ? dnd.dropProps(dropTarget, folderNode.key) : {})}
        {...(folderNode.folderId ? dnd.keyProps(src, folders.startMove) : {})}
      >
        <Button
          size="small"
          type={selectedInside > 0 ? 'primary' : 'default'}
          ghost={selectedInside > 0}
          icon={open ? <FolderOpenOutlined /> : <FolderOutlined />}
          onClick={() => folders.toggle(folderNode, withSelection.has(folderNode.key))}
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
        {folderNode.folderId && (
          <FolderMenu node={folderNode as SegmentFolderNode<{ id: string; name: string }>} {...folders.menu(folderNode)} />
        )}
      </span>
    )
  }

  // Un niveau : dossiers et segments sur une même ligne, puis le contenu des dossiers ouverts.
  const renderLevel = (nodes: SegmentTreeNode<Segment>[], trailing?: React.ReactNode) => {
    const levelFolders = nodes.filter(
      (node): node is SegmentFolderNode<Segment> => node.kind === 'folder'
    )
    const leaves = nodes.filter(
      (node): node is Extract<SegmentTreeNode<Segment>, { kind: 'segment' }> => node.kind === 'segment'
    )
    return (
      <div className="flex flex-col gap-2 min-w-0">
        <Space wrap>
          {levelFolders.map(renderFolderButton)}
          {leaves.map(renderSegment)}
          {trailing}
        </Space>
        {levelFolders
          .filter((folderNode) => isOpen(folderNode) && folderNode.children.length > 0)
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

  // Sans dossier : même rendu qu'avant (une seule rangée de segments), plus le bouton « Nouveau dossier ».
  if (!nested) {
    return (
      <div className="flex items-center gap-2 mb-6">
        <div className="text-sm font-medium">{t`Segments`}:</div>
        <Space wrap>
          {segments.map((segment: Segment) => renderSegment({ kind: 'segment', key: `segment:${segment.id}`, label: segment.name, segment }))}
          {upsertButton}
          <Button size="small" icon={<FolderAddOutlined />} onClick={folders.newFolder}>
            {t`New folder`}
          </Button>
        </Space>
        {folders.dialogs}
      </div>
    )
  }

  const rootDrop = dnd.dragging ? (
    <span
      style={{ border: '1px dashed #bfbfbf', borderRadius: 6, padding: '0 8px', fontSize: 12, ...overStyle('__root__') }}
      {...dnd.dropProps({ type: 'folder', id: null }, '__root__')}
    >
      {t`Root (no folder)`}
    </span>
  ) : null

  return (
    <div className="flex items-start gap-2 mb-6">
      <div className="text-sm font-medium pt-1">{t`Segments`}:</div>
      <div className="flex flex-col gap-2 min-w-0 flex-1">
        <div>{folders.toolbar(rootDrop)}</div>
        {renderLevel(visibleTree, upsertButton)}
      </div>
      {folders.dialogs}
    </div>
  )
}
