import React from 'react'
import { Collapse, theme, Tooltip } from 'antd'
import { useLingui } from '@lingui/react/macro'

import type { Mailbox, MailMessage } from '../../services/api/veridian_mail'
import { VeridianMailOriginLinks } from './veridian_mail_links'
import { exactDate, MAIL_TIMEZONE, relativeDate } from './veridian_mail_labels'
import { displayName, flattenHeaders, mailboxOf, messageDate, parseAddress } from './veridian_mail_rules'

const Person: React.FC<{ raw: string; name?: string }> = ({ raw, name }) => {
  const { token } = theme.useToken()
  const a = parseAddress(raw)
  const shown = displayName(raw, name)
  return (
    <span style={{ overflowWrap: 'anywhere' }}>
      <strong>{shown}</strong>
      {a.email && shown !== a.email && <span style={{ color: token.colorTextSecondary }}>{` <${a.email}>`}</span>}
    </span>
  )
}

// En-tête lisible d'un message : De / À / Cc, date exacte et relative, boîte et profil, origine
// de l'envoi (journal, séquence, nœud) et, repliés, tous les en-têtes bruts.
export const VeridianMailHeader: React.FC<{
  workspaceId: string
  message: MailMessage
  mailboxes: readonly Mailbox[]
  timezone?: string
}> = ({ workspaceId, message, mailboxes, timezone = MAIL_TIMEZONE }) => {
  const { t } = useLingui()
  const { token } = theme.useToken()
  const mailbox = mailboxOf(message.profile_id, mailboxes)
  const when = messageDate(message)
  const headers = flattenHeaders(message.headers)
  const matchText =
    message.link?.match_type === 'message_id'
      ? t`matched by message reference`
      : message.link?.match_type === 'sender'
        ? t`matched by known sender`
        : message.link?.match_type === 'subject'
          ? t`matched by subject`
          : ''

  const rows: Array<{ label: string; node: React.ReactNode; key: string }> = [
    { key: 'from', label: t`From`, node: <Person raw={message.from} name={message.from_name} /> },
    {
      key: 'to',
      label: t`To`,
      node: (
        <span style={{ display: 'inline-flex', flexWrap: 'wrap', gap: '2px 12px' }}>
          {message.to.map((a, i) => (
            <Person key={`${a}-${i}`} raw={a} />
          ))}
        </span>
      )
    }
  ]
  if (message.cc && message.cc.length > 0) {
    rows.push({
      key: 'cc',
      label: t`Cc`,
      node: (
        <span style={{ display: 'inline-flex', flexWrap: 'wrap', gap: '2px 12px' }}>
          {message.cc.map((a, i) => (
            <Person key={`${a}-${i}`} raw={a} />
          ))}
        </span>
      )
    })
  }
  rows.push({
    key: 'date',
    label: message.direction === 'out' ? t`Sent` : t`Received`,
    node: (
      <span>
        <span data-testid="header-exact-date">{exactDate(when, timezone)}</span>
        <span style={{ color: token.colorTextSecondary }}>{` · ${relativeDate(when)}`}</span>
      </span>
    )
  })
  rows.push({
    key: 'mailbox',
    label: t`Mailbox`,
    node: mailbox ? (
      <span>
        <strong>{mailbox.profile_name || mailbox.id}</strong>
        {mailbox.address && <span style={{ color: token.colorTextSecondary }}>{` · ${mailbox.address}`}</span>}
      </span>
    ) : message.profile_id ? (
      <Tooltip title={t`Profile identifier`}>
        <code>{message.profile_id}</code>
      </Tooltip>
    ) : (
      <span style={{ color: token.colorTextSecondary }}>—</span>
    )
  })
  if (message.link && (message.link.sent_message_id || message.link.automation_id || message.link.broadcast_id)) {
    rows.push({
      key: 'origin',
      label: t`Origin`,
      node: (
        <span>
          <VeridianMailOriginLinks workspaceId={workspaceId} link={message.link} />
          {matchText && <span style={{ color: token.colorTextSecondary, display: 'block', fontSize: 12 }}>{matchText}</span>}
        </span>
      )
    })
  }

  return (
    <section aria-label={t`Message header`} data-testid="mail-header">
      <dl style={{ display: 'grid', gridTemplateColumns: 'max-content minmax(0, 1fr)', gap: '8px 16px', margin: 0, fontSize: 14 }}>
        {rows.map((r) => (
          <React.Fragment key={r.key}>
            <dt style={{ color: token.colorTextSecondary, margin: 0 }}>{r.label}</dt>
            <dd style={{ margin: 0 }}>{r.node}</dd>
          </React.Fragment>
        ))}
      </dl>
      {headers.length > 0 && (
        <Collapse
          ghost
          size="small"
          style={{ marginTop: 8, marginLeft: -12 }}
          items={[
            {
              key: 'headers',
              label: t`Full headers (${headers.length})`,
              children: (
                <table data-testid="raw-headers" style={{ width: '100%', borderCollapse: 'collapse', fontSize: 12.5 }}>
                  <tbody>
                    {headers.map((h, i) => (
                      <tr key={`${h.name}-${i}`} style={{ borderTop: `1px solid ${token.colorBorderSecondary}` }}>
                        <th scope="row" style={{ textAlign: 'left', verticalAlign: 'top', padding: '4px 12px 4px 0', whiteSpace: 'nowrap', color: token.colorTextSecondary, fontWeight: 600 }}>
                          {h.name}
                        </th>
                        <td style={{ padding: '4px 0', fontFamily: 'ui-monospace, SFMono-Regular, Menlo, monospace', overflowWrap: 'anywhere' }}>{h.value}</td>
                      </tr>
                    ))}
                  </tbody>
                </table>
              )
            }
          ]}
        />
      )}
    </section>
  )
}
