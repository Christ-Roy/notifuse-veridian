import { describe, it, expect } from 'vitest'
import { readFileSync } from 'node:fs'
import { resolve } from 'node:path'
import { compileMessageOrThrow } from '@lingui/message-utils/compileMessage'
import { generateMessageId } from '@lingui/message-utils/generateMessageId'
import { i18n } from '@lingui/core'

// Garde-fou i18n, classe D2 (audit UI 19/10/2026) : un message dont la syntaxe ICU est
// invalide (ex. `{{ url }}` ou `{{ contact.first_name }}` ecrit litteralement) est
// supprime du catalogue compile par `lingui compile` ; l'interface affiche alors son
// hash (`X4YDvc`) a la place du texte, ou rend `{}`. Ce test echoue si un message des
// catalogues maintenus (fr, en) ne compile pas, rend des accolades vides, ou si le
// catalogue fr contient un msgstr vide. Les accolades liquid passent par
// src/i18n/liquid.ts (placeholders nommes).

interface Entry {
  msgid: string
  msgstr: string
}

function parsePo(text: string): Entry[] {
  const entries: Entry[] = []
  let cur: Entry | null = null
  let mode: 'msgid' | 'msgstr' | 'other' = 'other'
  for (const raw of text.split('\n')) {
    const line = raw.replace(/\r$/, '')
    if (line.startsWith('msgid ')) {
      cur = { msgid: JSON.parse(line.slice(6)) as string, msgstr: '' }
      entries.push(cur)
      mode = 'msgid'
    } else if (line.startsWith('msgstr ')) {
      if (cur) cur.msgstr = JSON.parse(line.slice(7)) as string
      mode = 'msgstr'
    } else if (line.startsWith('msgid_plural') || line.startsWith('msgstr[')) {
      mode = 'other'
    } else if (line.startsWith('"') && cur) {
      if (mode === 'msgid') cur.msgid += JSON.parse(line) as string
      else if (mode === 'msgstr') cur.msgstr += JSON.parse(line) as string
    } else if (line.trim() === '') {
      mode = 'other'
    }
  }
  return entries.filter((e) => e.msgid !== '')
}

const HASH = /^[A-Za-z0-9+/]{6}$/

for (const locale of ['fr', 'en']) {
  describe(`catalogue ${locale}`, () => {
    const entries = parsePo(readFileSync(resolve(__dirname, `locales/${locale}.po`), 'utf8'))

    it('chaque message compile en ICU valide et rend un texte, jamais un hash ni {}', () => {
      const bad: string[] = []
      for (const e of entries) {
        const message = e.msgstr || e.msgid
        try {
          const compiled = compileMessageOrThrow(message)
          const id = generateMessageId(e.msgid)
          i18n.load('zz', { [id]: compiled as never })
          i18n.activate('zz')
          const values = new Proxy({}, { get: () => 1 }) as Record<string, unknown>
          const out = i18n._(id, values)
          if (out.includes('{}') || (HASH.test(out) && !HASH.test(message))) {
            bad.push(`${id} rend ${JSON.stringify(out)} : ${message.slice(0, 80)}`)
          }
        } catch (err) {
          bad.push(`${generateMessageId(e.msgid)} ne compile pas : ${message.slice(0, 80)} (${String(err).slice(0, 60)})`)
        }
      }
      i18n.activate('en')
      expect(bad).toEqual([])
    })
  })
}

describe('catalogue fr complet', () => {
  it('aucun msgstr vide', () => {
    const entries = parsePo(readFileSync(resolve(__dirname, 'locales/fr.po'), 'utf8'))
    const missing = entries.filter((e) => e.msgstr === '').map((e) => e.msgid.slice(0, 80))
    expect(missing).toEqual([])
  })
})
