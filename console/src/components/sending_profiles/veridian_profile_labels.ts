// Lot 3 : libellés de la page Profils d'envoi, posés dans UN hook (le `t` vient de
// useLingui dans le corps du hook : l'extracteur Lingui le capte, contrairement à un
// `t` passé en paramètre d'une fonction hors composant, qui rend vide en runtime).

import { useLingui } from '@lingui/react/macro'
import { i18n } from '@lingui/core'

import type { EmailProfileType } from '../../services/api/veridian_email_profiles'
import type { Blocker, LimitingFactor, ReputationIssue } from './veridian_profile_rules'

export function formatPercent(rate: number): string {
  return new Intl.NumberFormat(i18n.locale || 'en', {
    style: 'percent',
    maximumFractionDigits: 1
  }).format(rate)
}

export function formatNumber(value: number): string {
  return new Intl.NumberFormat(i18n.locale || 'en').format(value)
}

export function useProfileLabels() {
  const { t } = useLingui()

  const typeLabel = (type: EmailProfileType): string => {
    switch (type) {
      case 'smtp':
        return t`SMTP`
      case 'gmail_app_password':
        return t`Gmail, app password`
      case 'gmail_oauth':
        return t`Gmail, OAuth`
      case 'ses':
        return t`Amazon SES`
      case 'sparkpost':
        return t`SparkPost`
      case 'postmark':
        return t`Postmark`
      case 'mailgun':
        return t`Mailgun`
      case 'mailjet':
        return t`Mailjet`
      case 'sendgrid':
        return t`SendGrid`
      default:
        return String(type)
    }
  }

  const classLabel = (cls: string): string => {
    switch (cls) {
      case 'google':
        return t`Google`
      case 'microsoft':
        return t`Microsoft`
      case 'yahoo_aol':
        return t`Yahoo / AOL`
      case 'freemail_fr':
        return t`French ISPs`
      case 'corporate':
        return t`Corporate (legacy)`
      case 'ovh':
        return t`OVH`
      case 'ionos':
        return t`IONOS / 1&1`
      case 'apple_icloud':
        return t`Apple iCloud`
      case 'security_gateway':
        return t`Anti-spam gateways`
      case 'other_hoster':
        return t`Other hosters`
      case 'infomaniak':
        return t`Infomaniak`
      case 'gandi':
        return t`Gandi`
      case 'hostinger':
        return t`Hostinger / Titan`
      case 'o2switch':
        return t`o2switch`
      case 'lws':
        return t`LWS`
      case 'scaleway':
        return t`Scaleway / Online`
      case 'website_builder':
        return t`Website builders`
      case 'corporate_selfhost':
        return t`Self-hosted corporate`
      default:
        return cls
    }
  }

  const weekdayLabel = (weekday: number): string => {
    switch (weekday) {
      case 0:
        return t`Sunday`
      case 1:
        return t`Monday`
      case 2:
        return t`Tuesday`
      case 3:
        return t`Wednesday`
      case 4:
        return t`Thursday`
      case 5:
        return t`Friday`
      default:
        return t`Saturday`
    }
  }

  const factorText = (factor: LimitingFactor): string => {
    switch (factor.kind) {
      case 'warmup': {
        const day = factor.day
        const of = factor.of
        return t`Warmup, day ${day}/${of}`
      }
      case 'profile_cap':
        return t`Profile cap`
      case 'per_sender':
        return t`Per sending address cap`
      case 'class_cap':
        return t`Per provider caps`
      default:
        return t`No daily cap`
    }
  }

  const blockerText = (blocker: Blocker): string => {
    switch (blocker.kind) {
      case 'paused':
        return t`Paused`
      case 'unverified':
        return t`Not verified: send a test first`
      case 'not_in_rotation':
        return t`Not in the rotation`
      case 'reputation_stopped':
        return t`Sending stopped for at least one provider`
      default: {
        const reopening = blocker.reopening
        if (!reopening) return t`Sending window closed`
        const time = reopening.time
        if (reopening.daysAhead === 0) return t`Window closed until ${time}`
        const day = weekdayLabel(reopening.weekday)
        return t`Window closed until ${day} ${time}`
      }
    }
  }

  const reasonText = (issue: ReputationIssue): string => {
    const rate = issue.rate === null ? '' : formatPercent(issue.rate)
    switch (issue.reason) {
      case 'hard_bounce_rate':
        return t`${rate} bounces`
      case 'policy_refusal_rate':
        return t`${rate} policy refusals`
      case 'complaint':
        return t`spam complaint received`
      case 'bulk_policy_refusal':
        return t`the provider refuses in bulk`
      default:
        return t`reputation signal`
    }
  }

  const reputationText = (issue: ReputationIssue): string => {
    const name = classLabel(issue.class)
    const reason = reasonText(issue)
    if (issue.stopped) return t`${name}: sending stopped, ${reason}`
    const factor = issue.factor
    return t`${name}: rate ÷${factor}, ${reason}`
  }

  return { typeLabel, classLabel, weekdayLabel, factorText, blockerText, reasonText, reputationText }
}
