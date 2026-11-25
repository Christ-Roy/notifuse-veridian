import type { Integration } from '../../services/api/workspace'
import {
  accessState,
  securityCode,
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

/** Laissé à la personne pour régler son application avant de lui dire que rien n'arrive. */
export const SETUP_GRACE_MS = 10 * 60 * 1000

/** Un accès ouvert depuis assez longtemps devrait avoir essayé (sinon « aucun essai » est normal). */
export function expectsAttempts(list: MailboxAccess[], now: number): boolean {
  return list.some((a) => accessState(a) === 'active' && now - Date.parse(a.created_at) > SETUP_GRACE_MS)
}

/** Réception et envoi sont en SSL/TLS : c'est alors la seule chose à dire. */
export function allSSL(c: MailboxConnection): boolean {
  return [c.imap, c.smtp].every((e) => !e || securityCode(e.security) === 'ssl')
}

/** Ports SSL/TLS à dire quand aucun essai n'est arrivé (un côté en STARTTLS n'est pas dit ici). */
export function portsOf(c: MailboxConnection): { imap?: number; smtp?: number } {
  const ssl = (e: MailboxConnection['imap']) => (e && securityCode(e.security) === 'ssl' ? e.port : undefined)
  return { imap: ssl(c.imap), smtp: ssl(c.smtp) }
}
