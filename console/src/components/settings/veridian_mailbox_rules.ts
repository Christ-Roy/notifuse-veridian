import type { Integration } from '../../services/api/workspace'
import {
  accessState,
  type MailboxAccess,
  type MailboxConnection
} from '../../services/api/veridian_mailbox_access'

// Règles pures de l'écran « Mailbox access » (sans React, testées seules).

export const DEFAULT_DAILY_CAP = 200

/** Profils d'envoi = intégrations e-mail du workspace ; leurs adresses = les expéditeurs du profil. */
export function emailProfiles(integrations: Integration[] | undefined): Integration[] {
  return (integrations ?? []).filter((i) => i.type === 'email' && !!i.email_provider)
}

/** Expéditeur par défaut d'un profil (le marqué par défaut, sinon le premier). */
export function defaultSender(p: Integration | undefined): string {
  const senders = p?.email_provider?.senders ?? []
  return (senders.find((s) => s.is_default) ?? senders[0])?.email ?? ''
}

/** Copie dans le presse-papiers ; false si le navigateur refuse (jamais d'exception, jamais de journal). */
export async function copyText(text: string): Promise<boolean> {
  try {
    await navigator.clipboard.writeText(text)
    return true
  } catch {
    return false
  }
}

/** Accès vivants d'abord (le plus récent en tête), révoqués à part. */
export function splitAccesses(list: MailboxAccess[]): { live: MailboxAccess[]; revoked: MailboxAccess[] } {
  const byDate = (a: MailboxAccess, b: MailboxAccess) => b.created_at.localeCompare(a.created_at)
  return {
    live: list.filter((a) => accessState(a) !== 'revoked').sort(byDate),
    revoked: list.filter((a) => accessState(a) === 'revoked').sort(byDate)
  }
}

/** Il y a au moins un accès qui devrait pouvoir se connecter (sinon « aucun essai » est normal). */
export function hasOpenAccess(list: MailboxAccess[]): boolean {
  return list.some((a) => accessState(a) === 'active')
}

/** Ports à dire quand aucun essai n'est arrivé : « 993 », ou « 993 / 465 » selon ce que le serveur annonce. */
export function portsOf(c: MailboxConnection): { imap?: number; smtp?: number } {
  return { imap: c.imap?.port, smtp: c.smtp?.port }
}
