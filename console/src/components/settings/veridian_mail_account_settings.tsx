/**
 * Veridian — section "Mail account" de Settings.
 *
 * Version MINIMALE livrée vague 6 (ticket
 * `2026-05-25-mail-send-as-user-via-hub-gateway.md` §3.1) — contrainte
 * critique : l'endpoint Hub `GET /api/users/{userId}/mail-provider-status`
 * n'existe pas encore (404 catchall Next.js sur Hub staging, vérifié par
 * team-lead). Donc on NE PEUT PAS afficher "Status connecté à Gmail
 * (email@...)" / "Reconnexion requise". Ticket Hub miroir ouvert :
 * `../veridian-hub/todo/2026-05-25-mail-provider-status-endpoint.md`.
 *
 * Ce qu'on livre maintenant :
 *   1. Bouton "Connecter mon Gmail" → redirect vers Hub avec `return` URL
 *      (Hub gère le consent OAuth + callback puis redirect retour ici).
 *   2. Radio entre 2 choix :
 *        - `smtp_generic` : sender générique Veridian (défaut)
 *        - `hub_gmail`    : route via Hub Mail Gateway (fallback automatique
 *          côté lib Go si Gmail pas connecté — UI ne le sait pas mais
 *          warning informatif affiché sous le radio).
 *   3. Pas de status connected / déconnect / needs_reauth → vague 7 quand
 *      l'endpoint Hub sera livré.
 */

import { Card, Radio, Button, Spin, Space, Typography, Alert, message } from 'antd'
import type { RadioChangeEvent } from 'antd'
import { useLingui } from '@lingui/react/macro'
import { GoogleOutlined, MailOutlined } from '@ant-design/icons'
import {
  useMailProviderChoice,
  useSetMailProviderChoice
} from '../../hooks/useMailProviderChoice'
import type { MailProviderChoice } from '../../services/api/veridian_mail_provider'

const { Text, Paragraph } = Typography

interface Props {
  workspaceId: string
}

const HUB_CONNECT_GMAIL_URL = 'https://app.veridian.site/dashboard/settings/mail'
const NOTIFUSE_RETURN_URL = 'https://notifuse.app.veridian.site/console'

function buildHubConnectUrl(workspaceId: string): string {
  const returnTo = `${NOTIFUSE_RETURN_URL}/workspace/${workspaceId}/settings/mail-account`
  const url = new URL(HUB_CONNECT_GMAIL_URL)
  url.searchParams.set('return', returnTo)
  return url.toString()
}

export function VeridianMailAccountSettings({ workspaceId }: Props) {
  const { t } = useLingui()
  const { data, isLoading } = useMailProviderChoice(workspaceId)
  const setChoice = useSetMailProviderChoice(workspaceId)

  const currentChoice: MailProviderChoice = data?.choice ?? 'smtp_generic'

  const handleChange = (e: RadioChangeEvent) => {
    const next = e.target.value as MailProviderChoice
    setChoice.mutate(next, {
      onSuccess: () => {
        message.success(t`Mail provider preference saved.`)
      },
      onError: () => {
        message.error(t`Failed to save mail provider preference. Please retry.`)
      }
    })
  }

  return (
    <div>
      <h2 style={{ marginBottom: 24 }}>{t`Mail account`}</h2>
      <Card>
        {isLoading ? (
          <div style={{ textAlign: 'center', padding: 24 }}>
            <Spin />
          </div>
        ) : (
          <Space direction="vertical" size="large" style={{ width: '100%' }}>
            <Paragraph type="secondary" style={{ marginBottom: 0 }}>
              {t`Choose how transactional emails (magic links, MFA, notifications) are sent on behalf of your workspace.`}
            </Paragraph>

            <div>
              <Text strong style={{ display: 'block', marginBottom: 12 }}>
                {t`Sender`}
              </Text>
              <Radio.Group
                value={currentChoice}
                onChange={handleChange}
                disabled={setChoice.isPending}
              >
                <Space direction="vertical">
                  <Radio value="smtp_generic">
                    <Space>
                      <MailOutlined />
                      <span>{t`Veridian generic sender (default)`}</span>
                    </Space>
                  </Radio>
                  <Radio value="hub_gmail">
                    <Space>
                      <GoogleOutlined />
                      <span>{t`My Gmail connected via Hub`}</span>
                    </Space>
                  </Radio>
                </Space>
              </Radio.Group>
            </div>

            {currentChoice === 'hub_gmail' && (
              <Alert
                type="info"
                showIcon
                message={t`If Gmail is not connected on the Hub, sends will automatically fall back to the generic sender.`}
              />
            )}

            <div>
              <Text strong style={{ display: 'block', marginBottom: 8 }}>
                {t`Connect your Gmail account`}
              </Text>
              <Paragraph type="secondary" style={{ fontSize: 13 }}>
                {t`Click below to connect your Gmail account from the Veridian Hub. Once connected, the "My Gmail" option above will route sends through your Gmail.`}
              </Paragraph>
              <Button
                type="primary"
                icon={<GoogleOutlined />}
                onClick={() => {
                  window.location.href = buildHubConnectUrl(workspaceId)
                }}
              >
                {t`Connect my Gmail`}
              </Button>
            </div>
          </Space>
        )}
      </Card>
    </div>
  )
}
