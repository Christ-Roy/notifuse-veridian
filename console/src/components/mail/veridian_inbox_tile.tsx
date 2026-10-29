import React from 'react'
import { Col, Statistic, theme } from 'antd'
import { Link } from '@tanstack/react-router'
import { useLingui } from '@lingui/react/macro'

import { exactDate, relativeDate } from './veridian_mail_labels'
import { useLastUnhandledReply, useUnhandledReplies } from './veridian_mail_queries'
import { counterpart, messageDate } from './veridian_mail_rules'

// Tuile du tableau de bord : « Réponses à traiter : N » + la dernière réponse, lien vers la boîte
// filtrée sur « à traiter ». Ne s'affiche pas tant que la boîte ne répond pas (route absente, droit refusé).
export const VeridianInboxTile: React.FC<{ workspaceId: string }> = ({ workspaceId }) => {
  const { t } = useLingui()
  const { token } = theme.useToken()
  const { count, loaded } = useUnhandledReplies(workspaceId)
  const last = useLastUnhandledReply(workspaceId, loaded)
  if (!loaded) return null
  const reply = last.data
  const who = reply ? counterpart(reply) : ''
  const when = reply ? relativeDate(messageDate(reply)) : ''

  return (
    <Col xs={24} md={12} xl={8}>
      <Link
        to="/console/workspace/$workspaceId/inbox"
        params={{ workspaceId }}
        search={{ nature: 'reponses', etat: 'a_traiter' } as never}
        data-testid="tile-inbox"
        aria-label={count === 1 ? t`Replies to handle: 1. Open the inbox.` : t`Replies to handle: ${count}. Open the inbox.`}
        className="focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-[#7763f1]"
        style={{ display: 'block', minHeight: 110, padding: 16, borderRadius: 8, background: token.colorFillSecondary, color: 'inherit' }}
      >
        <Statistic title={t`Replies to handle`} value={count} valueStyle={{ fontSize: '22px', fontWeight: 'bold' }} formatter={(v) => String(v)} />
        <div style={{ marginTop: 4, fontSize: 12.5, color: token.colorTextSecondary, overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' }} data-testid="tile-inbox-last">
          {reply ? (
            <span title={exactDate(messageDate(reply))}>
              {t`Latest: ${who} · ${when}`}
            </span>
          ) : (
            t`No reply waiting`
          )}
        </div>
      </Link>
    </Col>
  )
}
