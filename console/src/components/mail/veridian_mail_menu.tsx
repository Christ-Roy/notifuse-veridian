import React from 'react'
import { Badge } from 'antd'
import { Link } from '@tanstack/react-router'
import { useLingui } from '@lingui/react/macro'

import { useUnhandledReplies } from './veridian_mail_queries'

// Entrée « Boîte de réception » du menu, avec le nombre de réponses humaines à traiter.
export const VeridianInboxMenuLabel: React.FC<{ workspaceId: string }> = ({ workspaceId }) => {
  const { t } = useLingui()
  const { count } = useUnhandledReplies(workspaceId)
  return (
    <Link
      to="/console/workspace/$workspaceId/inbox"
      params={{ workspaceId }}
      aria-label={count > 0 ? (count === 1 ? t`Inbox, 1 reply to handle` : t`Inbox, ${count} replies to handle`) : t`Inbox`}
      style={{ display: 'flex', alignItems: 'center', justifyContent: 'space-between', gap: 8 }}
    >
      <span>{t`Inbox`}</span>
      {count > 0 && <Badge count={count} overflowCount={99} size="small" color="#7763F1" data-testid="inbox-badge" />}
    </Link>
  )
}
