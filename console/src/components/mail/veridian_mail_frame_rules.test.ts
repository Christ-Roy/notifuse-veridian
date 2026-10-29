import { describe, it, expect } from 'vitest'

import {
  buildCsp,
  buildSrcDoc,
  countRemoteImages,
  hasOwnColors,
  MAIL_FRAME_SANDBOX,
  normalizeCid,
  paletteFor,
  prepareMailHtml
} from './veridian_mail_frame_rules'

describe('bac à sable de l’iframe', () => {
  it('jamais allow-scripts, ni formulaires, ni navigation du haut ; popups sortent du bac à sable', () => {
    const tokens = MAIL_FRAME_SANDBOX.split(/\s+/)
    expect(tokens).toEqual(['allow-same-origin', 'allow-popups', 'allow-popups-to-escape-sandbox'])
    expect(tokens).not.toContain('allow-scripts')
    expect(MAIL_FRAME_SANDBOX).not.toMatch(/allow-(scripts|forms|top-navigation|modals|downloads)/)
  })
})

describe('CSP du document', () => {
  it('par défaut : rien, styles en ligne, images data: seulement', () => {
    const csp = buildCsp(false)
    expect(csp).toContain("default-src 'none'; style-src 'unsafe-inline'; img-src data:")
    expect(csp).not.toContain('https:')
    expect(csp).toContain("form-action 'none'")
    expect(csp).not.toMatch(/script-src|connect-src|'unsafe-eval'/)
  })
  it('images distantes autorisées sur demande : img-src data: https:', () => {
    expect(buildCsp(true)).toContain('img-src data: https:')
    expect(buildCsp(true)).toContain("default-src 'none'")
  })
})

describe('srcDoc', () => {
  const doc = (html: string, allow = false) => buildSrcDoc({ html, allowRemoteImages: allow, mode: 'light' })

  it('porte la CSP en première balise du head, avant tout contenu du mail', () => {
    const out = doc('<p>Bonjour</p>')
    expect(out).toContain(`<meta http-equiv="Content-Security-Policy" content="${buildCsp(false)}">`)
    expect(out.indexOf('Content-Security-Policy')).toBeLessThan(out.indexOf('<p>Bonjour</p>'))
    expect(out.indexOf('Content-Security-Policy')).toBeLessThan(out.indexOf('<style>'))
  })

  it('bascule des images distantes : la CSP change, le contenu aussi peu que possible', () => {
    expect(doc('<img src="https://t.exemple.fr/p.gif">', false)).toContain('img-src data:;')
    const open = doc('<img src="https://t.exemple.fr/p.gif">', true)
    expect(open).toContain('img-src data: https:;')
    expect(open).toContain('https://t.exemple.fr/p.gif')
  })

  it('feuille de base : police lisible, images et tableaux bornés, liens, mode sombre', () => {
    const light = doc('<p>x</p>')
    expect(light).toContain('img{max-width:100%;height:auto}')
    expect(light).toContain('table{max-width:100%')
    expect(light).toContain('color-scheme:light')
    const dark = buildSrcDoc({ html: '<p>x</p>', allowRemoteImages: false, mode: 'dark' })
    expect(dark).toContain('color-scheme:dark')
    expect(dark).toContain('background:#141414')
    expect(dark).toContain('color:#e6e6e6')
  })

  it('une couleur invalide (injection CSS) retombe sur la couleur de base', () => {
    const p = paletteFor('light', { text: 'red;} body{display:none', background: 'url(http://x)' })
    expect(p.text).toBe('#1f1f1f')
    expect(p.background).toBe('#ffffff')
  })

  it('le document ne contient jamais de script, même si le mail en apporte', () => {
    const out = doc('<p>a</p><script>alert(1)</script><img src=x onerror="alert(2)"><a href="javascript:alert(3)">clic</a>')
    expect(out).not.toMatch(/<script/i)
    expect(out).not.toMatch(/onerror/i)
    expect(out).not.toMatch(/javascript:/i)
  })
})

describe('prepareMailHtml', () => {
  it('retire les éléments actifs : script, cadre, objet, formulaire, base, meta, link', () => {
    const out = prepareMailHtml(
      '<form action="https://evil"><input name=x></form><iframe src="https://evil"></iframe><object data="x"></object><embed src="x"><base href="https://evil/"><meta http-equiv="refresh" content="0;url=https://evil"><link rel="stylesheet" href="https://evil/x.css"><p>ok</p>'
    )
    expect(out).toBe('<p>ok</p>')
  })

  it('liens : ouverts dehors avec noopener, schémas autres que http(s)/mailto/tel retirés', () => {
    const out = prepareMailHtml('<a href="https://exemple.fr/x">a</a><a href="mailto:m@x.fr">b</a><a href="javascript:alert(1)">c</a><a href=" JavaScript:alert(1)">d</a><a href="data:text/html,x">e</a>')
    const d = new DOMParser().parseFromString(out, 'text/html')
    const links = Array.from(d.querySelectorAll('a'))
    expect(links[0].getAttribute('target')).toBe('_blank')
    expect(links[0].getAttribute('rel')).toContain('noopener')
    expect(links[1].getAttribute('href')).toBe('mailto:m@x.fr')
    expect(links[2].hasAttribute('href')).toBe(false)
    expect(links[3].hasAttribute('href')).toBe(false)
    expect(links[4].hasAttribute('href')).toBe(false)
  })

  it('garde les feuilles de style du mail (head) et retire les gestionnaires on*', () => {
    const out = prepareMailHtml('<html><head><style>.a{color:red}</style></head><body><p class="a" onclick="x()" onmouseover="y()">t</p></body></html>')
    expect(out).toContain('<style>.a{color:red}</style>')
    expect(out).not.toMatch(/onclick|onmouseover/)
  })

  it('images cid: remplacées par les données lues par l’API, sinon retirées', () => {
    const out = prepareMailHtml('<img src="cid:Logo@x"><img src="cid:inconnu"><img src="cid:evil">', {
      inlineImages: { 'logo@x': 'data:image/png;base64,AAAA', evil: 'data:text/html;base64,PHNjcmlwdD4=' }
    })
    const imgs = Array.from(new DOMParser().parseFromString(out, 'text/html').querySelectorAll('img'))
    expect(imgs[0].getAttribute('src')).toBe('data:image/png;base64,AAAA')
    expect(imgs[1].hasAttribute('src')).toBe(false)
    expect(imgs[2].hasAttribute('src')).toBe(false)
    expect(imgs[0].getAttribute('referrerpolicy')).toBe('no-referrer')
  })

  it('identifiant cid normalisé', () => {
    expect(normalizeCid('<Logo@Exemple>')).toBe('logo@exemple')
    expect(normalizeCid('cid:Logo@Exemple')).toBe('logo@exemple')
  })

  it('HTML vide : rien', () => {
    expect(prepareMailHtml('   ')).toBe('')
  })
})

describe('images distantes et couleurs propres', () => {
  it('compte les images distantes (balises et fonds)', () => {
    expect(countRemoteImages('<img src="https://t.fr/p.gif"><img src="//cdn.fr/a.png"><img src="data:image/png;base64,AA"><img src="cid:x">')).toBe(2)
    expect(countRemoteImages('<div style="background:url(https://x.fr/bg.png)">a</div><table><tr><td background="http://x.fr/b.jpg">x</td></tr></table>')).toBe(2)
    expect(countRemoteImages('<p>texte</p>')).toBe(0)
    expect(countRemoteImages('')).toBe(0)
  })
  it('un mail qui pose ses couleurs reste sur papier clair', () => {
    expect(hasOwnColors('<td bgcolor="#fff">x</td>')).toBe(true)
    expect(hasOwnColors('<p style="color: #333">x</p>')).toBe(true)
    expect(hasOwnColors('<div style="background-color:#eee">x</div>')).toBe(true)
    expect(hasOwnColors('<font color="red">x</font>')).toBe(true)
    expect(hasOwnColors('<div dir="ltr">Bonjour<br><blockquote style="margin:0 0 0 .8ex;border-left:1px solid rgb(204,204,204)">x</blockquote></div>')).toBe(false)
  })
})
