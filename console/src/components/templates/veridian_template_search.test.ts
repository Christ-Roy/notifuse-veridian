import { describe, it, expect } from 'vitest'
import { filterTemplatesByName } from './veridian_template_search'

const modeles = [
  { id: '1', name: 'Bienvenue' },
  { id: '2', name: 'Relance panier' },
  { id: '3', name: 'Newsletter Mars' }
]

describe('filterTemplatesByName', () => {
  it('rend la liste intacte quand la recherche est vide ou blanche', () => {
    expect(filterTemplatesByName(modeles, '')).toBe(modeles)
    expect(filterTemplatesByName(modeles, '   ')).toBe(modeles)
  })

  it('filtre par sous-chaîne, sans tenir compte de la casse', () => {
    expect(filterTemplatesByName(modeles, 'RELANCE').map((m) => m.id)).toEqual(['2'])
    expect(filterTemplatesByName(modeles, 'mar').map((m) => m.id)).toEqual(['3'])
  })

  it('ignore les espaces de bord de la requête', () => {
    expect(filterTemplatesByName(modeles, '  bienvenue  ').map((m) => m.id)).toEqual(['1'])
  })

  it('rend une liste vide quand rien ne correspond', () => {
    expect(filterTemplatesByName(modeles, 'inexistant')).toEqual([])
  })
})
