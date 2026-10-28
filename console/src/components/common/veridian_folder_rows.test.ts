import { describe, expect, it } from 'vitest'
import { buildFolderRows, hasFolderRows } from './veridian_folder_rows'

const item = (id: string, name: string) => ({ id, name })
const none = new Set<string>()

describe('buildFolderRows', () => {
  it('liste plate : exactement les mêmes lignes qu avant, même ordre, aucune ligne de dossier', () => {
    const items = [item('b', 'Zèbre'), item('a', 'Alpha'), item('c', 'Contacts de test (zz-test)')]
    const rows = buildFolderRows(items, none)
    expect(rows.every((row) => row.kind === 'item')).toBe(true)
    expect(rows.map((row) => (row.kind === 'item' ? row.item : null))).toEqual(items)
    expect(rows.map((row) => row.key)).toEqual(['b', 'a', 'c'])
    expect(rows.map((row) => row.label)).toEqual(items.map((i) => i.name))
    expect(rows.every((row) => row.depth === 0)).toBe(true)
    expect(hasFolderRows(rows)).toBe(false)
  })

  it('un slash collé ne crée pas de dossier', () => {
    const rows = buildFolderRows([item('a', 'AC/DC'), item('b', 'Promo 1/2')], none)
    expect(hasFolderRows(rows)).toBe(false)
  })

  it('liste vide : aucune ligne', () => {
    expect(buildFolderRows([], none)).toEqual([])
  })

  it('Dossier / Nom : un dossier avec 2 éléments', () => {
    const rows = buildFolderRows(
      [item('a', '1 Prospection / Séquence froide'), item('b', '1 Prospection / Relance')],
      none
    )
    expect(rows).toHaveLength(3)
    expect(rows[0]).toMatchObject({ kind: 'folder', label: '1 Prospection', count: 2, depth: 0, open: true })
    expect(rows[1]).toMatchObject({ kind: 'item', label: 'Relance', depth: 1 })
    expect(rows[2]).toMatchObject({ kind: 'item', label: 'Séquence froide', depth: 1 })
    expect(hasFolderRows(rows)).toBe(true)
  })

  it('dossiers triés naturellement (10 après 2), éléments de la racine ensuite', () => {
    const rows = buildFolderRows(
      [
        item('r', 'Sans dossier'),
        item('x', '10 Divers / X'),
        item('y', '2 Clients / Y'),
        item('z', '1 Prospection / Z')
      ],
      none
    )
    const labels = rows.map((row) => row.label)
    expect(labels).toEqual(['1 Prospection', 'Z', '2 Clients', 'Y', '10 Divers', 'X', 'Sans dossier'])
  })

  it('dossier replié : ses éléments disparaissent, le compte reste', () => {
    const items = [item('a', 'D / A'), item('b', 'D / B'), item('c', 'E / C')]
    const first = buildFolderRows(items, none)
    const dKey = first[0].key
    const rows = buildFolderRows(items, new Set([dKey]))
    expect(rows.map((row) => row.label)).toEqual(['D', 'E', 'C'])
    expect(rows[0]).toMatchObject({ kind: 'folder', count: 2, open: false })
  })

  it('sous-dossiers : profondeur croissante, replier le parent masque les enfants', () => {
    const items = [item('a', 'Cibles / Animaleries / P1'), item('b', 'Cibles / Animaleries / P2')]
    const open = buildFolderRows(items, none)
    expect(open.map((row) => [row.label, row.depth])).toEqual([
      ['Cibles', 0],
      ['Animaleries', 1],
      ['P1', 2],
      ['P2', 2]
    ])
    const closed = buildFolderRows(items, new Set([open[0].key]))
    expect(closed.map((row) => row.label)).toEqual(['Cibles'])
    expect(closed[0]).toMatchObject({ count: 2, open: false })
  })

  it('ne modifie pas la liste reçue', () => {
    const items = [item('b', 'D / B'), item('a', 'D / A')]
    const copy = [...items]
    buildFolderRows(items, none)
    expect(items).toEqual(copy)
  })
})
