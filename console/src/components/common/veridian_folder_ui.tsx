// Éléments d'interface des dossiers natifs (lot L2) : saisie d'un nom, « Déplacer vers… » au clavier,
// menu d'un dossier, barre d'outils (recherche, nouveau dossier, tout ouvrir / fermer).
import { useMemo, useRef, useState } from 'react'
import { Button, Dropdown, Input, Modal, Space, Tree, type TreeDataNode } from 'antd'
import { FolderAddOutlined, MoreOutlined, SearchOutlined } from '@ant-design/icons'
import { useLingui } from '@lingui/react/macro'
import type { SegmentFolderNode, SegmentLike, SegmentTreeNode } from '../segment/veridian_segment_folders'
import { forbiddenDestinations, type DragSource } from './veridian_folder_moves'
import { folderNameError } from './veridian_folder_native'

interface PromptProps {
  open: boolean
  title: string
  initial?: string
  okText: string
  onOk: (name: string) => void
  onCancel: () => void
}

// La clé remonte le formulaire à chaque ouverture : le champ repart toujours de la valeur proposée.
export function FolderNamePrompt(props: PromptProps) {
  return <FolderNameForm key={`${props.open}:${props.initial ?? ''}`} {...props} />
}

function FolderNameForm({ open, title, initial = '', okText, onOk, onCancel }: PromptProps) {
  const { t } = useLingui()
  const [value, setValue] = useState(initial)
  const problem = folderNameError(value)
  const submit = () => {
    if (!problem) onOk(value.trim())
  }
  return (
    <Modal open={open} title={title} okText={okText} cancelText={t`Cancel`} onOk={submit} onCancel={onCancel} okButtonProps={{ disabled: !!problem }} destroyOnClose>
      <Input
        autoFocus
        value={value}
        maxLength={120}
        aria-label={t`Folder name`}
        placeholder={t`Folder name`}
        status={value !== '' && problem === 'separator' ? 'error' : undefined}
        onChange={(e) => setValue(e.target.value)}
        onPressEnter={submit}
      />
      {problem === 'separator' && (
        <div role="alert" style={{ color: '#ff4d4f', marginTop: 8 }}>
          {t`A name cannot contain " / ": create sub-folders instead.`}
        </div>
      )}
    </Modal>
  )
}

const ROOT = '__root__'

function toTreeData<T extends SegmentLike>(nodes: SegmentTreeNode<T>[], forbidden: Set<string>): TreeDataNode[] {
  return nodes
    .filter((n): n is SegmentFolderNode<T> => n.kind === 'folder' && !!n.folderId)
    .map((n) => ({
      key: n.folderId as string,
      title: `${n.label} (${n.count})`,
      disabled: forbidden.has(n.folderId as string),
      children: toTreeData(n.children, forbidden)
    }))
}

interface PickerProps<T extends SegmentLike> {
  source: DragSource | null
  tree: SegmentTreeNode<T>[]
  sourceLabel: string
  onPick: (folderId: string | null) => void
  onCancel: () => void
}

// « Déplacer vers… » : l'alternative au clavier du glisser-déposer. Flèches pour parcourir
// l'arbre, Entrée pour choisir, Échap pour annuler.
export function FolderMovePicker<T extends SegmentLike>({ source, tree, sourceLabel, onPick, onCancel }: PickerProps<T>) {
  const { t } = useLingui()
  const holder = useRef<HTMLDivElement>(null)
  const data = useMemo<TreeDataNode[]>(() => {
    if (!source) return []
    const forbidden = forbiddenDestinations(tree, source)
    return [{ key: ROOT, title: t`Root (no folder)`, children: toTreeData(tree, forbidden) }]
  }, [source, tree, t])
  return (
    <Modal
      open={!!source}
      title={t`Move "${sourceLabel}" to…`}
      footer={null}
      onCancel={onCancel}
      destroyOnClose
      afterOpenChange={(opened) => {
        // Le clavier arrive directement dans l'arbre (flèches, Entrée).
        if (opened) holder.current?.querySelector<HTMLElement>('.ant-tree-focused, input, [role="tree"]')?.focus()
      }}
    >
      <div ref={holder} role="group" aria-label={t`Destination folder`}>
      <Tree
        showLine
        defaultExpandAll
        treeData={data}
        selectedKeys={[]}
        onSelect={(keys) => {
          const key = keys[0]
          if (key === undefined) return
          onPick(key === ROOT ? null : String(key))
        }}
      />
      </div>
    </Modal>
  )
}

interface MenuProps {
  node: SegmentFolderNode<SegmentLike>
  canWrite: boolean
  onRename: () => void
  onCreateSub: () => void
  onMove: () => void
  onDelete: () => void
}

export function FolderMenu({ node, canWrite, onRename, onCreateSub, onMove, onDelete }: MenuProps) {
  const { t } = useLingui()
  if (!canWrite) return null
  return (
    <Dropdown
      trigger={['click']}
      menu={{
        items: [
          { key: 'rename', label: t`Rename`, onClick: onRename },
          { key: 'sub', label: t`New sub-folder`, onClick: onCreateSub },
          { key: 'move', label: t`Move to…`, onClick: onMove },
          { key: 'delete', danger: true, label: t`Delete folder`, onClick: onDelete }
        ]
      }}
    >
      <Button
        type="text"
        size="small"
        icon={<MoreOutlined />}
        aria-label={t`Folder actions for ${node.label}`}
        style={{ marginLeft: 2 }}
      />
    </Dropdown>
  )
}

interface ToolbarProps {
  query: string
  onQuery: (q: string) => void
  canWrite: boolean
  onNewFolder: () => void
  onExpandAll: () => void
  onCollapseAll: () => void
  announcement: string
  // Zone de dépôt « racine », visible pendant un glisser.
  rootDrop?: React.ReactNode
  hasFolders: boolean
}

export function FolderToolbar({ query, onQuery, canWrite, onNewFolder, onExpandAll, onCollapseAll, announcement, rootDrop, hasFolders }: ToolbarProps) {
  const { t } = useLingui()
  return (
    <Space wrap size={8} style={{ marginBottom: 8 }}>
      <Input
        allowClear
        size="small"
        prefix={<SearchOutlined />}
        placeholder={t`Search`}
        aria-label={t`Search`}
        value={query}
        onChange={(e) => onQuery(e.target.value)}
        style={{ width: 240 }}
      />
      {canWrite && (
        <Button size="small" icon={<FolderAddOutlined />} onClick={onNewFolder}>
          {t`New folder`}
        </Button>
      )}
      {hasFolders && (
        <>
          <Button size="small" type="link" onClick={onExpandAll}>
            {t`Expand all`}
          </Button>
          <Button size="small" type="link" onClick={onCollapseAll}>
            {t`Collapse all`}
          </Button>
        </>
      )}
      {rootDrop}
      <span role="status" aria-live="polite" style={{ position: 'absolute', width: 1, height: 1, overflow: 'hidden', clip: 'rect(0 0 0 0)' }}>
        {announcement}
      </span>
    </Space>
  )
}
