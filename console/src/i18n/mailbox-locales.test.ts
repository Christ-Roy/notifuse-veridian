import { describe, it, expect } from 'vitest'
import { readFileSync } from 'node:fs'
import { resolve } from 'node:path'

// Écran « Mailbox access » (fiche 84, règle 16) : chaque chaîne des fichiers veridian_mailbox_*
// a un msgstr NON VIDE dans les 8 catalogues, les variables {x} du message source sont conservées,
// et aucun texte ne contient le tiret cadratin entouré d'espaces.

interface Entry {
  refs: string[]
  msgid: string
  msgstr: string
}

function parsePo(text: string): Entry[] {
  const entries: Entry[] = []
  for (const block of text.split(/\r?\n\r?\n/)) {
    const lines = block.split(/\r?\n/)
    if (lines.some((l) => l.startsWith('#~'))) continue
    const e: Entry = { refs: [], msgid: '', msgstr: '' }
    let mode: 'id' | 'str' | '' = ''
    for (const l of lines) {
      if (l.startsWith('#:')) e.refs.push(l.slice(2).trim())
      else if (l.startsWith('msgid_plural')) mode = ''
      else if (l.startsWith('msgid ')) {
        e.msgid = JSON.parse(l.slice(6))
        mode = 'id'
      } else if (l.startsWith('msgstr')) {
        const m = /^msgstr(?:\[\d+\])? (".*")$/.exec(l)
        if (m) e.msgstr += JSON.parse(m[1])
        mode = 'str'
      } else if (l.startsWith('"')) {
        if (mode === 'id') e.msgid += JSON.parse(l)
        else if (mode === 'str') e.msgstr += JSON.parse(l)
      }
    }
    entries.push(e)
  }
  return entries
}

const LOCALES = ['en', 'fr', 'es', 'de', 'ca', 'pt-BR', 'ja', 'it']
const MINE = /veridian_mailbox_[A-Za-z0-9_]+\.tsx?/
const vars = (s: string) => (s.match(/\{[A-Za-z0-9_]+\}/g) ?? []).sort()

describe('catalogues de l’écran Mailbox access', () => {
  it('le détecteur voit une clé vide (épreuve)', () => {
    const sample = parsePo('#: src/x/veridian_mailbox_a.tsx\nmsgid "Hello"\nmsgstr ""\n\n#: src/y.tsx\nmsgid "Ok"\nmsgstr "Bien"\n')
    expect(sample.filter((e) => e.msgstr === '').map((e) => e.msgid)).toEqual(['Hello'])
  })

  for (const locale of LOCALES) {
    describe(locale, () => {
      const all = parsePo(readFileSync(resolve(__dirname, `locales/${locale}.po`), 'utf8'))
      const mine = all.filter((e) => e.msgid && e.refs.some((r) => MINE.test(r.split(':')[0])))

      it('couvre bien l’écran (la portée n’est pas vide)', () => {
        expect(mine.length).toBeGreaterThan(150)
        expect(mine.some((e) => e.msgid === 'Mailbox access')).toBe(true)
      })

      it('aucun msgstr vide', () => {
        expect(mine.filter((e) => e.msgstr.trim() === '').map((e) => e.msgid)).toEqual([])
      })

      it('garde les variables du message source', () => {
        for (const e of mine) expect(vars(e.msgstr), e.msgid).toEqual(vars(e.msgid))
      })

      it('aucun tiret cadratin entouré d’espaces', () => {
        for (const e of mine) expect(e.msgstr, e.msgid).not.toMatch(/ — /)
      })
    })
  }

  it('le français est soigné sur les textes clés', () => {
    const fr = new Map(
      parsePo(readFileSync(resolve(__dirname, 'locales/fr.po'), 'utf8')).map((e) => [e.msgid, e.msgstr] as const)
    )
    expect(fr.get('Choose SSL/TLS instead of STARTTLS or None.')).toBe('Choisissez SSL/TLS à la place de STARTTLS ou Aucune.')
    expect(fr.get('Set up my app')).toBe('Régler mon application')
    expect(fr.get('This password is shown only once.')).toBe('Ce mot de passe n\'est affiché qu\'une seule fois.')
  })
})
