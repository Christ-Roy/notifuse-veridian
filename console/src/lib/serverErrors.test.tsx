import { describe, it, expect } from 'vitest'
import { renderHook } from '@testing-library/react'
import { useFriendlyServerError, smtpCodeOf } from './serverErrors'

// C6 et D4 (audit UI 19/10/2026) : une erreur SMTP ou serveur ne s'affiche jamais brute.
describe('useFriendlyServerError', () => {
  const run = (raw: string | null) => renderHook(() => useFriendlyServerError()).result.current(raw)

  it('535 : message clair, detail technique conserve', () => {
    const r = run('failed to send test email: authentication failed with code: 535')!
    expect(r.text).toMatch(/refused the login or password/)
    expect(r.text).not.toMatch(/535|authentication failed/)
    expect(r.detail).toContain('code: 535')
  })
  it('4xx temporaire, 5xx rejet, reseau', () => {
    expect(run('smtp error code: 451 try later')!.text).toMatch(/temporarily unavailable/)
    expect(run('smtp error code: 550 mailbox unavailable')!.text).toMatch(/rejected the message/)
    expect(run('dial tcp 1.2.3.4:587: i/o timeout')!.text).toMatch(/cannot be reached/)
  })
  it('import de contacts : index et motif traduits', () => {
    expect(run('invalid contact at index 1: email is required')!.text).toBe('Contact 1: the email address is missing.')
  })
  it('message inconnu : rendu tel quel ; vide : null', () => {
    expect(run('quelque chose')).toEqual({ text: 'quelque chose' })
    expect(run(null)).toBeNull()
  })
  it('smtpCodeOf', () => {
    expect(smtpCodeOf('x code: 535')).toBe(535)
    expect(smtpCodeOf('nothing')).toBeNull()
  })
})
