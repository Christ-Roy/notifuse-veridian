import { describe, it, expect, vi } from 'vitest'
import { screen, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'

vi.mock('../../router', () => ({ router: { navigate: vi.fn() } }))
vi.mock('../../services/api/veridian_mail', async (importOriginal) => {
  const actual = await importOriginal<typeof import('../../services/api/veridian_mail')>()
  return { ...actual, mailService: { ...actual.mailService, attachment: vi.fn() } }
})

import { mailService } from '../../services/api/veridian_mail'
import { VeridianMailBodyFrame } from './veridian_mail_body_frame'
import { effectiveFrameMode } from './veridian_mail_theme'
import { VeridianMailBody } from './veridian_mail_body'
import { BANNER_PNG_BASE64, makeMessage, RICH_HTML, WS } from './veridian_mail_fixtures'
import { renderMail } from './veridian_mail_test_utils'

const user = userEvent.setup({ delay: null })

describe('iframe isolé du corps HTML', () => {
  it('sandbox exact : jamais allow-scripts, allow-same-origin seulement pour mesurer, popups hors du bac à sable', () => {
    renderMail(<VeridianMailBodyFrame html="<p>Bonjour</p>" allowRemoteImages={false} />)
    const frame = screen.getByTestId('mail-body-frame')
    expect(frame.tagName).toBe('IFRAME')
    const tokens = (frame.getAttribute('sandbox') ?? '').split(/\s+/).sort()
    expect(tokens).toEqual(['allow-popups', 'allow-popups-to-escape-sandbox', 'allow-same-origin'])
    expect(frame.getAttribute('sandbox')).not.toContain('allow-scripts')
    expect(frame.getAttribute('referrerpolicy')).toBe('no-referrer')
  })

  it('le contenu passe par srcDoc avec la CSP stricte, sans aucun script', () => {
    renderMail(<VeridianMailBodyFrame html={'<p>Bonjour</p><script>alert(1)</script>'} allowRemoteImages={false} />)
    const doc = screen.getByTestId('mail-body-frame').getAttribute('srcdoc') ?? ''
    expect(doc).toContain("default-src 'none'; style-src 'unsafe-inline'; img-src data:")
    expect(doc).toContain('Bonjour')
    expect(doc).not.toMatch(/<script/i)
    expect(screen.getByTestId('mail-body-frame').getAttribute('src')).toBeNull()
  })

  it('images distantes : la CSP s’élargit à https: seulement quand on le demande', () => {
    const { rerender } = renderMail(<VeridianMailBodyFrame html="<img src='https://t.fr/p.gif'>" allowRemoteImages={false} />)
    expect(screen.getByTestId('mail-body-frame').getAttribute('srcdoc')).not.toContain('img-src data: https:')
    rerender(<VeridianMailBodyFrame html="<img src='https://t.fr/p.gif'>" allowRemoteImages />)
    expect(screen.getByTestId('mail-body-frame').getAttribute('srcdoc')).toContain('img-src data: https:')
  })

  it('mode sombre : papier sombre pour un mail sobre, papier clair pour un mail qui pose ses couleurs', () => {
    expect(effectiveFrameMode('dark', '<p>texte</p>', 'auto')).toBe('dark')
    expect(effectiveFrameMode('dark', '<td bgcolor="#fff">x</td>', 'auto')).toBe('light')
    expect(effectiveFrameMode('dark', '<td bgcolor="#fff">x</td>', 'dark')).toBe('dark')
    expect(effectiveFrameMode('light', '<p>texte</p>', 'auto')).toBe('light')
    renderMail(<VeridianMailBodyFrame html="<p>texte</p>" allowRemoteImages={false} />, { dark: true })
    expect(screen.getByTestId('mail-body-frame').getAttribute('srcdoc')).toContain('color-scheme:dark')
  })
})

describe('corps d’un mail', () => {
  it('bandeau « images distantes bloquées » puis bascule sur demande', async () => {
    const msg = makeMessage({ id: 'm1', kind: 'reply_human', direction: 'in', body_html_sanitized: RICH_HTML })
    renderMail(<VeridianMailBody workspaceId={WS} message={msg} />)
    const banner = screen.getByTestId('remote-images-banner')
    expect(within(banner).getByText('Remote images are blocked (open tracking).')).toBeInTheDocument()
    expect(screen.getByTestId('mail-body-frame').getAttribute('srcdoc')).toContain('img-src data:;')
    await user.click(within(banner).getByRole('button', { name: 'Show images' }))
    expect(screen.queryByTestId('remote-images-banner')).toBeNull()
    expect(screen.getByTestId('mail-body-frame').getAttribute('srcdoc')).toContain('img-src data: https:')
  })

  it('pas de bandeau quand le mail n’a aucune image distante', () => {
    const msg = makeMessage({ id: 'm1', kind: 'reply_human', direction: 'in', body_html_sanitized: '<p>Bonjour</p>' })
    renderMail(<VeridianMailBody workspaceId={WS} message={msg} />)
    expect(screen.queryByTestId('remote-images-banner')).toBeNull()
  })

  it('images incorporées (cid:) lues par l’API et injectées en data: dans le document', async () => {
    vi.mocked(mailService.attachment).mockResolvedValue({
      blob: new Blob([Uint8Array.from(atob(BANNER_PNG_BASE64), (c) => c.charCodeAt(0))], { type: 'application/octet-stream' }),
      filename: 'banniere.png',
      reconstructed: false,
      contentType: 'image/png'
    })
    const msg = makeMessage({
      id: 'm1',
      kind: 'reply_human',
      direction: 'in',
      body_html_sanitized: '<p>Bonjour</p><img src="cid:banniere@martin">',
      attachments: [{ index: 0, name: 'banniere.png', content_type: 'image/png', size: 7603, inline: true, content_id: '<banniere@martin>', available: true }]
    })
    renderMail(<VeridianMailBody workspaceId={WS} message={msg} />)
    await screen.findByTestId('mail-body-frame')
    await vi.waitFor(() => expect(screen.getByTestId('mail-body-frame').getAttribute('srcdoc')).toContain('src="data:image/png;base64,'))
    expect(mailService.attachment).toHaveBeenCalledWith(WS, 'm1', 0, 'banniere.png', expect.anything())
  })

  it('sans HTML : repli texte rendu par React, échappé, liens http(s) cliquables, citation repliée', async () => {
    const msg = makeMessage({
      id: 'm2',
      kind: 'reply_human',
      direction: 'in',
      body_text: 'Voyez <b>gras</b> et <img src=x onerror=alert(1)> https://www.exemple.fr/contact.\n\nLe 26 oct. 2026, ASD a écrit :\n> Message cité\n> sur deux lignes\n'
    })
    renderMail(<VeridianMailBody workspaceId={WS} message={msg} />)
    expect(screen.queryByTestId('mail-body-frame')).toBeNull()
    const body = screen.getByTestId('mail-text-body')
    expect(body.textContent).toContain('<b>gras</b>')
    expect(body.querySelector('b')).toBeNull()
    expect(body.querySelector('img')).toBeNull()
    const link = within(body).getByRole('link', { name: 'https://www.exemple.fr/contact' })
    expect(link.getAttribute('href')).toBe('https://www.exemple.fr/contact')
    expect(link.getAttribute('rel')).toContain('noopener')
    expect(link.getAttribute('target')).toBe('_blank')
    expect(screen.queryByTestId('quoted-text')).toBeNull()
    await user.click(screen.getByRole('button', { name: 'Show the quoted text' }))
    expect(screen.getByTestId('quoted-text').textContent).toContain('Message cité')
    expect(screen.getByRole('button', { name: 'Hide the quoted text' })).toHaveAttribute('aria-expanded', 'true')
  })

  it('un lien javascript: du texte brut reste du texte', () => {
    const msg = makeMessage({ id: 'm3', kind: 'reply_human', direction: 'in', body_text: 'cliquez javascript:alert(1) ou data:text/html,<b>x</b>' })
    renderMail(<VeridianMailBody workspaceId={WS} message={msg} />)
    expect(screen.queryAllByRole('link')).toHaveLength(0)
  })

  it('corps indisponible : explication claire, pas de blanc', () => {
    const msg = makeMessage({ id: 'm4', kind: 'sent', direction: 'out', body_source: 'unavailable' })
    renderMail(<VeridianMailBody workspaceId={WS} message={msg} />)
    expect(screen.getByTestId('body-unavailable')).toHaveTextContent('The content of this mail is not available')
  })
})
