import { useLingui } from '@lingui/react/macro'

import {
  mailboxErrorKind,
  type MailboxAccess,
  type MailboxAccessState,
  type MailboxAttempt,
  type MailboxCheck,
  type MailboxErrorKind,
  type MailboxFolder,
  type MailboxSecurity
} from '../../services/api/veridian_mailbox_access'

// Tous les textes de l'écran « Mailbox access » : les codes du serveur (état, contrôles du test, refus d'un essai)
// sont traduits ICI, côté console. Les t`...` restent dans ce hook (un `t` passé à une fonction hors composant
// n'est pas capté par l'extracteur Lingui et rendrait un texte vide en production).

export type MailboxApp = 'iphone' | 'mac' | 'spark' | 'outlook' | 'thunderbird' | 'gmail_android' | 'other'
export const MAILBOX_APPS: readonly MailboxApp[] = [
  'iphone',
  'mac',
  'spark',
  'outlook',
  'thunderbird',
  'gmail_android',
  'other'
]
export const APPLE_APPS: readonly MailboxApp[] = ['iphone', 'mac']

export function useMailboxLabels() {
  const { t } = useLingui()

  const security = (s: MailboxSecurity | string | undefined): string => {
    if (s === 'ssl') return t`SSL/TLS`
    if (s === 'starttls') return t`STARTTLS`
    if (s === 'none') return t`None`
    return String(s ?? '')
  }

  const state = (s: MailboxAccessState): string => {
    switch (s) {
      case 'active':
        return t`Active`
      case 'closed':
        return t`Closed`
      case 'disabled':
        return t`Disabled`
      case 'revoked':
        return t`Revoked`
    }
  }

  const stateHelp = (s: MailboxAccessState, a: Pick<MailboxAccess, 'closed_reason'>): string => {
    switch (s) {
      case 'active':
        return t`This access works: apps can connect with it.`
      case 'disabled':
        return t`Switched off. Nothing can connect with it until you re-enable it.`
      case 'revoked':
        return t`Revoked for good. It cannot be reopened: create a new access if needed.`
      case 'closed':
        if (a.closed_reason === 'holder_gone') {
          return t`Closed because the person it belongs to left the workspace. It reopens if they come back.`
        }
        if (a.closed_reason === 'address_removed') {
          return t`Closed because this address is no longer a sender of the sending profile.`
        }
        if (a.closed_reason === 'workspace_closed') return t`Closed because the workspace is closed.`
        return t`Closed by the workspace. It reopens when the cause goes away.`
    }
  }

  const holder = (a: Pick<MailboxAccess, 'holder_user_id' | 'holder_email'>): string => {
    if (a.holder_email) return a.holder_email
    if (a.holder_user_id) return t`A workspace member`
    return t`The workspace (shared)`
  }

  const rights = (a: Pick<MailboxAccess, 'can_send' | 'daily_cap'>): string => {
    if (!a.can_send) return t`Read only`
    const cap = a.daily_cap
    if (cap > 0) return t`Read and send, up to ${cap} per day`
    return t`Read and send`
  }

  const attemptVia = (via: MailboxAttempt['via']): string => (via === 'imap' ? t`Receiving` : t`Sending`)

  const attemptResult = (a: Pick<MailboxAttempt, 'ok' | 'reason'>): string => {
    if (a.ok) return t`Succeeded`
    if (a.reason === 'bad_credentials') return t`Refused: wrong address or password`
    if (a.reason === 'rate_limited') return t`Refused: too many attempts, wait a few minutes`
    if (a.reason === 'closed') return t`Refused: this access is closed or switched off`
    return t`Refused`
  }

  const folder = (f: MailboxFolder): string => {
    switch (f) {
      case 'inbox':
        return t`Inbox`
      case 'sent':
        return t`Sent`
      case 'rejects':
        return t`Rejects`
      case 'trash':
        return t`Trash`
    }
  }

  const app = (a: MailboxApp): string => {
    switch (a) {
      case 'iphone':
        return t`iPhone or iPad`
      case 'mac':
        return t`Mac Mail`
      case 'spark':
        return t`Spark`
      case 'outlook':
        return t`Outlook`
      case 'thunderbird':
        return t`Thunderbird`
      case 'gmail_android':
        return t`Gmail on Android`
      case 'other':
        return t`Another app`
    }
  }

  /** Première étape propre à chaque application : le reste de la fiche est commun. */
  const appStart = (a: MailboxApp): string => {
    switch (a) {
      case 'iphone':
        return t`Settings > Mail > Accounts > Add Account > Other > Add Mail Account.`
      case 'mac':
        return t`Mail > Add Account > Other Mail Account.`
      case 'spark':
        return t`Add Account > Other (IMAP). Do not pick Gmail, Outlook or iCloud.`
      case 'outlook':
        return t`Add account > Advanced options > Let me set up my account manually > IMAP.`
      case 'thunderbird':
        return t`Account Settings > Account Actions > Add Mail Account, then Configure manually.`
      case 'gmail_android':
        return t`Open Gmail > your picture > Add another account > Other. Do not pick Google.`
      case 'other':
        return t`Add a new account and choose IMAP (or Other). Do not pick Gmail, Outlook.com or Exchange.`
    }
  }

  /** Ce que la personne doit changer, en une phrase d'action. `address` : l'adresse de l'accès. */
  const checkText = (c: MailboxCheck, address: string): string => {
    const expected = c.expected === undefined ? '' : String(c.expected)
    switch (c.code) {
      case 'host':
        if (c.ok) return t`The server name is right.`
        return expected ? t`Change the server to ${expected}.` : t`Change the server name: use the one shown in the steps.`
      case 'port':
        if (c.ok) return t`The port is right.`
        return expected ? t`Change the port to ${expected}.` : t`Change the port: use the one shown in the steps.`
      case 'security': {
        if (c.ok) return t`The security setting is right.`
        const wanted = security(expected)
        if (expected === 'ssl') return t`Choose SSL/TLS instead of STARTTLS or None.`
        return wanted ? t`Choose ${wanted} as the security setting.` : t`Change the security setting.`
      }
      case 'username':
        if (c.ok) return t`The username is right.`
        return t`Type your whole address (${address}) as the username.`
      case 'password_empty':
        if (c.ok) return t`A password is filled in.`
        return t`Fill in the password: the one shown once when the access was created.`
      case 'door':
        if (c.ok) return t`Our server answers.`
        return t`Our server did not answer: the problem is on our side, try again in a few minutes.`
      case 'credentials':
        if (c.ok) return t`The address and password are accepted.`
        return t`The password is not accepted: paste it again exactly, or create a new access if it is lost.`
      case 'access_open':
        if (c.ok) return t`This access is open.`
        return t`This access is closed or switched off: re-enable it, or create a new one.`
      case 'can_send':
        if (c.ok) return t`This access can send.`
        return t`This access is read only: create an access that can send to send from your app.`
      case 'not_suspended':
        if (c.ok) return t`Your workspace is not suspended.`
        return t`Your workspace is suspended, so sending is stopped: check your plan.`
      case 'profile_active':
        if (c.ok) return t`The sending profile is active.`
        return t`The sending profile is paused or removed: resume it in Sending profiles.`
      default:
        return c.ok ? t`Check passed.` : t`A check failed: contact support.`
    }
  }

  const errorText = (kind: MailboxErrorKind): string => {
    switch (kind) {
      case 'session':
        return t`Your session has expired. Sign in again.`
      case 'forbidden':
        return t`You do not have the right to manage mailbox access. Ask the workspace owner.`
      case 'owner_required':
        return t`Only the workspace owner can create a shared access.`
      case 'too_many':
        return t`This address already has the maximum number of accesses. Revoke one you no longer use.`
      case 'rate_limited':
        return t`Too many tries in a short time. Wait a minute, then try again.`
      case 'invalid':
        return t`Some information is not valid. Check the form.`
      case 'not_found':
        return t`This access no longer exists. Refresh the page.`
      case 'unavailable':
        return t`The mailbox service is not answering. Try again in a moment.`
      default:
        return t`Something went wrong. Try again.`
    }
  }

  const errorOf = (err: unknown): string => errorText(mailboxErrorKind(err))

  return {
    security,
    state,
    stateHelp,
    holder,
    rights,
    attemptVia,
    attemptResult,
    folder,
    app,
    appStart,
    checkText,
    errorText,
    errorOf
  }
}
