import { describe, expect, it } from 'vitest'

import {
  buildSegmentTree,
  filterSegmentTree,
  folderKeys,
  folderKeysContaining,
  hasFolders,
  parseSegmentPath,
  segmentsIn,
  toTreeSelectData,
  type SegmentFolderNode,
  type SegmentTreeNode
} from './veridian_segment_folders'

const seg = (id: string, name: string) => ({ id, name })

const names = (nodes: SegmentTreeNode<{ id: string; name: string }>[]) =>
  nodes.map((n) => (n.kind === 'folder' ? `[${n.label}]` : n.label))

const folder = (nodes: SegmentTreeNode<{ id: string; name: string }>[], label: string) => {
  const found = nodes.find((n) => n.kind === 'folder' && n.label === label)
  if (!found) throw new Error(`pas de dossier ${label}`)
  return found as SegmentFolderNode<{ id: string; name: string }>
}

describe('parseSegmentPath', () => {
  it('un nom sans séparateur reste un seul niveau', () => {
    expect(parseSegmentPath('Contacts de test (zz-test)')).toEqual(['Contacts de test (zz-test)'])
  })

  it('découpe sur espace slash espace', () => {
    expect(parseSegmentPath('2 Cibles / Animaleries indépendantes / P1')).toEqual([
      '2 Cibles',
      'Animaleries indépendantes',
      'P1'
    ])
  })

  it('ne coupe pas un slash collé sans espace', () => {
    expect(parseSegmentPath('AC/DC')).toEqual(['AC/DC'])
    expect(parseSegmentPath('Taille 1/2')).toEqual(['Taille 1/2'])
  })

  it('replie les séparateurs multiples', () => {
    expect(parseSegmentPath('Cibles / / Animaleries')).toEqual(['Cibles', 'Animaleries'])
    expect(parseSegmentPath('Cibles / / / P1')).toEqual(['Cibles', 'P1'])
  })

  it('ignore les espaces en trop autour du séparateur et aux bords', () => {
    expect(parseSegmentPath('   Cibles   /    Animaleries   ')).toEqual(['Cibles', 'Animaleries'])
  })

  it('ignore un séparateur de bord sans fabriquer de segment vide', () => {
    expect(parseSegmentPath('Cibles /')).toEqual(['Cibles'])
    expect(parseSegmentPath('/ Cibles')).toEqual(['Cibles'])
  })

  it('un nom réduit au séparateur reste tel quel à la racine', () => {
    expect(parseSegmentPath('/')).toEqual(['/'])
  })
})

describe('buildSegmentTree', () => {
  it('une liste sans séparateur donne exactement les segments, dans leur ordre reçu', () => {
    const input = [seg('a', 'Zèbre'), seg('b', 'Alpha'), seg('c', '2 Beta')]
    const tree = buildSegmentTree(input)
    expect(hasFolders(tree)).toBe(false)
    expect(tree.map((n) => n.kind)).toEqual(['segment', 'segment', 'segment'])
    expect(segmentsIn(tree).map((s) => s.id)).toEqual(['a', 'b', 'c'])
    expect(names(tree)).toEqual(['Zèbre', 'Alpha', '2 Beta'])
  })

  it('range les segments sous leur dossier et compte récursivement', () => {
    const tree = buildSegmentTree([
      seg('1', '1 Priorité / P1 · à contacter en premier'),
      seg('2', '2 Cibles / Animaleries indépendantes / P1'),
      seg('3', '2 Cibles / Animaleries indépendantes / P2'),
      seg('4', '2 Cibles / Grossistes')
    ])
    const cibles = folder(tree, '2 Cibles')
    expect(cibles.count).toBe(3)
    expect(folder(cibles.children, 'Animaleries indépendantes').count).toBe(2)
    expect(names(cibles.children)).toEqual(['[Animaleries indépendantes]', 'Grossistes'])
    expect(folder(tree, '1 Priorité').count).toBe(1)
  })

  it('un dossier et un segment homonymes restent deux nœuds distincts', () => {
    const tree = buildSegmentTree([
      seg('x', 'Cibles'),
      seg('y', 'Cibles / Animaleries'),
      seg('z', 'Cibles / Animaleries / Cibles')
    ])
    expect(names(tree)).toEqual(['[Cibles]', 'Cibles'])
    const cibles = folder(tree, 'Cibles')
    expect(names(cibles.children)).toEqual(['[Animaleries]', 'Animaleries'])
    const animaleries = folder(cibles.children, 'Animaleries')
    expect(names(animaleries.children)).toEqual(['Cibles'])
    const keys = folderKeys(tree)
    expect(new Set(keys).size).toBe(keys.length)
  })

  it('trie les dossiers naturellement (1, 2, 10) et les met avant les segments', () => {
    const tree = buildSegmentTree([
      seg('a', '10 Fin / X'),
      seg('b', 'Vrac'),
      seg('c', '2 Milieu / X'),
      seg('d', '1 Début / X'),
      seg('e', 'Autre / X')
    ])
    expect(names(tree)).toEqual(['[1 Début]', '[2 Milieu]', '[10 Fin]', '[Autre]', 'Vrac'])
  })

  it('garde les segments d un même dossier dans leur ordre reçu', () => {
    const tree = buildSegmentTree([seg('a', 'D / Zulu'), seg('b', 'D / Alpha')])
    expect(names(folder(tree, 'D').children)).toEqual(['Zulu', 'Alpha'])
  })

  it('un nom vide ou blanc devient une feuille à la racine sans planter', () => {
    const tree = buildSegmentTree([seg('a', ''), seg('b', '   ')])
    expect(tree.every((n) => n.kind === 'segment')).toBe(true)
  })
})

describe('filterSegmentTree', () => {
  const tree = buildSegmentTree([
    seg('1', '1 Priorité / P1 · à contacter en premier'),
    seg('2', '2 Cibles / Animaleries indépendantes / P1'),
    seg('3', '2 Cibles / Grossistes'),
    seg('4', 'Contacts de test (zz-test)')
  ])

  it('une requête vide rend l arbre intact', () => {
    expect(filterSegmentTree(tree, '   ')).toBe(tree)
  })

  it('ne garde que les dossiers qui contiennent un résultat, avec un compte recalculé', () => {
    const out = filterSegmentTree(tree, 'grossiste')
    expect(names(out)).toEqual(['[2 Cibles]'])
    expect(folder(out, '2 Cibles').count).toBe(1)
    expect(names(folder(out, '2 Cibles').children)).toEqual(['Grossistes'])
  })

  it('ignore la casse et les accents', () => {
    expect(segmentsIn(filterSegmentTree(tree, 'PRIORITE')).map((s) => s.id)).toEqual(['1'])
  })

  it('une requête sans résultat rend une liste vide', () => {
    expect(filterSegmentTree(tree, 'introuvable')).toEqual([])
  })
})

describe('folderKeysContaining', () => {
  it('rend tous les dossiers ancêtres d un segment sélectionné', () => {
    const tree = buildSegmentTree([
      seg('p1', '2 Cibles / Animaleries / P1'),
      seg('g', '2 Cibles / Grossistes'),
      seg('t', 'Test')
    ])
    const keys = folderKeysContaining(tree, new Set(['p1']))
    expect(keys).toEqual([
      folder(tree, '2 Cibles').key,
      folder(folder(tree, '2 Cibles').children, 'Animaleries').key
    ])
  })

  it('rend vide quand la sélection ne touche aucun dossier', () => {
    const tree = buildSegmentTree([seg('t', 'Test')])
    expect(folderKeysContaining(tree, new Set(['t']))).toEqual([])
  })
})

describe('toTreeSelectData', () => {
  it('un dossier n est pas sélectionnable, un segment l est avec son identifiant', () => {
    const data = toTreeSelectData(
      buildSegmentTree([seg('p1', '2 Cibles / Animaleries / P1'), seg('t', 'Test')])
    )
    expect(data).toEqual([
      {
        title: '2 Cibles (1)',
        value: folderKeysOf('2 Cibles / Animaleries / P1')[0],
        selectable: false,
        children: [
          {
            title: 'Animaleries (1)',
            value: folderKeysOf('2 Cibles / Animaleries / P1')[1],
            selectable: false,
            children: [{ title: 'P1', value: 'p1', selectable: true }]
          }
        ]
      },
      { title: 'Test', value: 't', selectable: true }
    ])
  })
})

// Clés attendues des dossiers, dérivées du chemin (même formule que le module).
function folderKeysOf(name: string): string[] {
  const parts = name.split(' / ')
  return parts.slice(0, -1).map((_, i) => `folder:${JSON.stringify(parts.slice(0, i + 1))}`)
}
