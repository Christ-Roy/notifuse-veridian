// LOT 1 : libellés de la page « File d'envoi », posés dans UN hook (le `t` vient de
// useLingui dans le corps du hook : l'extracteur Lingui le capte).

import { useLingui } from '@lingui/react/macro'
import { i18n } from '@lingui/core'

export function formatDateTime(iso: string | null | undefined): string {
  if (!iso) return '—'
  const d = new Date(iso)
  if (Number.isNaN(d.getTime())) return iso
  return new Intl.DateTimeFormat(i18n.locale || 'en', { dateStyle: 'short', timeStyle: 'short' }).format(d)
}

export function useQueueLabels() {
  const { t } = useLingui()

  const reasonLabel = (code: string): string => {
    switch (code) {
      case 'not_examined':
        return t`Not examined yet`
      case 'window_closed':
        return t`Sending window closed`
      case 'capacity':
        return t`Daily capacity reached`
      case 'class_rate':
        return t`Recipient provider rate limit`
      case 'reputation_stopped':
        return t`Reputation fuse tripped`
      case 'excluded_class':
        return t`Excluded class`
      case 'profile_paused':
        return t`Profile paused`
      case 'no_profile_in_pool':
        return t`No profile in the pool`
      case 'circuit_open':
        return t`Circuit open`
      case 'anchor_wait':
        return t`Follow-up waiting for its original sender`
      case 'quota_denied':
        return t`Quota denied`
      case 'render_failed':
        return t`Render failed`
      case 'guard_retry':
        return t`Safety check, retrying`
      case 'automation_paused':
        return t`Automation paused`
      case 'send_error':
        return t`Send error`
      case 'deferred_legacy':
        return t`Deferred (reason not recorded)`
      case '':
        return t`No reason`
      default:
        return code
    }
  }

  // Détail dominant d'un groupe : pour « capacité », le plafond qui a joué.
  const reasonDetailLabel = (reason: string, detail: string): string => {
    if (!detail) return ''
    if (reason === 'capacity') {
      switch (detail) {
        case 'warmup':
          return t`Warm-up cap`
        case 'provider_class':
          return t`Provider class cap`
        case 'per_recipient':
          return t`Per-recipient cap`
        case 'per_sender':
          return t`Per-sender cap`
        default:
          return detail
      }
    }
    return detail
  }

  const gateLabel = (gate: string): string => {
    switch (gate) {
      case 'excluded':
        return t`Excluded class`
      case 'reputation':
        return t`Reputation fuse`
      case 'class_rate':
        return t`Provider rate`
      case 'daily_cap':
        return t`Daily cap`
      case 'sender_cap':
        return t`Sender cap`
      case 'window':
        return t`Sending window`
      default:
        return gate
    }
  }

  const verdictLabel = (verdict: string): string => {
    switch (verdict) {
      case 'pass':
        return t`Pass`
      case 'block':
        return t`Blocked`
      case 'slowed':
        return t`Slowed`
      case 'skipped':
        return t`Skipped`
      default:
        return verdict
    }
  }

  const candidateOutcomeLabel = (outcome: string): string => {
    switch (outcome) {
      case 'selected':
        return t`Selected`
      case 'blocked':
        return t`Blocked`
      case 'excluded':
        return t`Excluded`
      case 'paused':
        return t`Paused`
      case 'circuit_open':
        return t`Circuit open`
      case 'skipped':
        return t`Skipped`
      default:
        return outcome
    }
  }

  const decisionOutcomeLabel = (outcome: string): string => {
    switch (outcome) {
      case 'sent':
        return t`Sent`
      case 'deferred':
        return t`Deferred`
      case 'failed':
        return t`Failed`
      case 'discarded':
        return t`Discarded`
      case 'exited':
        return t`Contact exited`
      case 'recomputed':
        return t`Recomputed`
      default:
        return outcome
    }
  }

  const entryStatusLabel = (status: string): string => {
    switch (status) {
      case 'pending':
        return t`Pending`
      case 'processing':
        return t`Processing`
      case 'failed':
        return t`Failed`
      case 'sent':
        return t`Sent`
      default:
        return status
    }
  }

  return {
    reasonLabel,
    reasonDetailLabel,
    gateLabel,
    verdictLabel,
    candidateOutcomeLabel,
    decisionOutcomeLabel,
    entryStatusLabel
  }
}
