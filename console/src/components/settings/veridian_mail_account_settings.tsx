/**
 * Veridian — section "Mail account" de Settings (vague 7, 2026-05-25).
 *
 * Evolution de la version vague 6 (sender choice radio uniquement) vers
 * la version multi-comptes :
 *
 *   1. Section "Connected accounts" : liste des comptes OAuth (Gmail +
 *      Microsoft) connectes cote Hub, recuperee via le proxy Notifuse
 *      `GET /api/veridian/mail-accounts/me`. Pour chaque compte :
 *        - Icone provider (GoogleOutlined / WindowsOutlined)
 *        - Email + nom affichage
 *        - Badge "Default" (compte qui sert pour `hub_gmail` sans
 *          mail_account_id explicite cote send-as-user) — OU Radio
 *          "Set as default" pour les autres
 *        - Badge rouge "Needs reauth" + bouton Reconnect si le refresh
 *          token Google/MS a expire
 *        - Bouton "Disconnect" (TODO : spec Hub DELETE pas encore livree
 *          — pour l'instant log warn console)
 *
 *   2. Boutons "Connect another Gmail" / "Connect Microsoft" : redirect
 *      vers le Hub avec `return` URL pour bouncer ici apres consent OAuth.
 *
 *   3. Radio sender (heritage vague 6) : choix smtp_generic vs hub_gmail
 *      — quand hub_gmail selectionne, l'envoi utilise le compte default
 *      de la liste ci-dessus.
 *
 * Mode optimiste : si proxy retourne `hub_available: false` (endpoint
 * Hub pas livre, user sans hub_user_id, Hub down), on n'affiche pas
 * d'error boundary — juste un Alert info + le bouton "Connect your
 * first Gmail account". L'experience reste fonctionnelle.
 *
 * Pas de hook d'error boundary global ici : l'UI doit monter meme si
 * tous les endpoints sont down (vague 6 lesson learned).
 */

import { useMemo } from 'react'
import {
  Alert,
  Badge,
  Button,
  Card,
  List,
  Radio,
  Space,
  Spin,
  Tag,
  Typography,
  message
} from 'antd'
import type { RadioChangeEvent } from 'antd'
import { useLingui } from '@lingui/react/macro'
import {
  GoogleOutlined,
  WindowsOutlined,
  MailOutlined,
  ExclamationCircleOutlined
} from '@ant-design/icons'
import {
  useMailProviderChoice,
  useSetMailProviderChoice
} from '../../hooks/useMailProviderChoice'
import {
  useMailAccountsList,
  useSetDefaultMailAccount
} from '../../hooks/useMailAccounts'
import type { MailProviderChoice } from '../../services/api/veridian_mail_provider'
import type { MailAccount } from '../../services/api/veridian_mail_accounts'

const { Text, Paragraph } = Typography

interface Props {
  workspaceId: string
}

const HUB_BASE_URL = 'https://app.veridian.site'
// Endpoint Hub qui démarre le consent Google DIRECTEMENT (302 → écran Google),
// sans page Hub intermédiaire. Lit `return` (allowlisté côté Hub) pour rebondir
// dans Notifuse après consent. Cf. veridian-hub app/api/gmail/connect/route.ts.
const HUB_GMAIL_CONNECT_ENDPOINT = `${HUB_BASE_URL}/api/gmail/connect`
// Microsoft : pas d'endpoint /api/microsoft/connect côté Hub aujourd'hui →
// on passe par la page Hub historique. Ticket ouvert pour l'endpoint direct.
const HUB_CONNECT_PAGE_URL = `${HUB_BASE_URL}/dashboard/settings/mail`
const NOTIFUSE_RETURN_URL = 'https://notifuse.app.veridian.site/console'

function buildHubConnectUrl(
  workspaceId: string,
  provider: 'google' | 'microsoft'
): string {
  const returnTo = `${NOTIFUSE_RETURN_URL}/workspace/${workspaceId}/settings/mail-account`

  // Gmail : flow DIRECT — clic Notifuse → écran Google, pas de page Hub visible.
  // L'endpoint /api/gmail/connect redirige immédiatement (302) vers le consent
  // Google puis rebondit sur `return` (Notifuse) après autorisation.
  if (provider === 'google') {
    const url = new URL(HUB_GMAIL_CONNECT_ENDPOINT)
    url.searchParams.set('return', returnTo)
    return url.toString()
  }

  // Microsoft : pas d'endpoint connect direct Hub → page Hub intermédiaire.
  const url = new URL(HUB_CONNECT_PAGE_URL)
  url.searchParams.set('return', returnTo)
  url.searchParams.set('add', '1')
  url.searchParams.set('provider', provider)
  return url.toString()
}

export function VeridianMailAccountSettings({ workspaceId }: Props) {
  const { t } = useLingui()

  // --- Sender choice (vague 6 — preserve) ---
  const { data: choiceData, isLoading: choiceLoading } = useMailProviderChoice(workspaceId)
  const setChoice = useSetMailProviderChoice(workspaceId)
  const currentChoice: MailProviderChoice = choiceData?.choice ?? 'smtp_generic'

  // --- Mail accounts list (vague 7 — nouveau) ---
  const { data: accountsData, isLoading: accountsLoading } = useMailAccountsList()
  const setDefaultMutation = useSetDefaultMailAccount()

  const hubAvailable = accountsData?.hub_available ?? false
  const accounts: MailAccount[] = accountsData?.accounts ?? []

  const defaultAccount = useMemo(
    () => accounts.find((a) => a.is_default),
    [accounts]
  )

  const handleChoiceChange = (e: RadioChangeEvent) => {
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

  const handleSetDefault = (accountId: string) => {
    setDefaultMutation.mutate(accountId, {
      onSuccess: (data) => {
        if (data.hub_available && data.is_default) {
          message.success(t`Default account updated.`)
        } else if (!data.hub_available) {
          message.warning(t`Hub unavailable. Please retry in a moment.`)
        } else {
          message.error(t`Could not set default account.`)
        }
      },
      onError: () => {
        message.error(t`Could not set default account. Please retry.`)
      }
    })
  }

  const handleDisconnect = (accountId: string) => {
    // TODO vague 8 : endpoint DELETE Hub /api/users/{userId}/mail-accounts/{id}
    // pas encore specifie. Pour l'instant on log un warn et on affiche un
    // message info — pas de crash, pas d'appel reseau.
    console.warn('[mail-accounts] disconnect not yet implemented for', accountId)
    message.info(t`Disconnect is not available yet. Please remove the account from the Veridian Hub.`)
  }

  const handleReconnect = (provider: 'google' | 'microsoft') => {
    window.location.href = buildHubConnectUrl(workspaceId, provider)
  }

  return (
    <div>
      <h2 style={{ marginBottom: 24 }}>{t`Mail account`}</h2>

      {/* --- Card 1: Connected accounts --- */}
      <Card style={{ marginBottom: 24 }} title={t`Connected accounts`}>
        {accountsLoading ? (
          <div style={{ textAlign: 'center', padding: 24 }}>
            <Spin />
          </div>
        ) : (
          <Space direction="vertical" size="middle" style={{ width: '100%' }}>
            {!hubAvailable && (
              <Alert
                type="info"
                showIcon
                message={t`No accounts connected yet`}
                description={t`Connect your first Gmail or Microsoft account to send emails on behalf of your workspace.`}
              />
            )}

            {hubAvailable && accounts.length === 0 && (
              <Alert
                type="info"
                showIcon
                message={t`No accounts connected yet`}
                description={t`Connect your first Gmail or Microsoft account to send emails on behalf of your workspace.`}
              />
            )}

            {hubAvailable && accounts.length > 0 && (
              <List
                dataSource={accounts}
                renderItem={(account) => (
                  <List.Item
                    key={account.id}
                    actions={buildAccountActions({
                      account,
                      defaultAccountId: defaultAccount?.id,
                      onSetDefault: handleSetDefault,
                      onReconnect: handleReconnect,
                      onDisconnect: handleDisconnect,
                      isSettingDefault: setDefaultMutation.isPending,
                      t
                    })}
                  >
                    <List.Item.Meta
                      avatar={renderProviderIcon(account.provider)}
                      title={
                        <Space>
                          <Text strong>{account.email}</Text>
                          {account.is_default && (
                            <Tag color="green">{t`Default`}</Tag>
                          )}
                          {account.needs_reauth && (
                            <Tag color="red" icon={<ExclamationCircleOutlined />}>
                              {t`Needs reauth`}
                            </Tag>
                          )}
                        </Space>
                      }
                      description={
                        <Space size="small">
                          <Text type="secondary">{account.name}</Text>
                          {account.connected_at && (
                            <Text type="secondary" style={{ fontSize: 12 }}>
                              {' · '}
                              {t`connected ${formatDate(account.connected_at)}`}
                            </Text>
                          )}
                        </Space>
                      }
                    />
                  </List.Item>
                )}
              />
            )}

            <Space wrap>
              <Button
                icon={<GoogleOutlined />}
                onClick={() => {
                  window.location.href = buildHubConnectUrl(workspaceId, 'google')
                }}
              >
                {accounts.length === 0
                  ? t`Connect your first Gmail account`
                  : t`Connect another Gmail account`}
              </Button>
              <Button
                icon={<WindowsOutlined />}
                onClick={() => {
                  window.location.href = buildHubConnectUrl(workspaceId, 'microsoft')
                }}
              >
                {t`Connect a Microsoft account`}
              </Button>
            </Space>
          </Space>
        )}
      </Card>

      {/* --- Card 2: Sender choice (vague 6 — preserve) --- */}
      <Card title={t`Sender preference`}>
        {choiceLoading ? (
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
                onChange={handleChoiceChange}
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
                      <span>{t`My connected account (default)`}</span>
                    </Space>
                  </Radio>
                </Space>
              </Radio.Group>
            </div>

            {currentChoice === 'hub_gmail' && (
              <Alert
                type="info"
                showIcon
                message={
                  defaultAccount
                    ? t`Sends will route through ${defaultAccount.email}.`
                    : t`No default account selected. Sends will fall back to the Veridian generic sender until you connect and pick a default account above.`
                }
              />
            )}
          </Space>
        )}
      </Card>
    </div>
  )
}

// ─── Helpers ───────────────────────────────────────────────────────────────

function renderProviderIcon(provider: 'google' | 'microsoft') {
  // Tailles cohérentes — Antd icons rendent un peu petits par defaut
  // dans List.Item.Meta.avatar.
  const style = { fontSize: 24 }
  if (provider === 'microsoft') {
    return <WindowsOutlined style={style} />
  }
  return <GoogleOutlined style={style} />
}

function formatDate(iso: string): string {
  try {
    const d = new Date(iso)
    if (Number.isNaN(d.getTime())) return iso
    return d.toLocaleDateString()
  } catch {
    return iso
  }
}

interface BuildActionsParams {
  account: MailAccount
  defaultAccountId: string | undefined
  onSetDefault: (accountId: string) => void
  onReconnect: (provider: 'google' | 'microsoft') => void
  onDisconnect: (accountId: string) => void
  isSettingDefault: boolean
  t: (strings: TemplateStringsArray, ...values: unknown[]) => string
}

function buildAccountActions({
  account,
  defaultAccountId,
  onSetDefault,
  onReconnect,
  onDisconnect,
  isSettingDefault,
  t
}: BuildActionsParams) {
  const actions: React.ReactNode[] = []

  if (account.needs_reauth) {
    actions.push(
      <Button
        key="reconnect"
        type="primary"
        danger
        size="small"
        onClick={() => onReconnect(account.provider)}
      >
        {t`Reconnect`}
      </Button>
    )
  }

  if (!account.is_default && !account.needs_reauth) {
    actions.push(
      <Button
        key="set-default"
        type="link"
        size="small"
        loading={isSettingDefault && defaultAccountId !== account.id}
        onClick={() => onSetDefault(account.id)}
      >
        {t`Set as default`}
      </Button>
    )
  }

  actions.push(
    <Button
      key="disconnect"
      type="link"
      size="small"
      danger
      onClick={() => onDisconnect(account.id)}
    >
      {t`Disconnect`}
    </Button>
  )

  return actions
}
