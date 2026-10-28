import { useCallback, useMemo, useState } from 'react'
import { buildFolderRows, hasFolderRows, type NamedItem } from './veridian_folder_rows'

// Dossiers dépliés par défaut : rien n'est caché, le pli est un confort.
export function useFolderRows<T extends NamedItem>(items: T[] | undefined) {
  const [collapsed, setCollapsed] = useState<ReadonlySet<string>>(() => new Set())
  const toggle = useCallback((key: string) => {
    setCollapsed((prev) => {
      const next = new Set(prev)
      if (next.has(key)) next.delete(key)
      else next.add(key)
      return next
    })
  }, [])
  const rows = useMemo(() => buildFolderRows(items ?? [], collapsed), [items, collapsed])
  const nested = useMemo(() => hasFolderRows(rows), [rows])
  return { rows, toggle, nested }
}
