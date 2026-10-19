import { describe, it, expect } from 'vitest'
import {
  reconcileBatch,
  trimTrailingBlankRows,
  stripServerIndex,
  DUPLICATE_IN_BATCH_ERROR
} from './csvImportReport'

// Cas de l'audit UI 19/10 (E1) : fichier de 4 lignes de donnees, une adresse invalide.
// Le serveur rend les operations invalides D'ABORD, puis les valides.
describe('reconcileBatch', () => {
  const sent = [
    { line: 2, email: 'ok1@example.com' },
    { line: 3, email: 'mauvais-email' },
    { line: 4, email: 'ok2@example.com' }
  ]

  it("attache l'erreur a la ligne de l'adresse invalide, pas a la premiere ligne valide", () => {
    const r = reconcileBatch(sent, [
      { email: 'mauvais-email', action: 'error', error: 'invalid contact at index 1: invalid email' },
      { email: 'ok1@example.com', action: 'create' },
      { email: 'ok2@example.com', action: 'create' }
    ])
    expect(r.succeeded).toBe(2)
    expect(r.failures).toEqual([{ line: 3, email: 'mauvais-email', error: 'invalid email' }])
  })

  it('aucune ligne ne disparait : importees + ignorees = lignes envoyees (doublon dans le lot)', () => {
    const rows = [
      { line: 2, email: 'a@example.com' },
      { line: 3, email: 'b@example.com' },
      { line: 4, email: 'A@example.com' } // meme adresse : le serveur garde la derniere
    ]
    const r = reconcileBatch(rows, [
      { email: 'b@example.com', action: 'create' },
      { email: 'a@example.com', action: 'create' }
    ])
    expect(r.succeeded + r.failures.length).toBe(rows.length)
    expect(r.failures).toEqual([{ line: 2, email: 'a@example.com', error: DUPLICATE_IN_BATCH_ERROR }])
  })

  it("une erreur de lot entier est rapportee ligne par ligne", () => {
    const r = reconcileBatch(sent, [], 'Insufficient permissions')
    expect(r.succeeded).toBe(0)
    expect(r.failures.map((f) => f.line)).toEqual([2, 3, 4])
  })
})

describe('trimTrailingBlankRows / stripServerIndex', () => {
  it("ne compte plus la ligne vide finale comme un contact", () => {
    const rows = [['a@example.com'], ['b@example.com'], [''], ['']]
    expect(trimTrailingBlankRows(rows)).toHaveLength(2)
  })
  it('garde une ligne vide au milieu (elle sera rapportee avec son numero)', () => {
    expect(trimTrailingBlankRows([['a'], [''], ['b']])).toHaveLength(3)
  })
  it("retire l'index technique du serveur", () => {
    expect(stripServerIndex('invalid contact at index 7: email is required')).toBe('email is required')
  })
})
