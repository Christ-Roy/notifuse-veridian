import type { TableColumnType } from 'antd'
import type { FolderRow, NamedItem, TreeRow } from './veridian_folder_rows'
import { FolderToggleButton } from './veridian_folder_tree'

export const FOLDER_INDENT_PX = 20

type DataIndex = string | number | readonly (string | number)[] | undefined

function readValue(item: object, dataIndex: DataIndex): unknown {
  if (dataIndex === undefined) return undefined
  const path = Array.isArray(dataIndex) ? dataIndex : [dataIndex]
  return path.reduce<unknown>(
    (current, part) =>
      current !== null && typeof current === 'object'
        ? (current as Record<string | number, unknown>)[part]
        : undefined,
    item
  )
}

// Adapte les colonnes d'un tableau d'éléments à un tableau de lignes dossier / élément.
// - ligne dossier : le bouton occupe toute la largeur (première colonne étendue) ;
// - ligne élément : les colonnes d'origine, rendues sur l'élément d'origine (actions inchangées) ;
//   la colonne `labelColumnKey` affiche le nom court, indenté selon la profondeur.
export function folderTableColumns<T extends NamedItem>(
  columns: TableColumnType<T>[],
  labelColumnKey: string,
  onToggle: (key: string) => void,
  folderExtra?: (row: FolderRow) => React.ReactNode
): TableColumnType<TreeRow<T>>[] {
  return columns.map((column, index): TableColumnType<TreeRow<T>> => {
    const { render, onCell, dataIndex, ...passThrough } = column
    // Seuls titre, clé, largeur, alignement... sont repris ; aucun tri ni filtre n'existe sur ces tableaux.
    const rest = passThrough as unknown as Partial<TableColumnType<TreeRow<T>>>
    const isFirst = index === 0
    return {
      ...rest,
      dataIndex: undefined,
      onCell: (row) => {
        if (row.kind === 'folder') {
          return {
            colSpan: isFirst ? columns.length : 0,
            style: { background: 'rgba(0, 0, 0, 0.02)' }
          }
        }
        return onCell ? onCell(row.item, 0) : {}
      },
      render: (_value, row, rowIndex) => {
        if (row.kind === 'folder') {
          return isFirst ? (
            <div style={{ paddingLeft: row.depth * FOLDER_INDENT_PX }}>
              <FolderToggleButton row={row} onToggle={onToggle} extra={folderExtra?.(row)} />
            </div>
          ) : null
        }
        const raw = readValue(row.item, dataIndex as DataIndex)
        const value = column.key === labelColumnKey ? row.label : raw
        const content = (render ? render(value, row.item, rowIndex) : value) as React.ReactNode
        if (column.key !== labelColumnKey) return content
        return <div style={{ paddingLeft: row.depth * FOLDER_INDENT_PX }}>{content}</div>
      }
    }
  })
}
