import { useEffect, useMemo, useState } from 'react'
import {
  Alert,
  Button,
  Collapse,
  Drawer,
  Form,
  Input,
  InputNumber,
  Radio,
  Select,
  Space,
  Switch,
  Typography
} from 'antd'
import { useLingui } from '@lingui/react/macro'

import type { EmailProvider, Integration, Sender, Workspace } from '../../services/api/types'
import type { EmailProfileOverview } from '../../services/api/veridian_email_profiles'
import {
  gmailAccountTypeMaxDailyCap,
  inferEmailProfileMode,
  smtpSettingsForEdit
} from '../settings/veridian_email_profiles'
import { VeridianInboxDrawer } from './veridian_inbox_drawer'
import { VeridianProfileAdvanced } from './veridian_profile_advanced'
import { setUsage, updateProfile } from './veridian_profile_ops'

const { Text } = Typography

interface Props {
  open: boolean
  workspace: Workspace
  profile: EmailProfileOverview | null
  onClose: () => void
  // Appelé après une écriture réussie : la page relit workspace et overview.
  onSaved: () => void
}

type Usage = 'commercial' | 'transactional'

const newSender = (): Sender => ({ id: crypto.randomUUID(), email: '', name: '', is_default: false })

export function VeridianProfileEditDrawer({ open, workspace, profile, onClose, onSaved }: Props) {
  const { t } = useLingui()
  const integration: Integration | undefined = useMemo(
    () => (workspace.integrations || []).find((i) => i.id === profile?.integration_id),
    [workspace.integrations, profile?.integration_id]
  )
  const [name, setName] = useState('')
  const [draft, setDraft] = useState<EmailProvider | null>(null)
  const [usage, setUsageState] = useState<Usage>('commercial')
  const [inboxId, setInboxId] = useState<string>('')
  const [saving, setSaving] = useState(false)
  const [error, setError] = useState<string | null>(null)
  const [inboxDrawer, setInboxDrawer] = useState<{ open: boolean; inbox: Integration | null }>({
    open: false,
    inbox: null
  })

  // Réinitialise le brouillon à chaque ouverture sur un profil.
  useEffect(() => {
    if (!open || !integration?.email_provider) return
    const provider = integration.email_provider
    setName(integration.name)
    setDraft({ ...provider, smtp: smtpSettingsForEdit(provider.smtp) })
    setUsageState(profile?.usage ?? 'commercial')
    setInboxId(provider.veridian_return_imap_integration_id ?? '')
    setError(null)
    // eslint-disable-next-line react-hooks/exhaustive-deps -- seule l'ouverture et l'identité du profil réinitialisent
  }, [open, profile?.integration_id])

  if (!profile || !integration || !draft) {
    return <Drawer open={false} onClose={onClose} />
  }

  const profileName = profile.name
  const original = integration.email_provider as EmailProvider
  const mode = inferEmailProfileMode(draft)
  const isSMTP = draft.kind === 'smtp'
  const inboxes = (workspace.integrations || []).filter((i) => i.type === 'imap')
  const linkedInbox = inboxes.find((i) => i.id === inboxId) ?? null
  const gmailMax = mode === 'gmail_app_password' ? gmailAccountTypeMaxDailyCap(draft.veridian_gmail_account_type) : undefined

  const transportChanged =
    isSMTP &&
    (draft.smtp?.host !== original.smtp?.host ||
      draft.smtp?.port !== original.smtp?.port ||
      draft.smtp?.username !== original.smtp?.username ||
      draft.smtp?.use_tls !== original.smtp?.use_tls ||
      !!draft.smtp?.password)

  const setSmtp = (changes: Partial<NonNullable<EmailProvider['smtp']>>) =>
    setDraft({ ...draft, smtp: { ...(draft.smtp as NonNullable<EmailProvider['smtp']>), ...changes } })

  const setSenders = (senders: Sender[]) => setDraft({ ...draft, senders })

  const save = async () => {
    setSaving(true)
    setError(null)
    try {
      const senders = draft.senders.filter((s) => s.email.trim() !== '')
      if (senders.length === 0) throw new Error(t`At least one sender address is required`)
      if (!senders.some((s) => s.is_default)) senders[0] = { ...senders[0], is_default: true }
      const provider: EmailProvider = { ...draft, senders }
      if (inboxId) provider.veridian_return_imap_integration_id = inboxId
      else delete provider.veridian_return_imap_integration_id
      await updateProfile(workspace.id, profile.integration_id, {
        name: name.trim() || integration.name,
        provider: () => provider
      })
      if (usage !== profile.usage) await setUsage(workspace.id, profile.integration_id, usage)
      onSaved()
      onClose()
    } catch (err) {
      setError(err instanceof Error ? err.message : t`Could not save the profile`)
    } finally {
      setSaving(false)
    }
  }

  const transport = isSMTP ? (
    <>
      <Space align="start" wrap>
        <Form.Item label={t`Host`}>
          <Input
            value={draft.smtp?.host}
            disabled={mode === 'gmail_app_password'}
            onChange={(event) => setSmtp({ host: event.target.value })}
            style={{ width: 240 }}
            aria-label="smtp-host"
          />
        </Form.Item>
        <Form.Item label={t`Port`}>
          <InputNumber
            min={1}
            max={65535}
            precision={0}
            value={draft.smtp?.port}
            disabled={mode === 'gmail_app_password'}
            onChange={(next) => setSmtp({ port: Number(next ?? 587) })}
            aria-label="smtp-port"
          />
        </Form.Item>
        <Form.Item label={t`TLS`}>
          <Switch
            checked={draft.smtp?.use_tls}
            disabled={mode === 'gmail_app_password'}
            onChange={(on) => setSmtp({ use_tls: on })}
          />
        </Form.Item>
      </Space>
      <Form.Item label={t`Login`}>
        <Input
          value={draft.smtp?.username}
          onChange={(event) => setSmtp({ username: event.target.value })}
          aria-label="smtp-login"
        />
      </Form.Item>
      <Form.Item
        label={mode === 'gmail_app_password' ? t`New app password` : t`New password`}
        extra={t`Leave empty to keep the stored secret. It is never shown.`}
      >
        <Input.Password
          autoComplete="new-password"
          value={draft.smtp?.password ?? ''}
          onChange={(event) => setSmtp({ password: event.target.value })}
          aria-label="smtp-password"
        />
      </Form.Item>
      {transportChanged && (
        <Alert
          type="warning"
          showIcon
          style={{ marginBottom: 12 }}
          message={t`Changing the transport clears the verification: send a new test afterwards.`}
        />
      )}
    </>
  ) : (
    <Alert
      type="info"
      showIcon
      message={t`The credentials of this provider are managed through the API or the CLI. Name, senders, usage and rules are editable here.`}
    />
  )

  return (
    <>
      <Drawer
        open={open}
        onClose={onClose}
        width={620}
        destroyOnClose
        title={t`Edit ${profileName}`}
        footer={
          <Space>
            <Button type="primary" loading={saving} onClick={save}>
              {t`Save`}
            </Button>
            <Button onClick={onClose} disabled={saving}>
              {t`Cancel`}
            </Button>
          </Space>
        }
      >
        <Form layout="vertical" requiredMark={false} autoComplete="off">
          <Form.Item label={t`Profile name`}>
            <Input value={name} onChange={(event) => setName(event.target.value)} aria-label="profile-name" />
          </Form.Item>

          <Form.Item label={t`Usage`} extra={t`A profile is either commercial or transactional, never both.`}>
            <Radio.Group value={usage} onChange={(event) => setUsageState(event.target.value as Usage)}>
              <Radio value="commercial">{t`Commercial`}</Radio>
              <Radio value="transactional">{t`Transactional`}</Radio>
            </Radio.Group>
          </Form.Item>

          <Text strong>{t`Senders`}</Text>
          <div style={{ margin: '8px 0 16px' }}>
            {draft.senders.map((sender, index) => (
              <Space key={sender.id || index} style={{ display: 'flex', marginBottom: 6 }} wrap>
                <Input
                  placeholder={t`Address`}
                  value={sender.email}
                  style={{ width: 230 }}
                  onChange={(event) =>
                    setSenders(draft.senders.map((s, i) => (i === index ? { ...s, email: event.target.value } : s)))
                  }
                  aria-label={`sender-email-${index}`}
                />
                <Input
                  placeholder={t`Display name`}
                  value={sender.name}
                  style={{ width: 160 }}
                  onChange={(event) =>
                    setSenders(draft.senders.map((s, i) => (i === index ? { ...s, name: event.target.value } : s)))
                  }
                />
                <Radio
                  checked={sender.is_default}
                  onChange={() => setSenders(draft.senders.map((s, i) => ({ ...s, is_default: i === index })))}
                >
                  {t`Default`}
                </Radio>
                <Button
                  size="small"
                  disabled={draft.senders.length <= 1}
                  onClick={() => setSenders(draft.senders.filter((_, i) => i !== index))}
                >
                  {t`Remove`}
                </Button>
              </Space>
            ))}
            <Button size="small" onClick={() => setSenders([...draft.senders, newSender()])}>
              {t`Add a sender`}
            </Button>
          </div>

          {transport}

          <Text strong>{t`Return inbox`}</Text>
          <div style={{ margin: '8px 0 16px' }}>
            <Space wrap>
              <Select
                style={{ width: 300 }}
                value={inboxId}
                onChange={setInboxId}
                options={[
                  { value: '', label: t`No linked inbox` },
                  ...inboxes.map((i) => ({
                    value: i.id,
                    label: `${i.imap_settings?.username ?? i.name} (${i.imap_settings?.host ?? ''})`
                  }))
                ]}
                aria-label="return-inbox"
              />
              {linkedInbox && (
                <Button onClick={() => setInboxDrawer({ open: true, inbox: linkedInbox })}>{t`Edit this inbox`}</Button>
              )}
              <Button onClick={() => setInboxDrawer({ open: true, inbox: null })}>{t`New inbox`}</Button>
            </Space>
          </div>

          <Collapse
            ghost
            items={[
              {
                key: 'advanced',
                label: t`Advanced settings`,
                children: (
                  <VeridianProfileAdvanced
                    value={draft}
                    onChange={setDraft}
                    workspaceSettings={workspace.settings}
                    gmailMaxDailyCap={gmailMax}
                  />
                )
              }
            ]}
          />
        </Form>
        {error && <Alert type="error" showIcon message={error} style={{ marginTop: 12 }} />}
      </Drawer>

      <VeridianInboxDrawer
        open={inboxDrawer.open}
        workspaceId={workspace.id}
        inbox={inboxDrawer.inbox}
        onClose={() => setInboxDrawer({ open: false, inbox: null })}
        onSaved={(id) => {
          setInboxDrawer({ open: false, inbox: null })
          setInboxId(id)
          onSaved()
        }}
      />
    </>
  )
}
