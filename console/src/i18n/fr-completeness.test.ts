import { describe, it, expect } from 'vitest'
import { readFileSync } from 'node:fs'
import { resolve } from 'node:path'

// Garde-fou i18n (console assumée, lots 1 et 3, 07-08/10/2026) : un message de l'écran
// Intégrations (Integrations.tsx, veridian_*.tsx et .ts), de la page Profils d'envoi
// (SendingProfilesPage.tsx, components/sending_profiles/veridian_*) ou de la sidebar
// (WorkspaceLayout.tsx) dont le msgstr français est vide s'affiche en
// anglais ou, pire, vide. Ce test échoue dès qu'une telle clé existe.
// Après avoir ajouté un t`...` dans ces fichiers : `npm run lingui:extract`,
// traduire dans fr.po, puis `npm run lingui:compile`.

const SCOPE = /(^|\/)(Integrations|WorkspaceLayout|SendingProfilesPage|veridian_[A-Za-z0-9_]+)\.tsx?$/

interface PoEntry {
  refs: string[]
  msgid: string
  msgstr: string
}

function unquote(raw: string): string {
  return JSON.parse(raw) as string
}

function parsePo(text: string): PoEntry[] {
  const entries: PoEntry[] = []
  let cur: PoEntry | null = null
  let mode: 'msgid' | 'msgstr' | 'other' = 'other'
  let seenId = false
  for (const line of text.split('\n').map((l) => l.replace(/\r$/, ''))) {
    if (line.startsWith('#:')) {
      if (!cur || seenId) {
        cur = { refs: [], msgid: '', msgstr: '' }
        entries.push(cur)
        seenId = false
      }
      cur.refs.push(line.slice(2).trim())
    } else if (line.startsWith('msgid_plural ')) {
      mode = 'other'
    } else if (line.startsWith('msgid ')) {
      if (!cur || seenId) {
        cur = { refs: [], msgid: '', msgstr: '' }
        entries.push(cur)
      }
      seenId = true
      cur.msgid = unquote(line.slice(6))
      mode = 'msgid'
    } else if (line.startsWith('msgstr')) {
      const m = /^msgstr(\[\d+\])? (".*")$/.exec(line)
      if (cur && m) cur.msgstr += unquote(m[2])
      mode = 'msgstr'
    } else if (line.startsWith('"')) {
      if (cur && mode === 'msgid') cur.msgid += unquote(line)
      else if (cur && mode === 'msgstr') cur.msgstr += unquote(line)
    } else if (line.trim() === '') {
      cur = null
      seenId = false
      mode = 'other'
    }
  }
  return entries
}

describe('complétude du catalogue français (écran Intégrations et sidebar)', () => {
  const entries = parsePo(readFileSync(resolve(__dirname, 'locales/fr.po'), 'utf8'))

  it('lit un catalogue non vide (le test sait lire fr.po)', () => {
    expect(entries.length).toBeGreaterThan(1000)
  })

  it('reconnaît une clé vide (épreuve du détecteur)', () => {
    const sample = parsePo(
      '#: src/components/settings/Integrations.tsx:1\nmsgid "Hello"\nmsgstr ""\n\n#: src/x.tsx:2\nmsgid "Ok"\nmsgstr "Bien"\n'
    )
    expect(sample.filter((e) => e.msgstr === '').map((e) => e.msgid)).toEqual(['Hello'])
  })

  it('couvre bien des messages de ces écrans (la portée n est pas vide)', () => {
    const inScope = entries.filter((e) => e.refs.some((r) => SCOPE.test(r.split(':')[0])))
    expect(inScope.length).toBeGreaterThan(200)
  })

  it("couvre la page Profils d'envoi (la portée n'est pas vide)", () => {
    const inScope = entries.filter((e) =>
      e.refs.some((r) => /(SendingProfilesPage|sending_profiles\/veridian_[A-Za-z0-9_]+)\.tsx?/.test(r.split(':')[0]))
    )
    expect(inScope.length).toBeGreaterThan(150)
    expect(inScope.some((e) => e.msgid === 'Warmup, day {day}/{of}' && e.msgstr === 'Chauffe, jour {day}/{of}')).toBe(true)
  })

  it("traduit les messages-clés de la page Profils d'envoi, variables conservées", () => {
    const fr = new Map(entries.map((e) => [e.msgid, e.msgstr]))
    expect(fr.get('{name}: rate ÷{factor}, {reason}')).toBe('{name} : débit ÷{factor}, {reason}')
    expect(fr.get('Window closed until {time}')).toBe("Fenêtre fermée jusqu'à {time}")
    expect(fr.get('Sending profiles')).toBe("Profils d'envoi")
    expect(fr.get('Anti-spam gateways')).toBe('Passerelles anti-spam')
    expect(fr.get('Soon')).toBe('Bientôt')
    // chaque variable {x} du message source existe dans la traduction
    for (const e of entries) {
      if (!e.refs.some((r) => /sending_profiles\//.test(r)) || e.msgstr === '') continue
      const vars = (e.msgid.match(/\{[A-Za-z0-9_]+\}/g) ?? []).sort()
      const varsFr = (e.msgstr.match(/\{[A-Za-z0-9_]+\}/g) ?? []).sort()
      expect(varsFr, e.msgid).toEqual(vars)
    }
  })

  it("n'a aucun msgstr français vide pour ces écrans", () => {
    const empty = entries
      .filter((e) => e.msgid !== '' && e.msgstr === '')
      .filter((e) => e.refs.some((r) => SCOPE.test(r.split(':')[0])))
      .map((e) => e.msgid.slice(0, 80))
    expect(empty).toEqual([])
  })
})
