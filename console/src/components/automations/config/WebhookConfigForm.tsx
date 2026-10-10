import React from 'react'
import { Button, Form, Input, Space } from 'antd'
import { useLingui } from '@lingui/react/macro'
import type { WebhookNodeConfig } from '../../../services/api/automation'

interface WebhookConfigFormProps {
  config: WebhookNodeConfig
  onChange: (config: WebhookNodeConfig) => void
}

export const WebhookConfigForm: React.FC<WebhookConfigFormProps> = ({ config, onChange }) => {
  const { t } = useLingui()

  const handleUrlChange = (e: React.ChangeEvent<HTMLInputElement>) => {
    onChange({ ...config, url: e.target.value })
  }

  // The server never sends the secret back: it only tells us has_secret.
  // An empty field therefore means "keep the stored secret".
  const handleSecretChange = (e: React.ChangeEvent<HTMLInputElement>) => {
    const value = e.target.value
    onChange({ ...config, secret: value || undefined, clear_secret: undefined })
  }

  const handleClearSecret = () => {
    onChange({ ...config, secret: undefined, has_secret: false, clear_secret: true })
  }

  const isValidUrl = (url: string) => {
    if (!url) return true // Empty is valid (just not configured)
    return url.startsWith('https://')
  }

  return (
    <Form layout="vertical" className="nodrag">
      <Form.Item
        label={t`Webhook URL`}
        required
        validateStatus={config.url && !isValidUrl(config.url) ? 'error' : undefined}
        help={config.url && !isValidUrl(config.url) ? t`URL must start with https://` : undefined}
        extra={t`The URL to send the POST request to`}
      >
        <Input
          value={config.url || ''}
          onChange={handleUrlChange}
          placeholder="https://api.example.com/webhook"
        />
      </Form.Item>

      <Form.Item
        label={t`Signing Secret`}
        extra={t`Optional. Each request is signed (HMAC-SHA256, webhook-signature header). The secret is never shown again once saved.`}
      >
        <Space.Compact style={{ width: '100%' }}>
          <Input.Password
            value={config.secret || ''}
            onChange={handleSecretChange}
            placeholder={
              config.has_secret ? t`Secret saved (hidden). Leave empty to keep it.` : t`Optional signing secret`
            }
            autoComplete="new-password"
          />
          {config.has_secret && !config.secret && (
            <Button onClick={handleClearSecret}>{t`Remove secret`}</Button>
          )}
        </Space.Compact>
      </Form.Item>
    </Form>
  )
}
