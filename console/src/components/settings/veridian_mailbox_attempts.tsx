import { Alert, Button, Tag, theme, Tooltip, Typography } from 'antd'
import { ReloadOutlined } from '@ant-design/icons'
import { useLingui } from '@lingui/react/macro'

import dayjs from '../../lib/dayjs'
import type { MailboxAttempt, MailboxConnection } from '../../services/api/veridian_mailbox_access'
import { useMailboxLabels } from './veridian_mailbox_labels'
import { portsOf } from './veridian_mailbox_rules'

const { Text } = Typography

interface Props {
  attempts: MailboxAttempt[]
  /** Début de la fenêtre observée par le serveur. */
  since: string
  connection: MailboxConnection
  /** Il existe un accès ouvert : l'absence d'essai veut alors dire que l'application ne nous atteint pas. */
  expectAttempts: boolean
  refreshing: boolean
  onRefresh: () => void
}

// « Recent attempts » : ce que le serveur a vu arriver. Aucun essai depuis `since` = l'application ne nous
// atteint pas (STARTTLS, mauvais port) : on le dit avec le port à utiliser plutôt que d'afficher une liste vide.
export function VeridianMailboxAttempts({ attempts, since, connection, expectAttempts, refreshing, onRefresh }: Props) {
  const { t } = useLingui()
  const labels = useMailboxLabels()
  const { token } = theme.useToken()

  const { imap, smtp } = portsOf(connection)
  const sinceText = dayjs(since).format('lll')
  let ports = t`the port shown in the setup steps`
  if (imap && smtp && imap !== smtp) ports = t`${imap} to receive, ${smtp} to send`
  else if (imap || smtp) ports = String(imap ?? smtp)

  return (
    <section aria-labelledby="mbx-attempts-title" style={{ marginTop: 32 }}>
      <div style={{ display: 'flex', alignItems: 'center', justifyContent: 'space-between', gap: 8, flexWrap: 'wrap' }}>
        <h3 id="mbx-attempts-title" style={{ margin: 0 }}>
          {t`Recent attempts`}
        </h3>
        <Button size="small" icon={<ReloadOutlined />} loading={refreshing} onClick={onRefresh}>
          {t`Refresh`}
        </Button>
      </div>
      {attempts.length === 0 ? (
        expectAttempts ? (
          <Alert
            style={{ marginTop: 12 }}
            type="warning"
            showIcon
            message={t`No attempt reached us since ${sinceText}: your app is probably set to STARTTLS or to another port. Use SSL/TLS, port ${ports}.`}
          />
        ) : (
          <Text type="secondary" style={{ display: 'block', marginTop: 12 }}>
            {t`No attempt since ${sinceText}.`}
          </Text>
        )
      ) : (
        <ul style={{ listStyle: 'none', padding: 0, margin: '12px 0 0' }}>
          {attempts.slice(0, 20).map((a, i) => (
            <li
              key={`${a.at}-${a.via}-${i}`}
              data-ok={a.ok ? 'true' : 'false'}
              style={{
                display: 'flex',
                flexWrap: 'wrap',
                gap: '4px 12px',
                alignItems: 'center',
                padding: '8px 0',
                borderBottom: `1px solid ${token.colorBorderSecondary}`
              }}
            >
              <Tooltip title={dayjs(a.at).format('lll')}>
                <Text type="secondary">{dayjs(a.at).fromNow()}</Text>
              </Tooltip>
              <Tag style={{ margin: 0 }}>{labels.attemptVia(a.via)}</Tag>
              <Text style={{ overflowWrap: 'anywhere' }}>{a.address}</Text>
              <Tag color={a.ok ? 'success' : 'error'} style={{ margin: 0, whiteSpace: 'normal' }}>
                {labels.attemptResult(a)}
              </Tag>
            </li>
          ))}
        </ul>
      )}
    </section>
  )
}
