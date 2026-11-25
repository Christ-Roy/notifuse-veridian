import { useState } from 'react'
import { Alert, Button, Input, Radio, Space, theme, Typography } from 'antd'
import { CheckCircleFilled, CloseCircleFilled } from '@ant-design/icons'
import { useLingui } from '@lingui/react/macro'

import {
  mailboxAccessService,
  mailboxErrorKind,
  type MailboxAccess,
  type MailboxSecurity,
  type MailboxTestResult,
  type MailboxVia
} from '../../services/api/veridian_mailbox_access'
import { useMailboxLabels } from './veridian_mailbox_labels'

const { Text } = Typography

// « Check my settings » : la personne recopie ce qu'elle a saisi dans SON application ; le serveur compare à ce
// qui est attendu et rend un contrôle par point. Le mot de passe saisi ne sert qu'à cet appel : il n'est ni
// gardé, ni journalisé, ni envoyé ailleurs.

interface SideValues {
  host: string
  port: string
  security: MailboxSecurity | ''
  username: string
  password: string
}

const EMPTY: SideValues = { host: '', port: '', security: '', username: '', password: '' }

type SideOutcome = { result: MailboxTestResult } | { error: string }

function portNumber(raw: string): number | null {
  if (!/^\d{1,5}$/.test(raw.trim())) return null
  const n = Number(raw)
  return n >= 1 && n <= 65535 ? n : null
}

interface Props {
  workspaceId: string
  access: MailboxAccess
  /** Appelé après un test (reçu ou non) : l'écran relit les essais récents. */
  onTested?: () => void
}

export function VeridianMailboxCheck({ workspaceId, access, onTested }: Props) {
  const { t } = useLingui()
  const { token } = theme.useToken()
  const labels = useMailboxLabels()
  const [values, setValues] = useState<Record<MailboxVia, SideValues>>({ imap: { ...EMPTY }, smtp: { ...EMPTY } })
  const [outcomes, setOutcomes] = useState<Partial<Record<MailboxVia, SideOutcome>>>({})
  const [running, setRunning] = useState(false)
  const [formError, setFormError] = useState('')

  const sides: MailboxVia[] = access.can_send ? ['imap', 'smtp'] : ['imap']
  const set = (via: MailboxVia, patch: Partial<SideValues>) =>
    setValues((v) => ({ ...v, [via]: { ...v[via], ...patch } }))

  const run = async () => {
    setFormError('')
    for (const via of sides) {
      const v = values[via]
      if (!v.host.trim() || portNumber(v.port) === null || !v.security) {
        setFormError(t`Fill in the server, the port and the security setting for each side.`)
        return
      }
    }
    setRunning(true)
    setOutcomes({})
    const settled = await Promise.all(
      sides.map(async (via): Promise<[MailboxVia, SideOutcome]> => {
        const v = values[via]
        try {
          const result = await mailboxAccessService.test({
            workspace_id: workspaceId,
            address: access.address,
            via,
            host: v.host.trim(),
            port: portNumber(v.port) as number,
            security: v.security as MailboxSecurity,
            username: v.username.trim(),
            password: v.password
          })
          return [via, { result }]
        } catch (err) {
          return [via, { error: labels.errorText(mailboxErrorKind(err)) }]
        }
      })
    )
    setOutcomes(Object.fromEntries(settled))
    setRunning(false)
    onTested?.()
  }

  const sideTitle = (via: MailboxVia) => (via === 'imap' ? t`Receiving (IMAP)` : t`Sending (SMTP)`)

  return (
    <div>
      <Text type="secondary">
        {t`Type here what you entered in your app. We compare it with what is expected and tell you what to change.`}
      </Text>
      {!access.can_send && (
        <Alert
          style={{ marginTop: 12 }}
          type="info"
          showIcon
          message={t`This access is read only: only receiving is checked.`}
        />
      )}
      {sides.map((via) => {
        const v = values[via]
        const id = (f: string) => `mbx-check-${via}-${f}`
        return (
          <fieldset
            key={via}
            style={{ border: `1px solid ${token.colorBorderSecondary}`, borderRadius: 8, margin: '16px 0', padding: 16 }}
          >
            <legend style={{ padding: '0 8px', width: 'auto', fontWeight: 600 }}>{sideTitle(via)}</legend>
            <Space direction="vertical" size={12} style={{ width: '100%' }}>
              <div>
                <label htmlFor={id('host')}>{t`Server`}</label>
                <Input
                  id={id('host')}
                  value={v.host}
                  autoComplete="off"
                  autoCapitalize="none"
                  spellCheck={false}
                  onChange={(e) => set(via, { host: e.target.value })}
                />
              </div>
              <div>
                <label htmlFor={id('port')}>{t`Port`}</label>
                <Input
                  id={id('port')}
                  value={v.port}
                  inputMode="numeric"
                  autoComplete="off"
                  onChange={(e) => set(via, { port: e.target.value })}
                />
              </div>
              <div role="radiogroup" aria-label={t`Security`}>
                <div id={id('security')}>{t`Security`}</div>
                <Radio.Group
                  value={v.security || undefined}
                  onChange={(e) => set(via, { security: e.target.value as MailboxSecurity })}
                >
                  <Space wrap>
                    <Radio value="ssl">{labels.security('ssl')}</Radio>
                    <Radio value="starttls">{labels.security('starttls')}</Radio>
                    <Radio value="none">{labels.security('none')}</Radio>
                  </Space>
                </Radio.Group>
              </div>
              <div>
                <label htmlFor={id('username')}>{t`Username`}</label>
                <Input
                  id={id('username')}
                  value={v.username}
                  autoComplete="off"
                  autoCapitalize="none"
                  spellCheck={false}
                  onChange={(e) => set(via, { username: e.target.value })}
                />
              </div>
              <div>
                <label htmlFor={id('password')}>{t`Password`}</label>
                <Input.Password
                  id={id('password')}
                  value={v.password}
                  autoComplete="new-password"
                  onChange={(e) => set(via, { password: e.target.value })}
                />
              </div>
            </Space>
          </fieldset>
        )
      })}
      {formError && <Alert type="warning" showIcon message={formError} style={{ marginBottom: 12 }} role="alert" />}
      <Button type="primary" loading={running} onClick={run}>
        {t`Test`}
      </Button>

      <div aria-live="polite" style={{ marginTop: 16 }}>
        {sides.map((via) => {
          const out = outcomes[via]
          if (!out) return null
          if ('error' in out) {
            return (
              <Alert
                key={via}
                type="error"
                showIcon
                style={{ marginBottom: 12 }}
                message={sideTitle(via)}
                description={out.error}
              />
            )
          }
          return (
            <div key={via} style={{ marginBottom: 16 }}>
              <div style={{ fontWeight: 600, marginBottom: 4 }}>{sideTitle(via)}</div>
              {out.result.ok && (
                <Alert
                  type="success"
                  showIcon
                  style={{ marginBottom: 8 }}
                  message={t`Everything is right: your app should work.`}
                />
              )}
              <ul style={{ listStyle: 'none', padding: 0, margin: 0 }}>
                {out.result.checks.map((c) => (
                  <li
                    key={c.code}
                    data-check={c.code}
                    data-ok={c.ok ? 'true' : 'false'}
                    style={{ display: 'flex', gap: 8, alignItems: 'flex-start', padding: '4px 0' }}
                  >
                    {c.ok ? (
                      <CheckCircleFilled
                        role="img"
                        aria-label={t`OK`}
                        style={{ color: token.colorSuccess, marginTop: 4 }}
                      />
                    ) : (
                      <CloseCircleFilled
                        role="img"
                        aria-label={t`To change`}
                        style={{ color: token.colorError, marginTop: 4 }}
                      />
                    )}
                    <span style={{ fontWeight: c.ok ? 400 : 600, overflowWrap: 'anywhere' }}>
                      {labels.checkText(c, access.address)}
                    </span>
                  </li>
                ))}
              </ul>
            </div>
          )
        })}
      </div>
    </div>
  )
}
