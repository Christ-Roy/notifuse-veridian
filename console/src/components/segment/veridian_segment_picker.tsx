import { useMemo } from 'react'
import { Select, Tag, TreeSelect } from 'antd'
import { hasFolders, toTreeSelectData } from './veridian_segment_folders'
import { buildNativeTree, withoutEmptyFolders } from '../common/veridian_folder_native'
import { useNativeFolders } from '../common/veridian_folder_hooks'

export interface PickableSegment {
  id: string
  name: string
  color: string
  users_count?: number
}

interface VeridianSegmentPickerProps {
  segments: PickableSegment[]
  value?: string | string[]
  onChange?: (value: string | string[]) => void
  multiple?: boolean
  placeholder?: string
  loading?: boolean
  // Dossiers natifs (lot L2) : sans workspace, l'arbre se déduit des noms « A / B ».
  workspaceId?: string
  formatNumber: (n: number) => string
}

// Sélecteur de segments. Sans nom « Dossier / Segment » : Select d'origine, rendu inchangé.
// Avec dossiers : TreeSelect arborescent (dossiers non sélectionnables).
export function VeridianSegmentPicker({
  segments,
  value,
  onChange,
  multiple = false,
  placeholder,
  loading,
  workspaceId,
  formatNumber
}: VeridianSegmentPickerProps) {
  const { native } = useNativeFolders(workspaceId, 'segments', segments)
  // Un sélecteur n'a que faire des dossiers vides.
  const tree = useMemo(() => withoutEmptyFolders(buildNativeTree(segments, native)), [segments, native])
  const nested = hasFolders(tree)

  const colorTag = (segment: PickableSegment, props?: { closable?: boolean; onClose?: () => void }) => (
    <Tag
      color={segment.color}
      bordered={false}
      closable={props?.closable}
      onClose={props?.onClose}
      style={{ marginRight: 3 }}
    >
      {segment.name}
      {segment.users_count !== undefined && (
        <span className="ml-1">({formatNumber(segment.users_count)})</span>
      )}
    </Tag>
  )

  const tagRender = multiple
    ? (props: { label: unknown; value: unknown; closable: boolean; onClose: () => void }) => {
        const segment = segments.find((s) => s.id === props.value)
        if (!segment) return <Tag>{props.label as string}</Tag>
        return colorTag(segment, props)
      }
    : undefined

  if (nested) {
    return (
      <TreeSelect
        multiple={multiple}
        showSearch
        treeNodeFilterProp="title"
        placeholder={placeholder}
        value={value}
        onChange={onChange}
        loading={loading}
        style={multiple ? undefined : { width: '100%' }}
        treeData={toTreeSelectData(tree)}
        treeTitleRender={(node) => {
          const segment = segments.find((s) => s.id === node.value)
          if (!segment || !multiple) return node.title
          return (
            <Tag color={segment.color} bordered={false}>
              {node.title}
              {segment.users_count !== undefined && (
                <span className="ml-1">({formatNumber(segment.users_count)})</span>
              )}
            </Tag>
          )
        }}
        tagRender={tagRender}
      />
    )
  }

  return (
    <Select
      mode={multiple ? 'multiple' : undefined}
      placeholder={placeholder}
      value={value}
      onChange={onChange}
      loading={loading}
      style={multiple ? undefined : { width: '100%' }}
      options={segments.map((segment) => ({ value: segment.id, label: segment.name }))}
      optionRender={
        multiple
          ? (option) => {
              const segment = segments.find((s) => s.id === option.value)
              if (!segment) return option.label
              return (
                <Tag color={segment.color} bordered={false}>
                  {segment.name}
                  {segment.users_count !== undefined && (
                    <span className="ml-1">({formatNumber(segment.users_count)})</span>
                  )}
                </Tag>
              )
            }
          : undefined
      }
      tagRender={tagRender}
    />
  )
}
