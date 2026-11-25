import { useState } from 'react'
import { Alert, Button, Collapse, Drawer, Tabs, theme, Typography } from 'antd'
import { useLingui } from '@lingui/react/macro'

import dayjs from '../../lib/dayjs'
import {
  mailboxAccessService,
  securityCode,
  type MailboxAccess,
  type MailboxConnection,
  type MailboxProfileLink
} from '../../services/api/veridian_mailbox_access'
import { VeridianMailboxCheck } from './veridian_mailbox_check'
import { APPLE_APPS, MAILBOX_APPS, useMailboxLabels, type MailboxApp } from './veridian_mailbox_labels'
import { qrFits } from './veridian_mailbox_qr'
import { QrCode, ValueRow } from './veridian_mailbox_parts'
import { allSSL } from './veridian_mailbox_rules'

const { Text, Paragraph, Title } = Typography

interface Props {
  open: boolean
  onClose: () => void
  workspaceId: string
  access: MailboxAccess | null
  connection: MailboxConnection
  /** Le mot de passe, seulement juste après la création (il n'est plus jamais renvoyé ensuite). */
  password?: string
  /** Appelé après un test de connexion : l'écran relit les essais récents. */
  onTested?: () => void
}

export function VeridianMailboxSetup({ open, onClose, workspaceId, access, connection, password, onTested }: Props) {
  const { t } = useLingui()
  return (
    <Drawer
      open={open}
      onClose={onClose}
      width="min(560px, 100vw)"
      title={t`Set up my app`}
      destroyOnHidden
      styles={{ body: { padding: 16 } }}
    >
      {access && (
        <Tabs
          items={[
            {
              key: 'setup',
              label: t`Set up`,
              children: (
                <SetupSteps
                  workspaceId={workspaceId}
                  access={access}
                  connection={connection}
                  password={password}
                />
              )
            },
            {
              key: 'check',
              label: t`Check my settings`,
              children: <VeridianMailboxCheck workspaceId={workspaceId} access={access} onTested={onTested} />
            }
          ]}
        />
      )}
    </Drawer>
  )
}

function SetupSteps({
  workspaceId,
  access,
  connection,
  password
}: {
  workspaceId: string
  access: MailboxAccess
  connection: MailboxConnection
  password?: string
}) {
  const { t } = useLingui()
  const labels = useMailboxLabels()
  const [app, setApp] = useState<MailboxApp | null>(null)

  return (
    <div>
      <Title level={5} style={{ marginTop: 0 }} id="mbx-app-title">
        {t`Which app do you use?`}
      </Title>
      <div role="group" aria-labelledby="mbx-app-title" style={{ display: 'flex', flexWrap: 'wrap', gap: 8 }}>
        {MAILBOX_APPS.map((a) => (
          <Button key={a} type={app === a ? 'primary' : 'default'} aria-pressed={app === a} onClick={() => setApp(a)}>
            {labels.app(a)}
          </Button>
        ))}
      </div>

      {app && (
        <div style={{ marginTop: 16 }}>
          <Alert
            type="info"
            message={t`Three things to get right`}
            description={
              <ul style={{ margin: 0, paddingLeft: 18, listStyle: 'disc' }}>
                <li>
                  {allSSL(connection)
                    ? t`Choose SSL/TLS, not STARTTLS, for receiving AND for sending.`
                    : t`Choose the security setting shown below, for receiving AND for sending.`}
                </li>
                <li>{t`Use the same password for receiving and for sending.`}</li>
                <li>{t`The username is your whole address.`}</li>
              </ul>
            }
          />
          {APPLE_APPS.includes(app) ? (
            <>
              <AppleProfile workspaceId={workspaceId} access={access} app={app} />
              <Collapse
                ghost
                style={{ marginTop: 8 }}
                items={[
                  {
                    key: 'manual',
                    label: t`Enter the values by hand instead`,
                    children: (
                      <ManualSteps app={app} access={access} connection={connection} password={password} />
                    )
                  }
                ]}
              />
            </>
          ) : (
            <ManualSteps app={app} access={access} connection={connection} password={password} />
          )}
        </div>
      )}
    </div>
  )
}

function AppleProfile({
  workspaceId,
  access,
  app
}: {
  workspaceId: string
  access: MailboxAccess
  app: MailboxApp
}) {
  const { t } = useLingui()
  const labels = useMailboxLabels()
  const { token } = theme.useToken()
  const [link, setLink] = useState<MailboxProfileLink | null>(null)
  const [loading, setLoading] = useState(false)
  const [error, setError] = useState('')
  const until = link ? dayjs(link.expires_at).format('HH:mm') : ''

  const fetchLink = async () => {
    setLoading(true)
    setError('')
    try {
      setLink(await mailboxAccessService.profileLink(workspaceId, access.id))
    } catch (err) {
      setError(labels.errorOf(err))
    } finally {
      setLoading(false)
    }
  }

  return (
    <div style={{ marginTop: 16 }}>
      <Paragraph>
        {app === 'iphone'
          ? t`A configuration profile sets everything up for you. Open it on your iPhone or iPad, then go to Settings > Profile Downloaded > Install. The device will ask for the password.`
          : t`A configuration profile sets everything up for you. Open it on your Mac, then go to System Settings > Privacy & Security > Profiles > Install. The Mac will ask for the password.`}
      </Paragraph>
      {error && <Alert type="error" showIcon message={error} style={{ marginBottom: 12 }} role="alert" />}
      {!link ? (
        <Button type="primary" loading={loading} onClick={fetchLink}>
          {t`Get my configuration profile`}
        </Button>
      ) : (
        <div
          style={{
            display: 'flex',
            flexWrap: 'wrap',
            gap: 16,
            alignItems: 'center',
            padding: 12,
            border: `1px solid ${token.colorBorderSecondary}`,
            borderRadius: 8
          }}
        >
          <div style={{ flex: '1 1 200px' }}>
            <Button type="primary" href={link.url} target="_blank" rel="noopener noreferrer">
              {t`Open on this device`}
            </Button>
            <div style={{ marginTop: 8 }}>
              <Text type="secondary">
                {t`The link works until ${until}.`}
              </Text>
            </div>
            <Button type="link" style={{ paddingLeft: 0 }} loading={loading} onClick={fetchLink}>
              {t`Get a new link`}
            </Button>
          </div>
          {qrFits(link.url) && (
            <div style={{ flex: '0 1 auto', textAlign: 'center' }}>
              <QrCode value={link.url} label={t`QR code to open the configuration profile on your phone`} />
              <div>
                <Text type="secondary">{t`Or scan this code with your phone camera.`}</Text>
              </div>
            </div>
          )}
        </div>
      )}
    </div>
  )
}

function ManualSteps({
  app,
  access,
  connection,
  password
}: {
  app: MailboxApp
  access: MailboxAccess
  connection: MailboxConnection
  password?: string
}) {
  const { t } = useLingui()
  const labels = useMailboxLabels()
  const { imap, smtp } = connection
  return (
    <div style={{ marginTop: 12 }}>
      <ol style={{ paddingLeft: 20, margin: '0 0 12px', listStyle: 'decimal' }}>
        <li>{labels.appStart(app)}</li>
        <li>{t`Fill in the values below, one by one (the copy buttons help).`}</li>
      </ol>

      <Title level={5}>{t`Receiving (IMAP)`}</Title>
      {imap ? (
        <>
          <ValueRow label={t`Server`} value={imap.host} />
          <ValueRow label={t`Port`} value={String(imap.port)} />
          <ValueRow label={t`Security`} value={labels.security(securityCode(imap.security))} />
        </>
      ) : (
        <Text type="secondary">{t`Receiving is not available for this workspace yet.`}</Text>
      )}

      {access.can_send && (
        <>
          <Title level={5} style={{ marginTop: 16 }}>
            {t`Sending (SMTP)`}
          </Title>
          {smtp ? (
            <>
              <ValueRow label={t`Server`} value={smtp.host} />
              <ValueRow label={t`Port`} value={String(smtp.port)} />
              <ValueRow label={t`Security`} value={labels.security(securityCode(smtp.security))} />
            </>
          ) : (
            <Text type="secondary">{t`Sending is not available for this workspace yet.`}</Text>
          )}
        </>
      )}

      <Title level={5} style={{ marginTop: 16 }}>
        {t`Login (receiving and sending)`}
      </Title>
      <ValueRow label={t`Username`} value={access.address} />
      {password ? (
        <ValueRow label={t`Password`} value={password} secret />
      ) : (
        <Paragraph type="secondary" style={{ marginTop: 8 }}>
          {t`Password: the one shown when this access was created. It cannot be shown again. If you lost it, create a new access.`}
        </Paragraph>
      )}
    </div>
  )
}
