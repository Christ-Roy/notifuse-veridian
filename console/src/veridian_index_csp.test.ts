import { readFileSync } from 'node:fs'
import { resolve } from 'node:path'
import { describe, expect, it } from 'vitest'

// VER-265 : la console est servie sous une CSP « script-src 'self' » SANS 'unsafe-inline' (veridian_csp.go).
// Un <script> inline ou un attribut on* dans index.html serait bloqué en enforce et casserait le démarrage de la console.
const html = readFileSync(resolve(__dirname, '../index.html'), 'utf8')

describe('index.html sous CSP sans inline', () => {
  it("aucun <script> sans src (inline)", () => {
    const scripts = html.match(/<script\b[^>]*>/gi) ?? []
    expect(scripts.length).toBeGreaterThan(0)
    for (const tag of scripts) expect(tag, tag).toMatch(/\ssrc=/i)
  })
  it('aucun gestionnaire on* ni javascript: dans le HTML', () => {
    expect(html).not.toMatch(/\son[a-z]+\s*=/i)
    expect(html).not.toMatch(/javascript:/i)
  })
  it('config.js chargé par une balise src, sans document.write', () => {
    expect(html).toMatch(/<script src="\/config\.js"><\/script>/)
    expect(html).not.toMatch(/document\.write/)
  })
})
