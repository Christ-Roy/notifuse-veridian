import { describe, it, expect } from 'vitest'
import { readdirSync, readFileSync, statSync } from 'fs'
import { join, relative } from 'path'

// Garde de CLASSE (checklist sécurité S2, défaut bloquant du rapport UX du 27/10) :
//  1. toute <iframe srcDoc=...> porte un attribut sandbox, et jamais allow-scripts ;
//  2. tout dangerouslySetInnerHTML passe par veridianSanitizeHtml.
// Un aperçu monté sans bac à sable exécute le contenu d'un modèle (ou les données
// d'un contact qu'il a rendues) dans l'origine de la console, où vit le jeton.

const SRC = join(__dirname, '..')

function walk(dir: string, out: string[] = []): string[] {
  for (const e of readdirSync(dir)) {
    const p = join(dir, e)
    if (statSync(p).isDirectory()) walk(p, out)
    else if (/\.tsx$/.test(e) && !/\.test\.tsx$/.test(e)) out.push(p)
  }
  return out
}

/** Extrait chaque balise <iframe ...> en suivant les accolades JSX (un `>` dans {...} ne ferme pas la balise). */
export function extractIframeTags(source: string): string[] {
  const tags: string[] = []
  let i = source.indexOf('<iframe')
  while (i !== -1) {
    let depth = 0
    let quote: string | null = null
    let j = i + 7
    for (; j < source.length; j++) {
      const c = source[j]
      if (quote) {
        if (c === quote) quote = null
        continue
      }
      if (depth === 0 && (c === '"' || c === "'")) quote = c
      else if (c === '{') depth++
      else if (c === '}') depth--
      else if (c === '>' && depth === 0) break
    }
    tags.push(source.slice(i, j + 1))
    i = source.indexOf('<iframe', j)
  }
  return tags
}

describe('garde iframe / HTML injecté (S2)', () => {
  const files = walk(SRC)

  it('l\'extracteur voit les iframes connues (autotest : une garde qui ne trouve rien ne prouve rien)', () => {
    const n = files.reduce((acc, f) => acc + extractIframeTags(readFileSync(f, 'utf8')).length, 0)
    expect(n).toBeGreaterThanOrEqual(5)
  })

  it('l\'extracteur traverse un > dans une expression JSX', () => {
    const tags = extractIframeTags('<iframe srcDoc={a ? `<p>x</p>` : b} sandbox="" />')
    expect(tags).toHaveLength(1)
    expect(tags[0]).toContain('sandbox=""')
  })

  it('aucune iframe srcDoc sans sandbox, aucune avec allow-scripts', () => {
    const fautifs: string[] = []
    for (const f of files) {
      for (const tag of extractIframeTags(readFileSync(f, 'utf8'))) {
        if (!/srcDoc/.test(tag)) continue
        const m = tag.match(/sandbox=(?:"([^"]*)"|\{[^}]*\})/)
        if (!m) fautifs.push(`${relative(SRC, f)} : iframe srcDoc sans sandbox`)
        else if (/allow-scripts/.test(m[0])) fautifs.push(`${relative(SRC, f)} : sandbox avec allow-scripts`)
      }
    }
    expect(fautifs).toEqual([])
  })

  it('tout dangerouslySetInnerHTML est assaini', () => {
    const fautifs: string[] = []
    for (const f of files) {
      const src = readFileSync(f, 'utf8')
      for (const m of src.matchAll(/dangerouslySetInnerHTML=\{\{([^}]*)\}\}/g)) {
        if (!/veridianSanitizeHtml\(/.test(m[1])) fautifs.push(`${relative(SRC, f)} : ${m[0]}`)
      }
    }
    expect(fautifs).toEqual([])
  })
})
