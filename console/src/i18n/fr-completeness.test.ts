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

const SCOPE = /(^|\/)(Integrations|WorkspaceLayout|SendingProfilesPage|SendQueuePage|StatNode|veridian_[A-Za-z0-9_]+)\.tsx?$/

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

// Lot 4 (08/10/2026) : séparation transactionnel / commercial. Chaque chaîne ajoutée par ce
// lot a sa traduction française ET anglaise (pages hors de la portée ci-dessus : Modèles,
// Journal, API d'envoi, tableau de bord).
describe('complétude fr + en des chaînes du lot 4', () => {
  const LOT4 = [
    'Transactional templates',
    'Sending API / SMTP Bridge',
    'Sending API',
    'Transactional log',
    'Sending log',
    'Profiles in rotation',
    'Sent today',
    'Transactional profile',
    'These emails are subject to no cap: a transactional email always goes out.',
    '{sentLabel} sent. No commercial limit: a transactional mail always goes out.',
    'Commercial',
    'Transactional',
    'SMTP Bridge'
  ]
  for (const locale of ['fr', 'en']) {
    const entries = parsePo(readFileSync(resolve(__dirname, `locales/${locale}.po`), 'utf8'))
    it(`${locale} : aucune chaîne du lot 4 vide`, () => {
      for (const msgid of LOT4) {
        const entry = entries.find((e) => e.msgid === msgid)
        expect(entry, `${locale}: « ${msgid} » absente du catalogue`).toBeDefined()
        expect(entry!.msgstr, `${locale}: « ${msgid} » vide`).not.toBe('')
      }
    })
  }

  it('fr : aucun tiret cadratin entouré d\'espaces dans les chaînes du lot 4', () => {
    const entries = parsePo(readFileSync(resolve(__dirname, 'locales/fr.po'), 'utf8'))
    for (const msgid of LOT4) {
      expect(entries.find((e) => e.msgid === msgid)!.msgstr).not.toMatch(/ — /)
    }
  })
})

// Lot 5 (08/10/2026) : tableau de bord de prospection et surveillance du profil
// transactionnel. TOUT msgid des fichiers du lot (components/prospection/, AnalyticsDashboard)
// a sa traduction française ET anglaise, variables conservées, sans tiret cadratin entouré
// d'espaces. Une chaîne ajoutée sans `lingui extract` + traduction fait échouer ce test.
describe('complétude fr + en du tableau de bord de prospection (lot 5)', () => {
  const LOT5_SCOPE = /(components\/prospection\/|components\/analytics\/AnalyticsDashboard)/
  const MUST_EXIST = [
    'Sends per relay',
    'Per day',
    'Per hour',
    'Sequence progress',
    'Replies by sequence',
    'Segments (lists): replies and remaining stock',
    'Rejections, unsubscribes and complaints',
    'Policy refusals',
    'Reputation by recipient provider',
    'Remaining stock',
    'Automatic replies',
    'Volume and reputation watch',
    '{queued} in the queue, {waiting} waiting, {left} left after',
    'Unusual volume: {sent} mails today, {average} per day on average over the previous 7 days',
    'Slowed ÷{factor}'
  ]

  for (const locale of ['fr', 'en']) {
    const entries = parsePo(readFileSync(resolve(__dirname, `locales/${locale}.po`), 'utf8'))
    const inScope = entries.filter((e) => e.refs.some((r) => LOT5_SCOPE.test(r.split(':')[0])))

    it(`${locale} : la portée du lot 5 n'est pas vide (le test sait la lire)`, () => {
      expect(inScope.length).toBeGreaterThan(60)
    })

    it(`${locale} : aucune chaîne du lot 5 sans traduction`, () => {
      const empty = inScope.filter((e) => e.msgid !== '' && e.msgstr === '').map((e) => e.msgid.slice(0, 80))
      expect(empty).toEqual([])
    })

    it(`${locale} : les chaînes clés du lot 5 existent`, () => {
      for (const msgid of MUST_EXIST) {
        const entry = entries.find((e) => e.msgid === msgid)
        expect(entry, `${locale}: « ${msgid} » absente du catalogue`).toBeDefined()
        expect(entry!.msgstr, `${locale}: « ${msgid} » vide`).not.toBe('')
      }
    })

    it(`${locale} : variables conservées dans chaque traduction du lot 5`, () => {
      for (const e of inScope) {
        if (e.msgstr === '') continue
        const vars = (e.msgid.match(/\{[A-Za-z0-9_]+\}/g) ?? []).sort()
        const varsOut = (e.msgstr.match(/\{[A-Za-z0-9_]+\}/g) ?? []).sort()
        expect(varsOut, e.msgid).toEqual(vars)
      }
    })
  }

  it('fr : aucun tiret cadratin entouré d\'espaces, des vraies traductions françaises', () => {
    const entries = parsePo(readFileSync(resolve(__dirname, 'locales/fr.po'), 'utf8'))
    const fr = new Map(entries.map((e) => [e.msgid, e.msgstr]))
    for (const e of entries.filter((x) => x.refs.some((r) => LOT5_SCOPE.test(r.split(':')[0])))) {
      expect(e.msgstr, e.msgid).not.toMatch(/ — /)
    }
    expect(fr.get('Sends per relay')).toBe('Envois par relais')
    expect(fr.get('Sequence progress')).toBe('Avancement des séquences')
    expect(fr.get('Remaining stock')).toBe('Stock restant')
    expect(fr.get('Automatic replies')).toBe('Réponses automatiques')
    expect(fr.get('Slowed ÷{factor}')).toBe('Ralenti ÷{factor}')
  })
})

// LOT 1 (10/10/2026) : page File d'envoi et compteurs du noeud email. Chaque chaine a sa
// traduction française ET anglaise, sans tiret cadratin entouré d'espaces.
describe('complétude fr + en des chaînes du lot 1 (File d'envoi)', () => {
  const LOT1: Record<string, string> = {
    'Send queue': "File d'envoi",
    'Not examined yet': 'Jamais examinée',
    'Sending window closed': "Fenêtre d'envoi fermée",
    'Daily capacity reached': 'Capacité du jour atteinte',
    'Recipient provider rate limit': 'Débit du fournisseur destinataire',
    'Reputation fuse tripped': 'Fusible de réputation déclenché',
    'Excluded class': 'Classe exclue',
    'Profile paused': 'Profil en pause',
    'No profile in the pool': 'Aucun profil dans le pool',
    'Circuit open': 'Circuit ouvert',
    'Follow-up waiting for its original sender': "La relance attend son expéditeur d'origine",
    'Quota denied': 'Quota refusé',
    'Render failed': 'Rendu en échec',
    'Safety check, retrying': 'Garde-fou, nouvelle tentative',
    'Automation paused': 'Automatisation en pause',
    'Send error': "Erreur d'envoi",
    'Deferred (reason not recorded)': 'Reporté (raison non enregistrée)',
    'Warm-up cap': 'Plafond de chauffe',
    'Provider class cap': 'Plafond de la classe de fournisseur',
    'Per-recipient cap': 'Plafond par destinataire',
    'Per-sender cap': 'Plafond par expéditeur',
    'Pass': 'Passe',
    'Slowed': 'Ralentie',
    'Recompute': 'Recalculer',
    'Remove this contact': 'Sortir ce contact',
    'Decision log': 'Journal des décisions',
    'Raw trace (JSON)': 'Trace brute (JSON)',
    'Queued': 'En file',
    'Sent': 'Envoyé'
  }
  for (const locale of ['fr', 'en']) {
    const entries = parsePo(readFileSync(resolve(__dirname, `locales/${locale}.po`), 'utf8'))
    it(`${locale} : aucune chaîne du lot 1 vide`, () => {
      for (const msgid of Object.keys(LOT1)) {
        const entry = entries.find((e) => e.msgid === msgid)
        expect(entry, `${locale}: « ${msgid} » absente du catalogue`).toBeDefined()
        expect(entry!.msgstr, `${locale}: « ${msgid} » vide`).not.toBe('')
      }
    })
  }

  it('fr : traductions attendues, variables conservées, aucun tiret cadratin entouré d'espaces', () => {
    const entries = parsePo(readFileSync(resolve(__dirname, 'locales/fr.po'), 'utf8'))
    for (const [msgid, expected] of Object.entries(LOT1)) {
      const entry = entries.find((e) => e.msgid === msgid)!
      if (msgid !== 'Sent') expect(entry.msgstr).toBe(expected)
      expect(entry.msgstr).not.toMatch(/ — /)
    }
    const mine = entries.filter((e) => e.refs.some((r) => /send_queue\/|SendQueuePage/.test(r)))
    expect(mine.length).toBeGreaterThan(60)
    for (const e of mine) {
      const vars = (e.msgid.match(/\{[A-Za-z0-9_]+\}/g) ?? []).sort()
      const varsFr = (e.msgstr.match(/\{[A-Za-z0-9_]+\}/g) ?? []).sort()
      expect(varsFr, e.msgid).toEqual(vars)
      expect(e.msgstr, e.msgid).not.toMatch(/ — /)
    }
  })
})
