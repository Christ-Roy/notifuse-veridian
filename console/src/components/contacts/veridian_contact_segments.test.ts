import { describe, expect, it } from 'vitest'
import { pickContactSegments, type ContactSegmentEntry } from './veridian_contact_segments'

const entry = (segment_id: string, name?: string, extra: Partial<ContactSegmentEntry> = {}) => ({
  segment_id,
  name,
  color: 'blue',
  ...extra
})

const SIX = [
  entry('c2', '2 Cibles / Animaleries / P2'),
  entry('c1', '2 Cibles / Animaleries / P1'),
  entry('p2', '1 Priorité / P2 · plus tard'),
  entry('p1', '1 Priorité / P1 · à contacter en premier'),
  entry('c3', '2 Cibles / Jardineries / P1'),
  entry('c4', '2 Cibles / Jardineries / P3')
]

describe('pickContactSegments', () => {
  it('contact avec 6 segments dans 2 dossiers : 1 Priorité puis 2 Cibles, 4 cachés', () => {
    const { visible, hidden } = pickContactSegments(SIX)
    expect(visible.map((s) => s.segment_id)).toEqual(['p1', 'c1'])
    expect(hidden.map((s) => s.segment_id)).toEqual(['p2', 'c2', 'c3', 'c4'])
  })

  it('le nom court est le dernier élément du chemin, le chemin complet est conservé', () => {
    const { visible } = pickContactSegments(SIX)
    expect(visible[0].shortName).toBe('P1 · à contacter en premier')
    expect(visible[0].name).toBe('1 Priorité / P1 · à contacter en premier')
    expect(visible[1].shortName).toBe('P1')
  })

  it('contact sans dossier : les 2 premiers dans l ordre naturel', () => {
    const { visible, hidden } = pickContactSegments([
      entry('z', 'Zèbre'),
      entry('b', 'Beta'),
      entry('a', 'Alpha')
    ])
    expect(visible.map((s) => s.segment_id)).toEqual(['a', 'b'])
    expect(hidden.map((s) => s.segment_id)).toEqual(['z'])
  })

  it('un seul dossier : le représentant puis les suivants dans l ordre naturel', () => {
    const { visible, hidden } = pickContactSegments([
      entry('c3', '2 Cibles / Z'),
      entry('c1', '2 Cibles / A'),
      entry('c2', '2 Cibles / B')
    ])
    expect(visible.map((s) => s.segment_id)).toEqual(['c1', 'c2'])
    expect(hidden.map((s) => s.segment_id)).toEqual(['c3'])
  })

  it('un seul segment : une pastille, rien de caché', () => {
    const { visible, hidden } = pickContactSegments([entry('a', 'Alpha')])
    expect(visible).toHaveLength(1)
    expect(hidden).toEqual([])
  })

  it('aucun segment ou liste absente : rien', () => {
    expect(pickContactSegments([])).toEqual({ visible: [], hidden: [] })
    expect(pickContactSegments(undefined)).toEqual({ visible: [], hidden: [] })
  })

  it('ordre naturel : 10 vient après 2', () => {
    const { visible, hidden } = pickContactSegments([
      entry('d10', '10 Autres / X'),
      entry('d2', '2 Cibles / X'),
      entry('d1', '1 Priorité / X')
    ])
    expect(visible.map((s) => s.segment_id)).toEqual(['d1', 'd2'])
    expect(hidden.map((s) => s.segment_id)).toEqual(['d10'])
  })

  it('nom absent : repli sur l identifiant, comme avant', () => {
    const { visible } = pickContactSegments([entry('seg_abc')])
    expect(visible[0].name).toBe('seg_abc')
    expect(visible[0].shortName).toBe('seg_abc')
  })

  it('couleur absente : couleur par défaut, date et version conservées', () => {
    const { visible } = pickContactSegments([
      { segment_id: 'a', name: 'A', matched_at: '2026-01-01T00:00:00Z', version: 3 }
    ])
    expect(visible[0].color).toBe('#1890ff')
    expect(visible[0].matched_at).toBe('2026-01-01T00:00:00Z')
    expect(visible[0].version).toBe(3)
  })

  it('ne modifie pas la liste reçue', () => {
    const input = [...SIX]
    pickContactSegments(input)
    expect(input.map((s) => s.segment_id)).toEqual(SIX.map((s) => s.segment_id))
  })
})
