import { useLingui } from '@lingui/react/macro'

import type { TransactionalWatch, TransactionalWatchAlert } from '../../services/api/veridian_email_profiles'
import { formatNumber, formatPercent } from '../sending_profiles/veridian_profile_labels'

// Textes de la surveillance du profil transactionnel (lot 5). Dans un fichier à part :
// un fichier de composants n'exporte que des composants (rechargement à chaud).
export function useWatchTexts() {
  const { t } = useLingui()
  const alertText = (alert: TransactionalWatchAlert, watch: TransactionalWatch): string => {
    switch (alert.code) {
      case 'volume_spike': {
        const sent = formatNumber(watch.sent_today)
        const average = formatNumber(Math.round(watch.baseline_per_day))
        return t`Unusual volume: ${sent} mails today, ${average} per day on average over the previous 7 days`
      }
      case 'hard_bounce_rate': {
        const rate = formatPercent(alert.value)
        return t`Hard bounces: ${rate} over 7 days`
      }
      case 'complaint_rate': {
        const rate = formatPercent(alert.value)
        return t`Spam complaints: ${rate} over 7 days`
      }
      case 'policy_refusal_rate': {
        const rate = formatPercent(alert.value)
        return t`Policy refusals: ${rate} over 7 days`
      }
      default:
        return alert.message
    }
  }
  const levelLabel = (level: string): string => {
    switch (level) {
      case 'ok':
        return t`Healthy`
      case 'watch':
        return t`To watch`
      case 'alert':
        return t`Alert`
      default:
        return t`Not measured`
    }
  }
  return { alertText, levelLabel }
}
