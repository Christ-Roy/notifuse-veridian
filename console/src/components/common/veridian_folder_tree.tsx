import { Button } from 'antd'
import { FolderOpenOutlined, FolderOutlined, DownOutlined, RightOutlined } from '@ant-design/icons'
import { useLingui } from '@lingui/react/macro'
import type { FolderRow } from './veridian_folder_rows'

interface FolderToggleButtonProps {
  row: FolderRow
  onToggle: (key: string) => void
  controlsId?: string
}

// Bouton natif : Entrée et Espace fonctionnent sans code supplémentaire.
export function FolderToggleButton({ row, onToggle, controlsId }: FolderToggleButtonProps) {
  const { t } = useLingui()
  const countLabel = row.count === 1 ? t`1 item` : t`${row.count} items`
  return (
    <Button
      type="text"
      size="small"
      icon={row.open ? <FolderOpenOutlined /> : <FolderOutlined />}
      onClick={() => onToggle(row.key)}
      aria-expanded={row.open}
      aria-controls={row.open ? controlsId : undefined}
      style={{ fontWeight: 500, maxWidth: '100%' }}
    >
      <span>{row.label}</span>
      <span style={{ opacity: 0.75, marginLeft: 6, fontWeight: 400 }}>{countLabel}</span>
      {row.open ? (
        <DownOutlined style={{ fontSize: 10, marginLeft: 6 }} />
      ) : (
        <RightOutlined style={{ fontSize: 10, marginLeft: 6 }} />
      )}
    </Button>
  )
}
