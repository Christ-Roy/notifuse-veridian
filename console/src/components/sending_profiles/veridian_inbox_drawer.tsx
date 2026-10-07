import { useEffect, useState } from 'react'
import { Alert, Button, Drawer, Form, Input, InputNumber, Space, Switch } from 'antd'
import { useLingui } from '@lingui/react/macro'

import type { Integration } from '../../services/api/types'
import { saveInbox } from './veridian_profile_ops'

interface Props {
  open: boolean
  workspaceId: string
  // null = nouvelle boîte.
  inbox: Integration | null
  onClose: () => void
  onSaved: (inboxId: string) => void
}

interface Values {
  name: string
  host: string
  port: number
  use_tls: boolean
  username: string
  password?: string
  folder?: string
}

// Boîte IMAP de retour (réponses et rejets). Le mot de passe est en écriture seule :
// à l'édition, le laisser vide conserve celui qui est stocké.
export function VeridianInboxDrawer({ open, workspaceId, inbox, onClose, onSaved }: Props) {
  const { t } = useLingui()
  const [form] = Form.useForm<Values>()
  const [saving, setSaving] = useState(false)
  const [error, setError] = useState<string | null>(null)

  useEffect(() => {
    if (!open) return
    setError(null)
    const settings = inbox?.imap_settings
    form.resetFields()
    form.setFieldsValue(
      settings
        ? {
            name: inbox?.name,
            host: settings.host,
            port: settings.port,
            use_tls: settings.use_tls,
            username: settings.username,
            folder: settings.folder || 'INBOX'
          }
        : { port: 993, use_tls: true, folder: 'INBOX' }
    )
  }, [open, inbox, form])

  const submit = async (values: Values) => {
    setSaving(true)
    setError(null)
    try {
      const id = await saveInbox(workspaceId, {
        id: inbox?.id,
        name: values.name,
        settings: {
          host: values.host.trim(),
          port: values.port,
          use_tls: values.use_tls,
          username: values.username.trim(),
          password: values.password || undefined,
          folder: values.folder?.trim() || 'INBOX',
          polling_interval_seconds: inbox?.imap_settings?.polling_interval_seconds
        }
      })
      form.resetFields()
      onSaved(id)
    } catch (err) {
      setError(err instanceof Error ? err.message : t`Could not save the inbox`)
    } finally {
      setSaving(false)
    }
  }

  return (
    <Drawer
      open={open}
      onClose={onClose}
      width={480}
      destroyOnClose
      title={inbox ? t`Edit the return inbox` : t`Add a return inbox`}
    >
      <Form form={form} layout="vertical" onFinish={submit} requiredMark={false} autoComplete="off">
        <Form.Item name="name" label={t`Name`} rules={[{ required: true, message: t`Required` }]}>
          <Input autoComplete="off" />
        </Form.Item>
        <Space align="start" wrap>
          <Form.Item name="host" label={t`IMAP host`} rules={[{ required: true, message: t`Required` }]}>
            <Input autoComplete="off" style={{ width: 240 }} />
          </Form.Item>
          <Form.Item name="port" label={t`Port`} rules={[{ required: true, message: t`Required` }]}>
            <InputNumber min={1} max={65535} precision={0} />
          </Form.Item>
          <Form.Item name="use_tls" label={t`TLS`} valuePropName="checked">
            <Switch />
          </Form.Item>
        </Space>
        <Form.Item name="username" label={t`Login`} rules={[{ required: true, message: t`Required` }]}>
          <Input autoComplete="off" />
        </Form.Item>
        <Form.Item
          name="password"
          label={t`Password`}
          extra={inbox ? t`Leave empty to keep the stored password.` : undefined}
          rules={inbox ? [] : [{ required: true, message: t`Required` }]}
        >
          <Input.Password autoComplete="new-password" />
        </Form.Item>
        <Form.Item name="folder" label={t`Folder`}>
          <Input autoComplete="off" />
        </Form.Item>
        {error && <Alert type="error" showIcon message={error} style={{ marginBottom: 12 }} />}
        <Button type="primary" htmlType="submit" loading={saving}>
          {t`Save`}
        </Button>
      </Form>
    </Drawer>
  )
}
