import { describe, expect, it, vi } from 'vitest'

const config = vi.fn()
vi.mock('@monaco-editor/react', () => ({ default: () => null, loader: { config: (c: unknown) => config(c) } }))

describe('Monaco servi par la console (VER-265)', () => {
  it("pointe le chargeur sur la copie locale dès l'import, jamais sur un CDN", async () => {
    const mod = await import('./veridian_monaco')
    expect(config).toHaveBeenCalledTimes(1)
    const vs = (config.mock.calls[0][0] as { paths: { vs: string } }).paths.vs
    expect(vs).toBe(mod.VERIDIAN_MONACO_VS)
    expect(vs).toMatch(/\/monaco\/vs$/)
    expect(vs).not.toMatch(/^https?:/)
    expect(mod.Editor).toBe(mod.default)
  })
})
