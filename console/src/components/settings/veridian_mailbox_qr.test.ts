import { describe, it, expect } from 'vitest'
import { qrByteCapacity, qrMatrix } from './veridian_mailbox_qr'

// Le décodage de bout en bout (jsQR 1.4.0, versions 1 à 10, 5 à 213 octets) a été vu réussir hors dépôt ;
// ici : géométrie, motifs fixes, information de format valide et empreinte figée (garde de régression).

const bit = (m: boolean[][], x: number, y: number) => (m[y][x] ? 1 : 0)

function formatBitsAt(m: boolean[][]): number {
  // première copie, autour du motif de repérage en haut à gauche (ordre ISO 18004)
  const pos: Array<[number, number]> = [
    [8, 0], [8, 1], [8, 2], [8, 3], [8, 4], [8, 5], [8, 7], [8, 8], [7, 8], [5, 8], [4, 8], [3, 8], [2, 8], [1, 8], [0, 8]
  ]
  let v = 0
  pos.forEach(([x, y], i) => {
    v |= bit(m, x, y) << i
  })
  return v
}

function bchOk(bits: number): boolean {
  const data = (bits ^ 0x5412) >>> 10
  let rem = data
  for (let i = 0; i < 10; i++) rem = (rem << 1) ^ ((rem >>> 9) * 0x537)
  return (((data << 10) | rem) ^ 0x5412) === bits
}

describe('qrMatrix', () => {
  it('choisit la plus petite version : 21 modules pour un texte court, 57 pour 213 octets', () => {
    expect(qrMatrix('HELLO').length).toBe(21)
    expect(qrMatrix('x'.repeat(213)).length).toBe(57)
    expect(qrByteCapacity(1)).toBe(14)
  })

  it('refuse un texte trop long au lieu de produire un code faux', () => {
    expect(() => qrMatrix('x'.repeat(214))).toThrow(RangeError)
  })

  it('pose les trois motifs de repérage, le module sombre et une information de format valide', () => {
    const url = 'https://notifuse.example/p/aZ9-_aZ9-_aZ9-_aZ9-_aZ9-_'
    const m = qrMatrix(url)
    const n = m.length
    for (const [ox, oy] of [[0, 0], [n - 7, 0], [0, n - 7]]) {
      for (let i = 0; i < 7; i++) {
        expect(m[oy][ox + i] && m[oy + 6][ox + i] && m[oy + i][ox] && m[oy + i][ox + 6]).toBe(true)
      }
      expect(m[oy + 3][ox + 3]).toBe(true)
      expect(m[oy + 1][ox + 1]).toBe(false)
    }
    expect(m[n - 8][8]).toBe(true)
    expect(bchOk(formatBitsAt(m))).toBe(true)
  })

  it('est déterministe, et change quand le contenu change', () => {
    expect(qrMatrix('abc')).toEqual(qrMatrix('abc'))
    expect(qrMatrix('abc')).not.toEqual(qrMatrix('abd'))
  })

  it('empreinte figée (décodée par jsQR lors de la mise au point)', () => {
    const m = qrMatrix('https://notifuse.example/api/veridian/p/aZ9-_aZ9-_aZ9-_aZ9-_')
    const flat = m.map((r) => r.map((c) => (c ? '1' : '0')).join('')).join('')
    let h = 0
    for (let i = 0; i < flat.length; i++) h = (Math.imul(h, 31) + flat.charCodeAt(i)) | 0
    expect([m.length, h]).toEqual(GOLDEN)
  })
})

const GOLDEN = [33, -596930532]
