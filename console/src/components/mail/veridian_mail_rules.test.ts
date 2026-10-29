import { describe, it, expect } from 'vitest'

import type { Mailbox, MailMessage, MailKind } from '../../services/api/veridian_mail'
import {
  attachmentGlyph,
  cleanLabels,
  contrastRatio,
  counterpart,
  defaultReplyProfile,
  displayName,
  excerptOf,
  flattenHeaders,
  formatBytes,
  gapBetween,
  gapUnit,
  handledByKind,
  handledParam,
  isReplyable,
  KIND_STYLE,
  linkify,
  listedAttachments,
  mailboxOf,
  NATURE_KINDS,
  normalizeInboxSearch,
  parseAddress,
  parseTextBody,
  replySubject,
  replyTarget,
  themeModeFromBackground,
  thumbnailType,
  unhandledTotal
} from './veridian_mail_rules'

const mb = (over: Partial<Mailbox>): Mailbox => ({ id: 'p1', address: 'a@x.fr', count_in: 0, count_out: 0, count_unhandled: 0, ...over })

describe('filtres de la liste', () => {
  it('chaque filtre de nature couvre les bonnes natures', () => {
    expect(NATURE_KINDS.reponses).toEqual(['reply_human'])
    expect(NATURE_KINDS.auto).toEqual(['reply_auto'])
    expect(NATURE_KINDS.rebonds).toEqual(['bounce_hard', 'bounce_soft'])
    expect(NATURE_KINDS.envoyes).toEqual(['sent'])
    expect(NATURE_KINDS.autres).toEqual(['dsn', 'other'])
    expect(NATURE_KINDS.toutes).toBeUndefined()
  })

  it('traité / à traiter / tous', () => {
    expect(handledParam('a_traiter')).toBe(false)
    expect(handledParam('traites')).toBe(true)
    expect(handledParam('tous')).toBeUndefined()
  })

  it("paramètres d'URL : une valeur inconnue disparaît, jamais d'erreur", () => {
    expect(normalizeInboxSearch({ message: 'm1', boite: 'p1', nature: 'rebonds', etat: 'a_traiter', q: ' devis ', onglet: 'conversation' })).toEqual({
      message: 'm1',
      boite: 'p1',
      nature: 'rebonds',
      etat: 'a_traiter',
      q: 'devis',
      onglet: 'conversation'
    })
    const bad = normalizeInboxSearch({ nature: 'zzz', etat: 3, message: '', q: 'x'.repeat(500), onglet: 'autre' })
    expect(bad.nature).toBeUndefined()
    expect(bad.etat).toBeUndefined()
    expect(bad.message).toBeUndefined()
    expect(bad.onglet).toBeUndefined()
    expect(bad.q).toHaveLength(200)
  })

  it('réponses à traiter : somme des boîtes, jamais négative', () => {
    expect(unhandledTotal(undefined)).toBe(0)
    expect(unhandledTotal([mb({ count_unhandled: 3 }), mb({ id: 'p2', count_unhandled: 4 }), mb({ id: 'p3', count_unhandled: -2 })])).toBe(7)
  })
})

describe('palette : contraste AA', () => {
  const kinds = Object.keys(KIND_STYLE) as MailKind[]
  it.each(kinds.flatMap((k) => (['light', 'dark'] as const).map((m) => [k, m] as const)))('%s en mode %s : texte sur fond teinté au moins 4,5:1', (kind, mode) => {
    const s = KIND_STYLE[kind][mode]
    expect(contrastRatio(s.fg, s.bg)).toBeGreaterThanOrEqual(4.5)
  })

  it('le calcul de contraste est juste (noir sur blanc = 21, identique = 1)', () => {
    expect(contrastRatio('#000000', '#ffffff')).toBeCloseTo(21, 0)
    expect(contrastRatio('#777777', '#777777')).toBeCloseTo(1, 5)
  })

  it('thème détecté depuis le fond Ant Design', () => {
    expect(themeModeFromBackground('#ffffff')).toBe('light')
    expect(themeModeFromBackground('#F9F9F9')).toBe('light')
    expect(themeModeFromBackground('#141414')).toBe('dark')
    expect(themeModeFromBackground('rgb(20, 20, 20)')).toBe('dark')
    expect(themeModeFromBackground('nimporte quoi')).toBe('light')
  })
})

describe('adresses', () => {
  it('nom et adresse, guillemets, adresse seule', () => {
    expect(parseAddress('Marie Dupont <marie@exemple.fr>')).toEqual({ name: 'Marie Dupont', email: 'marie@exemple.fr' })
    expect(parseAddress('"Dupont, Marie" <marie@exemple.fr>')).toEqual({ name: 'Dupont, Marie', email: 'marie@exemple.fr' })
    expect(parseAddress('marie@exemple.fr')).toEqual({ name: '', email: 'marie@exemple.fr' })
    expect(parseAddress(undefined)).toEqual({ name: '', email: '' })
  })
  it('nom affiché : nom, sinon repli, sinon adresse', () => {
    expect(displayName('Marie <m@x.fr>')).toBe('Marie')
    expect(displayName('m@x.fr', 'Marie D.')).toBe('Marie D.')
    expect(displayName('m@x.fr')).toBe('m@x.fr')
  })
  it('interlocuteur de la liste : nom de celui qui répond, contact pour un envoi ou un rebond', () => {
    const base = { contact_email: 'c@x.fr', to: [] as string[] }
    expect(counterpart({ ...base, direction: 'in', kind: 'reply_human', from: 'Marie Dupont <m@x.fr>' })).toBe('Marie Dupont')
    expect(counterpart({ ...base, direction: 'in', kind: 'reply_human', from: 'm@x.fr' })).toBe('m@x.fr')
    expect(counterpart({ ...base, direction: 'in', kind: 'bounce_hard', from: 'Mail Delivery Subsystem <mailer-daemon@g.com>' })).toBe('c@x.fr')
    expect(counterpart({ contact_email: '', to: ['T <t@x.fr>'], direction: 'out', kind: 'sent', from: 'me@x.fr' })).toBe('t@x.fr')
    expect(counterpart({ contact_email: '', to: [], direction: 'in', kind: 'dsn', from: 'F <f@x.fr>' })).toBe('f@x.fr')
  })
})

describe('réponse', () => {
  it('objet : un seul « Re: », quelle que soit la langue du préfixe', () => {
    expect(replySubject('Votre devis')).toBe('Re: Votre devis')
    expect(replySubject('Re: Votre devis')).toBe('Re: Votre devis')
    expect(replySubject('RE: RE: Votre devis')).toBe('Re: Votre devis')
    expect(replySubject('Réf : Votre devis')).toBe('Re: Votre devis')
    expect(replySubject('AW: Anfrage')).toBe('Re: Anfrage')
    expect(replySubject('')).toBe('Re:')
  })
  it('on répond à un humain, à un automate ou à un inconnu, pas à un rebond ni à un envoi', () => {
    const m = (direction: 'in' | 'out', kind: MailKind) => ({ direction, kind })
    expect(isReplyable(m('in', 'reply_human'))).toBe(true)
    expect(isReplyable(m('in', 'reply_auto'))).toBe(true)
    expect(isReplyable(m('in', 'other'))).toBe(true)
    expect(isReplyable(m('in', 'bounce_hard'))).toBe(false)
    expect(isReplyable(m('in', 'dsn'))).toBe(false)
    expect(isReplyable(m('out', 'sent'))).toBe(false)
  })
  it('profil proposé : celui qui a reçu, sinon le seul profil', () => {
    const boxes = [mb({ id: 'p1', profile_id: 'p1' }), mb({ id: 'p2', profile_id: 'p2' })]
    expect(defaultReplyProfile({ profile_id: 'p2' }, boxes)).toBe('p2')
    expect(defaultReplyProfile({ profile_id: undefined }, boxes)).toBe('')
    expect(defaultReplyProfile({ profile_id: undefined }, [boxes[0]])).toBe('p1')
    expect(defaultReplyProfile({ profile_id: 'imap-9' }, boxes)).toBe('imap-9')
  })
  it('destinataire : Reply-To, sinon l’expéditeur d’un mail reçu, sinon le contact', () => {
    expect(replyTarget({ headers: { 'Reply-To': ['Marie <reponse@x.fr>'] }, from: 'm@x.fr', contact_email: 'c@x.fr', kind: 'reply_human' })).toBe('reponse@x.fr')
    expect(replyTarget({ from: 'Marie <m@x.fr>', contact_email: 'c@x.fr', kind: 'reply_human' })).toBe('m@x.fr')
    expect(replyTarget({ from: 'noreply@x.fr', contact_email: 'c@x.fr', kind: 'bounce_hard' })).toBe('c@x.fr')
  })
  it('boîte d’un message : par profil, boîte IMAP liée ou identifiant', () => {
    const boxes = [mb({ id: 'p1', profile_id: 'p1', integration_id: 'imap-1' }), mb({ id: 'imap-2', integration_id: 'imap-2' })]
    expect(mailboxOf('p1', boxes)?.id).toBe('p1')
    expect(mailboxOf('imap-1', boxes)?.id).toBe('p1')
    expect(mailboxOf('imap-2', boxes)?.id).toBe('imap-2')
    expect(mailboxOf('zzz', boxes)).toBeUndefined()
    expect(mailboxOf(undefined, boxes)).toBeUndefined()
  })
})

describe('libellés', () => {
  it('espaces rognés, doublons (casse comprise) retirés, trop long refusé, 20 au plus', () => {
    expect(cleanLabels(['  chaud ', 'Chaud', 'a\nb', ''])).toEqual({ labels: ['chaud', 'a b'], rejected: [] })
    expect(cleanLabels(['x'.repeat(41)]).rejected).toHaveLength(1)
    const many = Array.from({ length: 25 }, (_, i) => `l${i}`)
    const out = cleanLabels(many)
    expect(out.labels).toHaveLength(20)
    expect(out.rejected).toHaveLength(5)
  })
  it('traité par un humain ou un agent', () => {
    expect(handledByKind('humain:42')).toBe('human')
    expect(handledByKind('agent:cle-1')).toBe('agent')
    expect(handledByKind('')).toBe('unknown')
  })
})

describe('temps', () => {
  it('écart entre deux dates', () => {
    expect(gapBetween('2026-10-01T08:00:00Z', '2026-10-03T08:05:00Z')).toEqual({ days: 2, hours: 0, minutes: 5 })
    expect(gapBetween('2026-10-01T08:00:00Z', '2026-10-01T10:30:00Z')).toEqual({ days: 0, hours: 2, minutes: 30 })
    expect(gapBetween('2026-10-03T08:00:00Z', '2026-10-01T08:00:00Z')).toBeNull()
    expect(gapBetween('nimporte', '2026-10-01T08:00:00Z')).toBeNull()
  })
  it('plus grande unité', () => {
    expect(gapUnit({ days: 2, hours: 3, minutes: 4 })).toEqual({ unit: 'day', value: 2 })
    expect(gapUnit({ days: 0, hours: 3, minutes: 4 })).toEqual({ unit: 'hour', value: 3 })
    expect(gapUnit({ days: 0, hours: 0, minutes: 4 })).toEqual({ unit: 'minute', value: 4 })
  })
})

describe('pièces jointes', () => {
  it('icône par type, par extension si le type est vague', () => {
    expect(attachmentGlyph({ content_type: 'image/png', name: 'a.png' })).toBe('image')
    expect(attachmentGlyph({ content_type: 'application/pdf', name: 'devis' })).toBe('pdf')
    expect(attachmentGlyph({ content_type: 'application/octet-stream', name: 'devis.PDF' })).toBe('pdf')
    expect(attachmentGlyph({ content_type: 'application/vnd.openxmlformats-officedocument.wordprocessingml.document', name: 'x' })).toBe('word')
    expect(attachmentGlyph({ content_type: 'application/octet-stream', name: 'tarif.xlsx' })).toBe('excel')
    expect(attachmentGlyph({ content_type: 'application/zip', name: 'x.zip' })).toBe('archive')
    expect(attachmentGlyph({ content_type: 'text/calendar', name: 'invite.ics' })).toBe('calendar')
    expect(attachmentGlyph({ content_type: 'application/x-foo', name: 'truc' })).toBe('other')
  })
  it('miniature : images courantes seulement, jamais le SVG, ni trop lourd, ni non conservé', () => {
    const att = (content_type: string, size = 1000, available = true) => ({ content_type, size, available })
    expect(thumbnailType(att('image/png'))).toBe('image/png')
    expect(thumbnailType(att('image/JPG'))).toBe('image/jpeg')
    expect(thumbnailType(att('image/png; name=x'))).toBe('image/png')
    expect(thumbnailType(att('image/svg+xml'))).toBeNull()
    expect(thumbnailType(att('image/png', 50 * 1024 * 1024))).toBeNull()
    expect(thumbnailType(att('image/png', 1000, false))).toBeNull()
    expect(thumbnailType(att('application/pdf'))).toBeNull()
  })
  it('les images incorporées au corps ne sont pas listées comme pièces jointes', () => {
    const list = listedAttachments({
      attachments: [
        { index: 0, name: 'logo.png', content_type: 'image/png', size: 1, inline: true, content_id: 'logo@x', available: true },
        { index: 1, name: 'devis.pdf', content_type: 'application/pdf', size: 1, available: true }
      ]
    })
    expect(list.map((a) => a.name)).toEqual(['devis.pdf'])
  })
  it('tailles lisibles en français et en anglais', () => {
    expect(formatBytes(512, 'fr')).toBe('512 o')
    expect(formatBytes(2048, 'fr')).toBe('2 Ko')
    expect(formatBytes(1536 * 1024, 'fr')).toBe('1,5 Mo')
    expect(formatBytes(2048, 'en')).toBe('2 KB')
    expect(formatBytes(-1)).toBe('')
  })
})

describe('en-têtes et extrait', () => {
  it('en-têtes à plat et triés', () => {
    expect(flattenHeaders({ Subject: ['x'], 'Received': ['a', 'b'] })).toEqual([
      { name: 'Received', value: 'a' },
      { name: 'Received', value: 'b' },
      { name: 'Subject', value: 'x' }
    ])
    expect(flattenHeaders(undefined)).toEqual([])
  })
  it('extrait : texte sans citations, sinon texte du HTML, sinon raison du rebond', () => {
    expect(excerptOf({ body_text: 'Bonjour,\n> ancien\n> message\nMerci de votre offre.' })).toBe('Bonjour, Merci de votre offre.')
    expect(excerptOf({ body_html_sanitized: '<style>p{}</style><p>Salut <b>Paul</b></p><blockquote>vieux</blockquote>' })).toBe('Salut Paul')
    expect(excerptOf({ dsn: { reason_fr: 'Adresse inexistante' } })).toBe('Adresse inexistante')
    expect(excerptOf({ body_text: 'a'.repeat(300) }, 50)).toHaveLength(50)
  })
  it('type MailMessage accepté par les helpers (compilation)', () => {
    const m: Pick<MailMessage, 'direction' | 'kind'> = { direction: 'in', kind: 'reply_human' }
    expect(isReplyable(m)).toBe(true)
  })
})

describe('corps texte : liens et citations', () => {
  it('liens http(s) cliquables, ponctuation finale écartée', () => {
    expect(linkify('Voir https://exemple.fr/devis.')).toEqual([
      { type: 'text', text: 'Voir ' },
      { type: 'link', text: 'https://exemple.fr/devis', href: 'https://exemple.fr/devis' },
      { type: 'text', text: '.' }
    ])
    const paren = linkify('(https://fr.wikipedia.org/wiki/Foo_(bar))')
    expect(paren.find((s) => s.type === 'link')).toMatchObject({ href: 'https://fr.wikipedia.org/wiki/Foo_(bar)' })
    expect(linkify('rien ici')).toEqual([{ type: 'text', text: 'rien ici' }])
  })
  it('jamais de lien javascript:, data: ou ftp:', () => {
    for (const bad of ['javascript:alert(1)', 'data:text/html,<b>x</b>', 'ftp://serveur/fichier']) {
      expect(linkify(bad).every((s) => s.type === 'text')).toBe(true)
    }
  })
  it('citations « > » regroupées avec leur ligne d’introduction', () => {
    const blocks = parseTextBody('Merci !\n\nLe 12 oct. 2026 à 10:00, Paul a écrit :\n> Bonjour\n> voici le devis\n\nCordialement')
    expect(blocks.map((b) => b.type)).toEqual(['paragraph', 'quote', 'paragraph'])
    const quote = blocks[1]
    expect(quote.type === 'quote' && quote.lines).toEqual(['Le 12 oct. 2026 à 10:00, Paul a écrit :', 'Bonjour', 'voici le devis'])
  })
})
