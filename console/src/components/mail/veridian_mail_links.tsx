import React from 'react'
import { Link } from '@tanstack/react-router'
import { useLingui } from '@lingui/react/macro'

import type { MailLink } from '../../services/api/veridian_mail'

// Liens vers ce qui a produit le mail, côté console : le journal d'envoi (filtre par identifiant),
// la page Séquences, la page Campagnes. Le nœud n'a pas de page propre : il reste un repère.

export const VeridianMailOriginLinks: React.FC<{ workspaceId: string; link: MailLink }> = ({ workspaceId, link }) => {
  const { t } = useLingui()
  const items: React.ReactNode[] = []
  if (link.sent_message_id) {
    items.push(
      <Link
        key="sent"
        to="/console/workspace/$workspaceId/logs"
        params={{ workspaceId }}
        search={{ type: 'commercial', id: link.sent_message_id } as never}
      >
        {t`Original send in the sending log`}
      </Link>
    )
  }
  if (link.automation_id) {
    items.push(
      <span key="seq">
        <Link to="/console/workspace/$workspaceId/automations" params={{ workspaceId }}>
          {t`Sequence`}
        </Link>
        {' '}
        <code title={t`Sequence identifier`}>{link.automation_id}</code>
        {link.node_id && (
          <>
            {' · '}
            {t`node`} <code title={t`Node identifier`}>{link.node_id}</code>
          </>
        )}
      </span>
    )
  }
  if (link.broadcast_id) {
    items.push(
      <span key="bc">
        <Link to="/console/workspace/$workspaceId/broadcasts" params={{ workspaceId }}>
          {t`Broadcast`}
        </Link>{' '}
        <code>{link.broadcast_id}</code>
      </span>
    )
  }
  if (items.length === 0) return <span>—</span>
  return (
    <span style={{ display: 'inline-flex', flexWrap: 'wrap', gap: '4px 16px' }}>
      {items.map((node, i) => (
        <React.Fragment key={i}>{node}</React.Fragment>
      ))}
    </span>
  )
}
