import { describe, it, expect, vi } from 'vitest'
import { recoverFromChunkError, isChunkLoadError } from './chunkReload'

// Incident 19/10/2026 : onglet ouvert pendant un deploiement, chunk supprime, console cassee.
function deps(initial: string | null = null) {
  let stored = initial
  return {
    storage: {
      getItem: () => stored,
      setItem: (_k: string, v: string) => {
        stored = v
      }
    },
    reload: vi.fn(),
    showMessage: vi.fn(),
    now: () => 1_000_000
  }
}

describe('recoverFromChunkError', () => {
  it('premiere erreur : recharge une fois', () => {
    const d = deps()
    expect(recoverFromChunkError(d)).toBe('reload')
    expect(d.reload).toHaveBeenCalledTimes(1)
    expect(d.showMessage).not.toHaveBeenCalled()
  })
  it('erreur revenue dans la minute : message, pas de boucle de rechargement', () => {
    const d = deps(String(1_000_000 - 5_000))
    expect(recoverFromChunkError(d)).toBe('message')
    expect(d.reload).not.toHaveBeenCalled()
    expect(d.showMessage).toHaveBeenCalledTimes(1)
  })
  it('garde ancienne (nouveau deploiement plus tard) : recharge de nouveau', () => {
    const d = deps(String(1_000_000 - 120_000))
    expect(recoverFromChunkError(d)).toBe('reload')
  })
  it('stockage indisponible : message, jamais de boucle', () => {
    const d = {
      ...deps(),
      storage: {
        getItem: () => {
          throw new Error('denied')
        },
        setItem: () => {}
      }
    }
    expect(recoverFromChunkError(d)).toBe('message')
    expect(d.reload).not.toHaveBeenCalled()
  })
})

describe('isChunkLoadError', () => {
  it('reconnait les messages Chrome, Firefox et Safari', () => {
    expect(isChunkLoadError(new Error('Failed to fetch dynamically imported module: https://x/console/assets/A-1.js'))).toBe(true)
    expect(isChunkLoadError(new Error('error loading dynamically imported module'))).toBe(true)
    expect(isChunkLoadError(new Error('Importing a module script failed.'))).toBe(true)
    expect(isChunkLoadError(new Error('boom'))).toBe(false)
    expect(isChunkLoadError(undefined)).toBe(false)
  })
})
