// Boîte unifiée : règles pures (sans React) de la page « Boîte de réception ».
// Natures et filtres, palette accessible, adresses, écarts de temps, pièces jointes, libellés,
// corps texte (liens et citations). Tout est testé sans monter un composant.

import type { Mailbox, MailAttachment, MailDirection, MailKind, MailMessage, MailSummary } from '../../services/api/veridian_mail'

// ---------------------------------------------------------------- filtres de la liste

export type NatureFilter = 'reponses' | 'auto' | 'rebonds' | 'envoyes' | 'autres' | 'toutes'
export type StateFilter = 'a_traiter' | 'traites' | 'tous'

export const NATURE_FILTERS: readonly NatureFilter[] = ['reponses', 'auto', 'rebonds', 'envoyes', 'autres', 'toutes']
export const STATE_FILTERS: readonly StateFilter[] = ['a_traiter', 'traites', 'tous']

export const DEFAULT_NATURE: NatureFilter = 'reponses'
export const DEFAULT_STATE: StateFilter = 'tous'

/** Natures de mail couvertes par chaque filtre de la liste. */
export const NATURE_KINDS: Record<NatureFilter, MailKind[] | undefined> = {
  reponses: ['reply_human'],
  auto: ['reply_auto'],
  rebonds: ['bounce_hard', 'bounce_soft'],
  envoyes: ['sent'],
  autres: ['dsn', 'other'],
  toutes: undefined
}

/** Paramètres d'URL de la page : partageables (?message=ID&boite=...). */
export interface InboxSearch {
  message?: string
  boite?: string
  nature?: NatureFilter
  etat?: StateFilter
  q?: string
  onglet?: 'message' | 'conversation'
}

const str = (v: unknown): string | undefined => (typeof v === 'string' && v.trim() !== '' ? v.trim() : undefined)

/** Valide les paramètres d'URL : une valeur inconnue disparaît, jamais d'erreur de page. */
export function normalizeInboxSearch(raw: Record<string, unknown>): InboxSearch {
  const nature = str(raw.nature)
  const etat = str(raw.etat)
  const onglet = str(raw.onglet)
  const q = str(raw.q)
  return {
    message: str(raw.message),
    boite: str(raw.boite),
    nature: nature && (NATURE_FILTERS as readonly string[]).includes(nature) ? (nature as NatureFilter) : undefined,
    etat: etat && (STATE_FILTERS as readonly string[]).includes(etat) ? (etat as StateFilter) : undefined,
    q: q ? q.slice(0, 200) : undefined,
    onglet: onglet === 'conversation' ? 'conversation' : onglet === 'message' ? 'message' : undefined
  }
}

export function handledParam(state: StateFilter): boolean | undefined {
  if (state === 'a_traiter') return false
  if (state === 'traites') return true
  return undefined
}

/** Nombre de réponses humaines à traiter, toutes boîtes confondues (badge du menu, tuile). */
export function unhandledTotal(mailboxes: readonly Mailbox[] | undefined): number {
  if (!mailboxes) return 0
  return mailboxes.reduce((n, m) => n + Math.max(0, m.count_unhandled || 0), 0)
}

// ---------------------------------------------------------------- palette (contraste AA)

export type ThemeMode = 'light' | 'dark'

export interface KindStyle {
  fg: string
  bg: string
  border: string
}

/** Couleur de chaque nature, texte sur fond teinté : contraste AA (4,5:1) vérifié par un test. */
export const KIND_STYLE: Record<MailKind, Record<ThemeMode, KindStyle>> = {
  sent: {
    light: { fg: '#0958d9', bg: '#e6f4ff', border: '#91caff' },
    dark: { fg: '#91caff', bg: '#111a2c', border: '#15325b' }
  },
  reply_human: {
    light: { fg: '#237804', bg: '#f6ffed', border: '#b7eb8f' },
    dark: { fg: '#95de64', bg: '#162312', border: '#274916' }
  },
  reply_auto: {
    light: { fg: '#434343', bg: '#f0f0f0', border: '#d9d9d9' },
    dark: { fg: '#bfbfbf', bg: '#262626', border: '#434343' }
  },
  bounce_hard: {
    light: { fg: '#a8071a', bg: '#fff1f0', border: '#ffa39e' },
    dark: { fg: '#ffa39e', bg: '#2a1215', border: '#58181c' }
  },
  bounce_soft: {
    light: { fg: '#ad4e00', bg: '#fff7e6', border: '#ffd591' },
    dark: { fg: '#ffc069', bg: '#2b1d11', border: '#593815' }
  },
  dsn: {
    light: { fg: '#595959', bg: '#fafafa', border: '#d9d9d9' },
    dark: { fg: '#bfbfbf', bg: '#1f1f1f', border: '#434343' }
  },
  other: {
    light: { fg: '#531dab', bg: '#f9f0ff', border: '#d3adf7' },
    dark: { fg: '#d3adf7', bg: '#1a1325', border: '#301c4d' }
  }
}

function channel(v: number): number {
  const s = v / 255
  return s <= 0.03928 ? s / 12.92 : Math.pow((s + 0.055) / 1.055, 2.4)
}

function luminance(hex: string): number {
  const m = /^#?([0-9a-f]{6})$/i.exec(hex)
  if (!m) return 0
  const n = parseInt(m[1], 16)
  return 0.2126 * channel((n >> 16) & 255) + 0.7152 * channel((n >> 8) & 255) + 0.0722 * channel(n & 255)
}

/** Rapport de contraste WCAG entre deux couleurs hexadécimales (#rrggbb). */
export function contrastRatio(a: string, b: string): number {
  const la = luminance(a)
  const lb = luminance(b)
  return (Math.max(la, lb) + 0.05) / (Math.min(la, lb) + 0.05)
}

/** Clair ou sombre, d'après la couleur de fond du thème Ant Design en cours. */
export function themeModeFromBackground(colorBgContainer: string): ThemeMode {
  const hex = /^#([0-9a-f]{3}|[0-9a-f]{6})$/i.exec(colorBgContainer.trim())
  if (hex) {
    const full = hex[1].length === 3 ? hex[1].split('').map((c) => c + c).join('') : hex[1]
    return luminance(full) < 0.3 ? 'dark' : 'light'
  }
  const rgb = /rgba?\(\s*(\d+)[,\s]+(\d+)[,\s]+(\d+)/i.exec(colorBgContainer)
  if (rgb) {
    const l = 0.2126 * channel(+rgb[1]) + 0.7152 * channel(+rgb[2]) + 0.0722 * channel(+rgb[3])
    return l < 0.3 ? 'dark' : 'light'
  }
  return 'light'
}

// ---------------------------------------------------------------- adresses

export interface ParsedAddress {
  name: string
  email: string
}

/** « Marie Dupont <marie@exemple.fr> » -> { name, email }. Tolère l'adresse seule. */
export function parseAddress(raw: string | undefined): ParsedAddress {
  const value = (raw ?? '').trim()
  if (!value) return { name: '', email: '' }
  const m = /^(.*?)\s*<([^<>]+)>\s*$/.exec(value)
  if (m) return { name: m[1].replace(/^"+|"+$/g, '').trim(), email: m[2].trim() }
  return { name: '', email: value }
}

/** Ce qu'on affiche pour une personne : son nom s'il existe, sinon l'adresse. */
export function displayName(raw: string | undefined, fallbackName?: string): string {
  const a = parseAddress(raw)
  return a.name || fallbackName?.trim() || a.email || ''
}

/** Expéditeur à afficher dans la liste : le contact pour un mail reçu, « Vous » pour un envoi (laissé à l'appelant). */
export function counterpart(s: Pick<MailSummary, 'direction' | 'contact_email' | 'from' | 'to'>): string {
  if (s.contact_email) return s.contact_email
  return s.direction === 'in' ? parseAddress(s.from).email : parseAddress(s.to[0]).email
}

// ---------------------------------------------------------------- réponse

/** Objet d'une réponse : « Re: » une seule fois, quelle que soit la langue du préfixe. */
export function replySubject(subject: string | undefined): string {
  const base = (subject ?? '').replace(/^\s*((re|réf|ref|rép|aw|sv|tr)\s*(\[\d+\])?\s*:\s*)+/i, '').trim()
  return base ? `Re: ${base}` : 'Re:'
}

/** On répond à un mail reçu d'un humain ou d'un inconnu : pas à un rebond ni à un rapport. */
export function isReplyable(m: Pick<MailMessage, 'direction' | 'kind'>): boolean {
  return m.direction === 'in' && (m.kind === 'reply_human' || m.kind === 'reply_auto' || m.kind === 'other')
}

/** Profil proposé pour répondre : celui qui a reçu, sinon le seul profil existant. */
export function defaultReplyProfile(message: Pick<MailMessage, 'profile_id'>, mailboxes: readonly Mailbox[]): string {
  const withProfile = mailboxes.filter((m) => m.profile_id)
  if (message.profile_id && withProfile.some((m) => m.profile_id === message.profile_id)) return message.profile_id
  if (message.profile_id) return message.profile_id
  return withProfile.length === 1 ? (withProfile[0].profile_id ?? '') : ''
}

// ---------------------------------------------------------------- libellés

export const MAX_LABELS = 20
export const MAX_LABEL_LENGTH = 40

/** Nettoie une liste de libellés : espaces rognés, doublons retirés, 40 caractères et 20 libellés au plus. */
export function cleanLabels(input: readonly string[]): { labels: string[]; rejected: string[] } {
  const labels: string[] = []
  const rejected: string[] = []
  const seen = new Set<string>()
  for (const raw of input) {
    const label = raw.replace(/[\r\n\0]/g, ' ').replace(/\s+/g, ' ').trim()
    if (!label) continue
    if (label.length > MAX_LABEL_LENGTH || labels.length >= MAX_LABELS) {
      rejected.push(label)
      continue
    }
    const key = label.toLowerCase()
    if (seen.has(key)) continue
    seen.add(key)
    labels.push(label)
  }
  return { labels, rejected }
}

/** « humain:42 » -> humain ; « agent:cle » -> agent. */
export function handledByKind(handledBy: string | undefined): 'human' | 'agent' | 'unknown' {
  if (!handledBy) return 'unknown'
  if (handledBy.startsWith('agent')) return 'agent'
  if (handledBy.startsWith('humain')) return 'human'
  return 'unknown'
}

// ---------------------------------------------------------------- temps

export interface Gap {
  days: number
  hours: number
  minutes: number
}

/** Écart entre deux dates ISO (arrondi à la minute) ; null si invalide ou si `to` précède `from`. */
export function gapBetween(fromIso: string, toIso: string): Gap | null {
  const a = Date.parse(fromIso)
  const b = Date.parse(toIso)
  if (Number.isNaN(a) || Number.isNaN(b) || b < a) return null
  const minutesTotal = Math.round((b - a) / 60000)
  return {
    days: Math.floor(minutesTotal / 1440),
    hours: Math.floor((minutesTotal % 1440) / 60),
    minutes: minutesTotal % 60
  }
}

/** Plus grande unité de l'écart : jours, sinon heures, sinon minutes. */
export function gapUnit(g: Gap): { unit: 'day' | 'hour' | 'minute'; value: number } {
  if (g.days > 0) return { unit: 'day', value: g.days }
  if (g.hours > 0) return { unit: 'hour', value: g.hours }
  return { unit: 'minute', value: g.minutes }
}

// ---------------------------------------------------------------- pièces jointes

export type AttachmentGlyph = 'image' | 'pdf' | 'word' | 'excel' | 'powerpoint' | 'archive' | 'text' | 'audio' | 'video' | 'calendar' | 'other'

export function attachmentGlyph(att: Pick<MailAttachment, 'content_type' | 'name'>): AttachmentGlyph {
  const type = (att.content_type || '').toLowerCase()
  const ext = (att.name.split('.').pop() || '').toLowerCase()
  if (type.startsWith('image/')) return 'image'
  if (type === 'application/pdf' || ext === 'pdf') return 'pdf'
  if (type.includes('wordprocessingml') || type === 'application/msword' || ['doc', 'docx', 'odt', 'rtf'].includes(ext)) return 'word'
  if (type.includes('spreadsheetml') || type === 'application/vnd.ms-excel' || ['xls', 'xlsx', 'ods', 'csv'].includes(ext)) return 'excel'
  if (type.includes('presentationml') || type === 'application/vnd.ms-powerpoint' || ['ppt', 'pptx', 'odp'].includes(ext)) return 'powerpoint'
  if (type.includes('zip') || type.includes('compressed') || type.includes('x-tar') || ['zip', 'rar', '7z', 'gz', 'tar'].includes(ext)) return 'archive'
  if (type.startsWith('audio/')) return 'audio'
  if (type.startsWith('video/')) return 'video'
  if (type === 'text/calendar' || ext === 'ics') return 'calendar'
  if (type.startsWith('text/') || ext === 'txt') return 'text'
  return 'other'
}

/** Types d'image montrés en miniature (jamais le SVG) et poids maximal chargé pour l'aperçu. */
export const THUMBNAIL_TYPES = ['image/png', 'image/jpeg', 'image/gif', 'image/webp'] as const
export const THUMBNAIL_MAX_BYTES = 3 * 1024 * 1024

export function thumbnailType(att: Pick<MailAttachment, 'content_type' | 'size' | 'available'>): string | null {
  const type = (att.content_type || '').toLowerCase().split(';')[0].trim()
  const normalized = type === 'image/jpg' ? 'image/jpeg' : type
  if (!att.available || att.size > THUMBNAIL_MAX_BYTES) return null
  return (THUMBNAIL_TYPES as readonly string[]).includes(normalized) ? normalized : null
}

/** Pièces à lister : les images incorporées au corps (cid:) ne sont pas des pièces jointes à proprement parler. */
export function listedAttachments(m: Pick<MailMessage, 'attachments'>): MailAttachment[] {
  return (m.attachments ?? []).filter((a) => !(a.inline && a.content_id))
}

export function formatBytes(bytes: number, locale = 'fr'): string {
  if (!Number.isFinite(bytes) || bytes < 0) return ''
  const units = locale.startsWith('fr') ? ['o', 'Ko', 'Mo', 'Go'] : ['B', 'KB', 'MB', 'GB']
  let value = bytes
  let i = 0
  while (value >= 1024 && i < units.length - 1) {
    value /= 1024
    i += 1
  }
  const text = new Intl.NumberFormat(locale, { maximumFractionDigits: value >= 10 || i === 0 ? 0 : 1 }).format(value)
  return `${text}\u00a0${units[i]}`
}

// ---------------------------------------------------------------- en-têtes

/** En-têtes à plat, triés, pour l'affichage « en-têtes complets ». */
export function flattenHeaders(headers: Record<string, string[]> | undefined): Array<{ name: string; value: string }> {
  if (!headers) return []
  const out: Array<{ name: string; value: string }> = []
  for (const name of Object.keys(headers).sort((a, b) => a.localeCompare(b))) {
    for (const value of headers[name] ?? []) out.push({ name, value })
  }
  return out
}

/** Date du message : envoi ou réception selon le sens, sinon la date de tri. */
export function messageDate(m: Pick<MailMessage | MailSummary, 'direction' | 'sent_at' | 'received_at' | 'occurred_at'>): string {
  const own: string | undefined = m.direction === 'out' ? m.sent_at : m.received_at
  return own || m.occurred_at
}

export function directionOf(kind: MailKind): MailDirection {
  return kind === 'sent' ? 'out' : 'in'
}

// ---------------------------------------------------------------- corps texte : liens et citations

export type TextSegment = { type: 'text'; text: string } | { type: 'link'; text: string; href: string }

export type TextBlock =
  | { type: 'paragraph'; segments: TextSegment[]; raw: string }
  | { type: 'quote'; lines: string[]; raw: string }

const URL_RE = /https?:\/\/[^\s<>"'`]+/gi

const count = (s: string, c: string) => s.split(c).length - 1

/** Retire la ponctuation de fin collée à l'URL ; une parenthèse fermante reste si elle ferme une ouvrante de l'URL. */
function trimUrl(url: string): string {
  let u = url
  for (;;) {
    const last = u[u.length - 1]
    if (!last) break
    if (/[.,;:!?'"»\]}]/.test(last) || (last === ')' && count(u, ')') > count(u, '('))) {
      u = u.slice(0, -1)
      continue
    }
    break
  }
  return u
}

/** Découpe une ligne en texte et liens http(s) cliquables (jamais d'autre schéma : ni javascript:, ni data:). */
export function linkify(text: string): TextSegment[] {
  const out: TextSegment[] = []
  let last = 0
  for (const match of text.matchAll(URL_RE)) {
    const start = match.index ?? 0
    const url = trimUrl(match[0])
    if (!/^https?:\/\/[^/\s]+/i.test(url)) continue
    if (start > last) out.push({ type: 'text', text: text.slice(last, start) })
    out.push({ type: 'link', text: url, href: url })
    last = start + url.length
  }
  if (last < text.length) out.push({ type: 'text', text: text.slice(last) })
  return out.length > 0 ? out : [{ type: 'text', text }]
}

const QUOTE_INTRO_RE = /^(le .{4,120}(a écrit|à écrit|wrote)\s*:|on .{4,120}wrote\s*:|-{2,}\s*(message d'origine|original message|message transféré|forwarded message)\s*-{2,}|de\s*:.{0,200}|from\s*:.{0,200})$/i

/**
 * Découpe un texte en paragraphes et blocs cités (lignes « > »). La ligne d'introduction
 * (« Le 12 oct. 2026, X a écrit : ») est rattachée au bloc cité pour être repliée avec lui.
 */
export function parseTextBody(text: string): TextBlock[] {
  const lines = text.replace(/\r\n?/g, '\n').split('\n')
  const blocks: TextBlock[] = []
  let para: string[] = []
  const flushPara = () => {
    if (para.length === 0) return
    const raw = para.join('\n')
    blocks.push({ type: 'paragraph', segments: linkify(raw), raw })
    para = []
  }
  let i = 0
  while (i < lines.length) {
    const line = lines[i]
    if (/^\s*>/.test(line)) {
      // l'introduction tout juste lue ouvre le bloc cité
      let intro: string | null = null
      if (para.length > 0 && QUOTE_INTRO_RE.test(para[para.length - 1].trim())) intro = para.pop() ?? null
      flushPara()
      const quoted: string[] = intro ? [intro] : []
      while (i < lines.length && (/^\s*>/.test(lines[i]) || (lines[i].trim() === '' && i + 1 < lines.length && /^\s*>/.test(lines[i + 1])))) {
        quoted.push(lines[i].replace(/^\s*>+ ?/, ''))
        i += 1
      }
      blocks.push({ type: 'quote', lines: quoted, raw: quoted.join('\n') })
      continue
    }
    if (line.trim() === '') {
      flushPara()
      i += 1
      continue
    }
    para.push(line)
    i += 1
  }
  flushPara()
  return blocks
}

/** Aperçu d'une ligne de liste quand le serveur n'en donne pas. */
export function previewOf(s: Pick<MailSummary, 'preview' | 'dsn_reason_fr'>): string {
  return (s.dsn_reason_fr || s.preview || '').replace(/\s+/g, ' ').trim()
}

// ---------------------------------------------------------------- boîtes

/** Boîte d'un message : par profil d'envoi, par boîte IMAP liée ou par identifiant de boîte. */
export function mailboxOf(profileId: string | undefined, mailboxes: readonly Mailbox[]): Mailbox | undefined {
  if (!profileId) return undefined
  return mailboxes.find((m) => m.id === profileId || m.profile_id === profileId || m.integration_id === profileId)
}

/** Où répondre : Reply-To s'il existe, sinon l'expéditeur d'un mail reçu, sinon le contact. */
export function replyTarget(m: Pick<MailMessage, 'headers' | 'from' | 'contact_email' | 'kind'>): string {
  const rt = m.headers?.['Reply-To']?.[0] ?? m.headers?.['Reply-to']?.[0]
  if (rt && rt.trim()) return parseAddress(rt).email || rt.trim()
  if (m.kind === 'reply_human' || m.kind === 'reply_auto' || m.kind === 'other') return parseAddress(m.from).email || m.contact_email
  return m.contact_email
}

// ---------------------------------------------------------------- extrait d'un message

/** Extrait lisible : texte sans citations, sinon texte du HTML (reparsé dans un document inerte), sinon la raison du rebond. */
export function excerptOf(m: Pick<MailMessage, 'body_text' | 'body_html_sanitized' | 'dsn'>, max = 160): string {
  let text = (m.body_text ?? '')
    .split(/\r?\n/)
    .filter((l) => !/^\s*>/.test(l))
    .join(' ')
  if (!text.trim() && m.body_html_sanitized) {
    const doc = new DOMParser().parseFromString(m.body_html_sanitized, 'text/html')
    doc.querySelectorAll('style,script,blockquote').forEach((el) => el.remove())
    text = doc.body.textContent ?? ''
  }
  text = text.replace(/\s+/g, ' ').trim()
  if (!text && m.dsn?.reason_fr) return m.dsn.reason_fr
  return text.length > max ? `${text.slice(0, max - 1).trimEnd()}…` : text
}
