// Boîte unifiée : textes de la page, posés dans des hooks (le `t` vient de useLingui dans le corps
// du hook : l'extracteur Lingui le capte). Les libellés de jalons et les raisons de rebond viennent
// du serveur, déjà en français : ils ne passent jamais par ici.

import { useLingui } from '@lingui/react/macro'
import { i18n } from '@lingui/core'

import dayjs from '../../lib/dayjs'
import type { MailErrorKind, MailKind } from '../../services/api/veridian_mail'
import { formatBytes, gapUnit, type Gap, type NatureFilter, type StateFilter } from './veridian_mail_rules'

export const MAIL_TIMEZONE = 'Europe/Paris'

/** Date exacte, localisée, dans le fuseau demandé. */
export function exactDate(iso: string | undefined, tz: string = MAIL_TIMEZONE): string {
  if (!iso) return '—'
  const d = dayjs(iso)
  if (!d.isValid()) return iso
  return d.tz(tz).format('LLL')
}

/** Date relative (« il y a 3 jours »). */
export function relativeDate(iso: string | undefined): string {
  if (!iso) return ''
  const d = dayjs(iso)
  return d.isValid() ? d.fromNow() : ''
}

/** Date courte de liste : l'heure aujourd'hui, le jour et le mois sinon, l'année si elle change. */
export function shortDate(iso: string | undefined, tz: string = MAIL_TIMEZONE, now: Date = new Date()): string {
  if (!iso) return ''
  const d = dayjs(iso)
  if (!d.isValid()) return ''
  const local = d.tz(tz)
  const ref = dayjs(now).tz(tz)
  if (local.isSame(ref, 'day')) return local.format('LT')
  if (local.isSame(ref, 'year')) return local.format('D MMM')
  return local.format('D MMM YYYY')
}

export function useMailLabels() {
  const { t } = useLingui()

  const kindLabel = (kind: MailKind | string): string => {
    switch (kind) {
      case 'sent':
        return t`Sent`
      case 'reply_human':
        return t`Human reply`
      case 'reply_auto':
        return t`Automatic reply`
      case 'bounce_hard':
        return t`Permanent bounce`
      case 'bounce_soft':
        return t`Temporary bounce`
      case 'dsn':
        return t`Delivery report`
      case 'other':
        return t`Other message`
      default:
        return String(kind)
    }
  }

  const natureLabel = (nature: NatureFilter): string => {
    switch (nature) {
      case 'reponses':
        return t`Replies`
      case 'auto':
        return t`Automatic replies`
      case 'rebonds':
        return t`Bounces`
      case 'envoyes':
        return t`Sent mails`
      case 'autres':
        return t`Others`
      default:
        return t`All messages`
    }
  }

  const stateLabel = (state: StateFilter): string => {
    switch (state) {
      case 'a_traiter':
        return t`To handle`
      case 'traites':
        return t`Handled`
      default:
        return t`All messages`
    }
  }

  const gapLabel = (gap: Gap | null): string => {
    if (!gap) return ''
    const { unit, value } = gapUnit(gap)
    if (unit === 'day') return value === 1 ? t`1 day after` : t`${value} days after`
    if (unit === 'hour') return value === 1 ? t`1 hour after` : t`${value} hours after`
    if (value <= 0) return t`right after`
    return value === 1 ? t`1 minute after` : t`${value} minutes after`
  }

  const errorText = (kind: MailErrorKind): { title: string; hint: string } => {
    switch (kind) {
      case 'session':
        return { title: t`Your session has expired`, hint: t`Sign in again to read your mail.` }
      case 'payment':
        return { title: t`This feature is suspended`, hint: t`The subscription of this workspace must be renewed.` }
      case 'forbidden':
        return { title: t`Access denied`, hint: t`You need read access to the sending log to open the inbox.` }
      case 'not_found':
        return { title: t`Message not found`, hint: t`It may have been removed. Go back to the list.` }
      case 'refused':
        return { title: t`Sending refused`, hint: t`The sending rules do not allow this mail right now.` }
      case 'invalid':
        return { title: t`Invalid request`, hint: t`Check your filters and try again.` }
      case 'rate':
        return { title: t`Too many open streams`, hint: t`Close another inbox tab and try again.` }
      case 'unavailable':
        return { title: t`The inbox is temporarily unavailable`, hint: t`The server is not ready. Retry in a moment.` }
      case 'network':
        return { title: t`Cannot reach the server`, hint: t`Check your connection, then retry.` }
      default:
        return { title: t`Something went wrong`, hint: t`Retry. If it persists, tell the team.` }
    }
  }

  const size = (bytes: number): string => formatBytes(bytes, i18n.locale || 'fr')

  return { kindLabel, natureLabel, stateLabel, gapLabel, errorText, size }
}
