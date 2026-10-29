import { beforeEach, describe, expect, it } from 'vitest'
import { pruneOpenState, readOpenState, writeOpenState } from './veridian_folder_store'

beforeEach(() => window.localStorage.clear())

describe('pli mémorisé', () => {
  it('relit ce qui a été écrit, par workspace et par genre', () => {
    writeOpenState('ws', 'segments', { a: false, b: true })
    expect(readOpenState('ws', 'segments')).toEqual({ a: false, b: true })
    expect(readOpenState('ws', 'lists')).toEqual({})
    expect(readOpenState('autre', 'segments')).toEqual({})
  })
  it('ignore un contenu corrompu ou de mauvais type', () => {
    window.localStorage.setItem('veridian:folders:open:ws:segments', '{oups')
    expect(readOpenState('ws', 'segments')).toEqual({})
    window.localStorage.setItem('veridian:folders:open:ws:segments', '[1,2]')
    expect(readOpenState('ws', 'segments')).toEqual({})
    window.localStorage.setItem('veridian:folders:open:ws:segments', '{"a":true,"b":"oui","c":1}')
    expect(readOpenState('ws', 'segments')).toEqual({ a: true })
  })
  it('ne casse pas quand le stockage refuse l ecriture ou la lecture', () => {
    const real = window.localStorage
    const broken = { getItem: () => { throw new Error('bloqué') }, setItem: () => { throw new Error('quota') } }
    Object.defineProperty(window, 'localStorage', { value: broken, configurable: true })
    try {
      expect(() => writeOpenState('ws', 'segments', { a: true })).not.toThrow()
      expect(readOpenState('ws', 'segments')).toEqual({})
    } finally {
      Object.defineProperty(window, 'localStorage', { value: real, configurable: true })
    }
  })
  it('oublie les dossiers supprimés', () => {
    expect(pruneOpenState({ a: true, b: false, c: true }, ['a', 'c'])).toEqual({ a: true, c: true })
  })
})
