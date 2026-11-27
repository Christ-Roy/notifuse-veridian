// Issue d'un mail écrit à la main (27/11/2026, Robert : « comme une vraie boîte mail, il part tout de suite »).
// L'API attend l'issue réelle : « sent » (le relais l'a accepté), « failed » (refusé, avec la raison du relais),
// « sending » (pas encore d'issue après l'attente ; il reste en file, jamais perdu).
import { Alert } from 'antd'
import { useLingui } from '@lingui/react/macro'
import type { MailSendResult } from '../../services/api/veridian_mail'

export function MailSendOutcome({
  result,
  testId,
  style,
  onClose
}: {
  result: MailSendResult
  testId: string
  style?: React.CSSProperties
  onClose?: () => void
}) {
  const { t } = useLingui()
  const closable = onClose !== undefined
  if (result.status === 'sent') {
    return (
      <Alert
        type="success"
        showIcon
        data-testid={testId}
        data-outcome="sent"
        style={style}
        message={t`Sent`}
        description={t`The relay accepted it; it is in the conversation.`}
        closable={closable}
        onClose={onClose}
      />
    )
  }
  if (result.status === 'failed') {
    return (
      <Alert
        type="error"
        showIcon
        role="alert"
        data-testid={testId}
        data-outcome="failed"
        style={style}
        message={t`The relay refused the message`}
        description={result.error || t`No reason given by the relay.`}
        closable={closable}
        onClose={onClose}
      />
    )
  }
  return (
    <Alert
      type="info"
      showIcon
      data-testid={testId}
      data-outcome="sending"
      style={style}
      message={t`Sending in progress`}
      description={t`It will appear in the conversation once the relay has accepted it.`}
      closable={closable}
      onClose={onClose}
    />
  )
}
