// Rendu isolé du HTML d'un mail : fonctions pures qui construisent le document de l'iframe.
//
// Défense en profondeur (le serveur assainit déjà, on ne lui fait JAMAIS confiance) :
//   1. l'iframe est `sandbox` SANS allow-scripts (jamais) ;
//   2. le document porte une CSP `default-src 'none'` : ni script, ni cadre, ni connexion, ni police ;
//      images distantes bloquées tant que l'utilisateur ne les a pas demandées (suivi d'ouverture) ;
//   3. le HTML est reparsé dans un document inerte (DOMParser) : scripts, cadres, formulaires,
//      <base>, <meta>, gestionnaires on* et liens non http(s)/mailto/tel sont retirés, les liens
//      s'ouvrent dehors avec rel noopener ;
//   4. aucun contenu de mail n'entre dans le DOM de la console hors de cet iframe.

import type { ThemeMode } from './veridian_mail_rules'

/**
 * `allow-same-origin` seul sert à mesurer la hauteur du contenu (aucun script ne s'exécute dedans) ;
 * `allow-popups` + `allow-popups-to-escape-sandbox` ouvrent les liens dehors, hors du bac à sable.
 * Pas de `allow-scripts`, ni `allow-forms`, ni `allow-top-navigation` : jamais.
 */
export const MAIL_FRAME_SANDBOX = 'allow-same-origin allow-popups allow-popups-to-escape-sandbox'

export function buildCsp(allowRemoteImages: boolean): string {
  const images = allowRemoteImages ? 'img-src data: https:' : 'img-src data:'
  return `default-src 'none'; style-src 'unsafe-inline'; ${images}; form-action 'none'`
}

export interface FramePalette {
  text: string
  background: string
  link: string
  muted: string
  border: string
  surface: string
}

export const LIGHT_PAPER: FramePalette = {
  text: '#1f1f1f',
  background: '#ffffff',
  link: '#0958d9',
  muted: '#595959',
  border: '#d9d9d9',
  surface: '#f5f5f5'
}

export const DARK_PAPER: FramePalette = {
  text: '#e6e6e6',
  background: '#141414',
  link: '#91caff',
  muted: '#a6a6a6',
  border: '#434343',
  surface: '#1f1f1f'
}

const COLOR_RE = /^(#[0-9a-f]{3,8}|rgba?\(\s*\d{1,3}\s*,\s*\d{1,3}\s*,\s*\d{1,3}\s*(,\s*[\d.]+\s*)?\))$/i
const safeColor = (value: string, fallback: string): string => (COLOR_RE.test(value.trim()) ? value.trim() : fallback)

export function paletteFor(mode: ThemeMode, overrides?: Partial<FramePalette>): FramePalette {
  const base = mode === 'dark' ? DARK_PAPER : LIGHT_PAPER
  return {
    text: safeColor(overrides?.text ?? base.text, base.text),
    background: safeColor(overrides?.background ?? base.background, base.background),
    link: safeColor(overrides?.link ?? base.link, base.link),
    muted: safeColor(overrides?.muted ?? base.muted, base.muted),
    border: safeColor(overrides?.border ?? base.border, base.border),
    surface: safeColor(overrides?.surface ?? base.surface, base.surface)
  }
}

/** Le mail pose ses propres couleurs (fond, texte) : on le montre sur papier clair, jamais sur un fond qui le rendrait illisible. */
export function hasOwnColors(html: string): boolean {
  return /\bbgcolor\s*=|background(-color|-image)?\s*[:=]|(^|[\s;"'{])color\s*:\s*[^;"'}]+|<font\b[^>]*\bcolor\s*=/i.test(html)
}

export function stylesheet(palette: FramePalette, mode: ThemeMode): string {
  return `
:root{color-scheme:${mode}}
html,body{margin:0;padding:0;background:${palette.background};color:${palette.text}}
body{font:15px/1.6 Inter,-apple-system,"Segoe UI",Roboto,"Helvetica Neue",Arial,sans-serif;padding:16px 20px;overflow-wrap:anywhere;word-break:break-word}
a{color:${palette.link};text-decoration:underline}
a:focus-visible{outline:2px solid ${palette.link};outline-offset:2px}
img{max-width:100%;height:auto}
table{max-width:100%;border-collapse:collapse}
td,th{vertical-align:top}
p{margin:0 0 .9em}
blockquote{margin:12px 0;padding:2px 0 2px 14px;border-left:3px solid ${palette.border};color:${palette.muted}}
pre,code{font-family:ui-monospace,SFMono-Regular,Menlo,Consolas,monospace;font-size:13px}
pre{white-space:pre-wrap;background:${palette.surface};padding:10px 12px;border-radius:6px;overflow:auto}
hr{border:0;border-top:1px solid ${palette.border}}
@media (max-width:480px){body{padding:12px}}
`.trim()
}

// ---------------------------------------------------------------- nettoyage du HTML

const REMOVED_TAGS = ['script', 'iframe', 'frame', 'frameset', 'object', 'embed', 'applet', 'form', 'base', 'meta', 'link', 'noscript', 'template', 'portal', 'dialog']
const ALLOWED_LINK_SCHEMES = /^(https?:|mailto:|tel:)/i

/** Identifiant de pièce incorporée normalisé (« <abc@x> » et « abc@x » désignent la même image). */
export function normalizeCid(raw: string): string {
  return raw.trim().replace(/^cid:/i, '').replace(/^<|>$/g, '').toLowerCase()
}

export interface PrepareOptions {
  /** content-id normalisé -> URL data: de l'image (pièces incorporées lues par l'API). */
  inlineImages?: Record<string, string>
}

/**
 * Reparse le HTML dans un document inerte et rend ce qui peut entrer dans le corps de l'iframe :
 * les feuilles de style de l'en-tête puis le corps, sans élément actif.
 */
export function prepareMailHtml(html: string, options: PrepareOptions = {}): string {
  if (!html.trim()) return ''
  const doc = new DOMParser().parseFromString(html, 'text/html')
  const styles = Array.from(doc.head.querySelectorAll('style')).map((s) => s.outerHTML)
  for (const tag of REMOVED_TAGS) doc.querySelectorAll(tag).forEach((el) => el.remove())
  doc.body.querySelectorAll('*').forEach((el) => {
    for (const attr of Array.from(el.attributes)) {
      if (/^on/i.test(attr.name)) el.removeAttribute(attr.name)
    }
  })
  doc.body.querySelectorAll('a').forEach((a) => {
    const href = a.getAttribute('href') ?? ''
    if (href && !ALLOWED_LINK_SCHEMES.test(href.trim()) && !href.startsWith('#')) a.removeAttribute('href')
    if (a.hasAttribute('href')) {
      a.setAttribute('target', '_blank')
      a.setAttribute('rel', 'noopener noreferrer nofollow')
    }
  })
  doc.body.querySelectorAll('area').forEach((el) => el.removeAttribute('href'))
  doc.body.querySelectorAll('img').forEach((img) => {
    img.removeAttribute('srcset')
    const src = img.getAttribute('src') ?? ''
    if (/^cid:/i.test(src)) {
      const data = options.inlineImages?.[normalizeCid(src)]
      if (data && data.startsWith('data:image/')) img.setAttribute('src', data)
      else img.removeAttribute('src')
    }
    img.setAttribute('referrerpolicy', 'no-referrer')
  })
  // un style en ligne ne doit pas tirer de ressource distante tant que les images sont bloquées : la CSP s'en charge
  return styles.join('') + doc.body.innerHTML
}

/** Nombre d'images distantes (balises img http(s) et fonds en url(http...)) : sert au bandeau « images bloquées ». */
export function countRemoteImages(html: string): number {
  if (!html) return 0
  const doc = new DOMParser().parseFromString(html, 'text/html')
  let n = 0
  doc.querySelectorAll('img').forEach((img) => {
    if (/^\s*(https?:)?\/\//i.test(img.getAttribute('src') ?? '')) n += 1
  })
  doc.querySelectorAll('[style],[background]').forEach((el) => {
    if (/url\(\s*['"]?\s*(https?:)?\/\//i.test(el.getAttribute('style') ?? '') || /^\s*(https?:)?\/\//i.test(el.getAttribute('background') ?? '')) n += 1
  })
  return n
}

export interface SrcDocOptions {
  html: string
  allowRemoteImages: boolean
  mode: ThemeMode
  palette?: Partial<FramePalette>
  lang?: string
  inlineImages?: Record<string, string>
}

const escapeAttr = (value: string): string => value.replace(/&/g, '&amp;').replace(/"/g, '&quot;').replace(/</g, '&lt;')

/** Document complet de l'iframe : CSP en première balise du <head>, puis feuille de base, puis le mail nettoyé. */
export function buildSrcDoc(opts: SrcDocOptions): string {
  const palette = paletteFor(opts.mode, opts.palette)
  const body = prepareMailHtml(opts.html, { inlineImages: opts.inlineImages })
  return [
    '<!doctype html>',
    `<html lang="${escapeAttr(opts.lang || 'fr')}">`,
    '<head>',
    '<meta charset="utf-8">',
    `<meta http-equiv="Content-Security-Policy" content="${escapeAttr(buildCsp(opts.allowRemoteImages))}">`,
    '<meta name="viewport" content="width=device-width, initial-scale=1">',
    '<base target="_blank">',
    `<style>${stylesheet(palette, opts.mode)}</style>`,
    '</head>',
    `<body>${body}</body>`,
    '</html>'
  ].join('')
}
