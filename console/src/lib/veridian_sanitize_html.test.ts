import { describe, it, expect } from 'vitest'
import { veridianSanitizeHtml } from './veridian_sanitize_html'

// Charges XSS classiques : chacune doit perdre sa partie exécutable, jamais
// le texte autour. Cible : le HTML d'un bloc texte/bouton monté dans l'éditeur.
const PAYLOADS: Array<[string, string]> = [
  ['gestionnaire onerror', '<img src="x" onerror="window.__xss=1">'],
  ['balise script', 'avant<script>window.__xss=1</script>après'],
  ['URL javascript:', '<a href="javascript:window.__xss=1">cliquer</a>'],
  ['svg onload', '<svg onload="window.__xss=1"><circle r="5"/></svg>'],
  ['iframe srcdoc', '<iframe srcdoc="<script>parent.__xss=1</script>"></iframe>'],
  ['formulaire formaction', '<form><button formaction="javascript:window.__xss=1">x</button></form>'],
  ['onclick en ligne', '<p onclick="window.__xss=1">texte</p>'],
  ['object / embed', '<object data="javascript:window.__xss=1"></object><embed src="javascript:window.__xss=1">']
]

describe('veridianSanitizeHtml', () => {
  it.each(PAYLOADS)('neutralise %s', (_nom, payload) => {
    const out = veridianSanitizeHtml(payload)
    expect(out).not.toMatch(/onerror|onload|onclick|<script|javascript:|srcdoc|formaction|<iframe|<object|<embed/i)
    // Preuve d'exécution : le DOM assaini, monté pour de vrai, ne pose rien.
    const host = document.createElement('div')
    host.innerHTML = out
    document.body.appendChild(host)
    expect((window as unknown as { __xss?: number }).__xss).toBeUndefined()
    host.remove()
  })

  it('garde le texte autour d\'une balise retirée', () => {
    expect(veridianSanitizeHtml('avant<script>x()</script>après')).toContain('avant')
    expect(veridianSanitizeHtml('avant<script>x()</script>après')).toContain('après')
  })

  it('conserve la mise en forme d\'un e-mail et la syntaxe Liquid', () => {
    const html =
      '<p style="color:#cc0000;text-align:center"><strong>Bonjour</strong> {{ contact.first_name }}</p>' +
      '<a href="https://animal-services-distribution.com/offre" target="_blank">Voir l\'offre</a>'
    const out = veridianSanitizeHtml(html)
    expect(out).toContain('<strong>Bonjour</strong>')
    expect(out).toContain('{{ contact.first_name }}')
    expect(out).toContain('color:#cc0000')
    expect(out).toContain('href="https://animal-services-distribution.com/offre"')
  })

  it('renvoie une chaîne vide pour une entrée absente', () => {
    expect(veridianSanitizeHtml(undefined)).toBe('')
    expect(veridianSanitizeHtml(null)).toBe('')
    expect(veridianSanitizeHtml('')).toBe('')
  })
})
