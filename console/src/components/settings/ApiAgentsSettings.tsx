import { useState, useEffect, useCallback } from 'react'
import {
  Table,
  Typography,
  Spin,
  Button,
  Modal,
  Form,
  Input,
  App,
  Alert,
  Space,
  Popconfirm,
  Tooltip,
  Tag,
  Card,
  Steps
} from 'antd'
import { FontAwesomeIcon } from '@fortawesome/react-fontawesome'
import { faTrashCan } from '@fortawesome/free-regular-svg-icons'
import { faRobot, faCircleQuestion, faCopy } from '@fortawesome/free-solid-svg-icons'
import { useLingui } from '@lingui/react/macro'
import { APIKeySummary } from '../../services/api/types'
import { workspaceService } from '../../services/api/workspace'
import { SettingsSectionHeader } from './SettingsSectionHeader'

const { Text, Paragraph } = Typography

interface ApiAgentsSettingsProps {
  workspaceId: string
  /** owner OR a member with write access on the workspace resource — the
   * same gate the backend enforces server-side for create/revoke/install. */
  canManageKeys: boolean
}

export function ApiAgentsSettings({ workspaceId, canManageKeys }: ApiAgentsSettingsProps) {
  const { t } = useLingui()
  const { message } = App.useApp()

  const [keys, setKeys] = useState<APIKeySummary[]>([])
  const [loading, setLoading] = useState(false)
  const [revokingId, setRevokingId] = useState<string | null>(null)

  // Create-key modal state
  const [createModalVisible, setCreateModalVisible] = useState(false)
  const [keyName, setKeyName] = useState('')
  const [creating, setCreating] = useState(false)
  const [createdToken, setCreatedToken] = useState('')

  // "Brancher mon agent" modal state
  const [installModalVisible, setInstallModalVisible] = useState(false)
  const [generatingInstall, setGeneratingInstall] = useState(false)
  const [installCommand, setInstallCommand] = useState('')
  const [installExpiresInSeconds, setInstallExpiresInSeconds] = useState(0)
  const [nonTechnicalMode, setNonTechnicalMode] = useState(false)

  const fetchKeys = useCallback(async () => {
    setLoading(true)
    try {
      const response = await workspaceService.listAPIKeys(workspaceId)
      setKeys(response.keys || [])
    } catch (error) {
      console.error('Failed to fetch API keys', error)
      message.error(t`Failed to fetch API keys`)
    } finally {
      setLoading(false)
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps -- message/t stable enough for this effect
  }, [workspaceId])

  useEffect(() => {
    fetchKeys()
  }, [fetchKeys])

  const domainName = `${workspaceId}.${
    window.API_ENDPOINT?.replace(/^https?:\/\//, '').split('/')[0] || 'api.example.com'
  }`

  const resetCreateModal = () => {
    setCreateModalVisible(false)
    setKeyName('')
    setCreatedToken('')
  }

  const handleCreateKey = async () => {
    if (!keyName.trim()) {
      message.error(t`Please enter a name for the key`)
      return
    }
    setCreating(true)
    try {
      const response = await workspaceService.createAPIKey({
        workspace_id: workspaceId,
        email_prefix: keyName
      })
      setCreatedToken(response.token)
      message.success(t`API key created successfully`)
      fetchKeys()
    } catch (error) {
      console.error('Failed to create API key', error)
      message.error((error as Error).message || t`Failed to create API key`)
    } finally {
      setCreating(false)
    }
  }

  const handleRevoke = async (userId: string) => {
    setRevokingId(userId)
    try {
      await workspaceService.revokeAPIKey({ workspace_id: workspaceId, user_id: userId })
      message.success(t`API key revoked`)
      fetchKeys()
    } catch (error) {
      console.error('Failed to revoke API key', error)
      message.error((error as Error).message || t`Failed to revoke API key`)
    } finally {
      setRevokingId(null)
    }
  }

  const copyToClipboard = (text: string, successMessage: string) => {
    navigator.clipboard
      .writeText(text)
      .then(() => message.success(successMessage))
      .catch(() => message.error(t`Could not copy to clipboard`))
  }

  const resetInstallModal = () => {
    setInstallModalVisible(false)
    setInstallCommand('')
    setInstallExpiresInSeconds(0)
    setNonTechnicalMode(false)
  }

  const handleGenerateInstallCommand = async () => {
    setGeneratingInstall(true)
    try {
      const response = await workspaceService.createAgentInstallToken({
        workspace_id: workspaceId
      })
      const host = window.API_ENDPOINT?.replace(/\/$/, '') || 'https://notifuse.app.veridian.site'
      const command = `curl -fsSL ${host}/agent/install.sh | sh -s -- --token ${response.install_token}`
      setInstallCommand(command)
      setInstallExpiresInSeconds(response.expires_in_seconds)
      fetchKeys()
    } catch (error) {
      console.error('Failed to create agent install token', error)
      message.error((error as Error).message || t`Failed to create install command`)
    } finally {
      setGeneratingInstall(false)
    }
  }

  const columns = [
    {
      title: t`Name`,
      dataIndex: 'name',
      key: 'name',
      render: (name: string) => <Text strong>{name}</Text>
    },
    {
      title: t`Key`,
      dataIndex: 'masked_email',
      key: 'masked_email',
      render: (maskedEmail: string) => <Text className="break-all" code>{maskedEmail}</Text>
    },
    {
      title: t`Created`,
      dataIndex: 'created_at',
      key: 'created_at',
      render: (date: string) => new Date(date).toLocaleDateString()
    },
    {
      title: t`Last used`,
      dataIndex: 'last_used_at',
      key: 'last_used_at',
      render: (date: string | undefined) =>
        date ? (
          new Date(date).toLocaleDateString()
        ) : (
          <Tooltip title={t`This instance does not track per-key usage yet`}>
            <Tag>{t`Not available`}</Tag>
          </Tooltip>
        )
    },
    ...(canManageKeys
      ? [
          {
            title: '',
            key: 'action',
            width: 80,
            render: (_: unknown, record: APIKeySummary) => (
              <Popconfirm
                title={t`Revoke this key`}
                description={t`Any agent or integration using this key will stop working immediately. This cannot be undone.`}
                onConfirm={() => handleRevoke(record.user_id)}
                okText={t`Revoke`}
                okButtonProps={{ danger: true, loading: revokingId === record.user_id }}
                cancelText={t`Cancel`}
              >
                <Tooltip title={t`Revoke`} placement="left">
                  <Button
                    icon={<FontAwesomeIcon icon={faTrashCan} />}
                    size="small"
                    type="text"
                    danger
                    loading={revokingId === record.user_id}
                  />
                </Tooltip>
              </Popconfirm>
            )
          }
        ]
      : [])
  ]

  return (
    <>
      <SettingsSectionHeader
        title={t`API & agents`}
        description={t`Create API keys and connect AI agents (Claude Code or similar) to this workspace.`}
      />

      {canManageKeys && (
        <Card
          className="mb-6"
          title={
            <Space>
              <FontAwesomeIcon icon={faRobot} />
              {t`Connect my agent`}
            </Space>
          }
        >
          <Paragraph type="secondary">
            {t`Generate a one-time install command. It installs the CLI, writes the key securely to disk, and never shows the key itself — not even to you.`}
          </Paragraph>
          <Button type="primary" onClick={() => setInstallModalVisible(true)}>
            {t`Connect my agent`}
          </Button>
        </Card>
      )}

      <div className="flex justify-between items-center mb-4">
        <div className="text-lg font-medium">{t`API keys`}</div>
        {canManageKeys && (
          <Button type="primary" ghost size="small" onClick={() => setCreateModalVisible(true)}>
            {t`Create API Key`}
          </Button>
        )}
      </div>

      {loading ? (
        <div style={{ textAlign: 'center', padding: '20px' }}>
          <Spin />
        </div>
      ) : (
        <Table
          dataSource={keys}
          columns={columns}
          rowKey="user_id"
          pagination={false}
          locale={{ emptyText: t`No API keys yet` }}
          className="border border-gray-200 rounded-md"
        />
      )}

      {/* Create API key modal */}
      <Modal
        title={t`Create API Key`}
        open={createModalVisible}
        onCancel={resetCreateModal}
        footer={
          createdToken
            ? [
                <Button key="close" type="primary" onClick={resetCreateModal}>
                  {t`Close`}
                </Button>
              ]
            : [
                <Button key="cancel" onClick={resetCreateModal}>
                  {t`Cancel`}
                </Button>,
                <Button key="create" type="primary" onClick={handleCreateKey} loading={creating}>
                  {t`Create API Key`}
                </Button>
              ]
        }
      >
        {!createdToken ? (
          <Form layout="vertical">
            <Form.Item
              label={t`Key name`}
              required
              rules={[{ required: true, message: t`Please enter a name` }]}
            >
              <Space.Compact style={{ width: '100%' }}>
                <Input
                  value={keyName}
                  onChange={(e) => {
                    const snakeCase = e.target.value
                      .toLowerCase()
                      .replace(/\s+/g, '_')
                      .replace(/[^a-z0-9_]/g, '')
                    setKeyName(snakeCase)
                  }}
                  placeholder="my_agent"
                  style={{ flex: 1 }}
                />
                <Button disabled style={{ pointerEvents: 'none', color: 'rgba(0, 0, 0, 0.88)' }}>
                  {'@' + domainName}
                </Button>
              </Space.Compact>
            </Form.Item>
          </Form>
        ) : (
          <>
            <Alert
              message={t`API Key Created Successfully`}
              description={t`This token will only be displayed once. Please save it in a secure location. It cannot be retrieved again.`}
              type="warning"
              showIcon
              style={{ marginBottom: 16 }}
            />
            <Form layout="vertical">
              <Form.Item label={t`API Token`}>
                <Input.TextArea value={createdToken} autoSize={{ minRows: 3, maxRows: 5 }} readOnly />
              </Form.Item>
              <Button
                icon={<FontAwesomeIcon icon={faCopy} />}
                onClick={() => copyToClipboard(createdToken, t`Token copied to clipboard`)}
              >
                {t`Copy`}
              </Button>
            </Form>
          </>
        )}
      </Modal>

      {/* "Connect my agent" modal */}
      <Modal
        title={t`Connect my agent`}
        open={installModalVisible}
        onCancel={resetInstallModal}
        width={680}
        footer={[
          <Button key="close" onClick={resetInstallModal}>
            {t`Close`}
          </Button>
        ]}
      >
        <div className="flex justify-end mb-4">
          <Button
            type="link"
            icon={<FontAwesomeIcon icon={faCircleQuestion} />}
            onClick={() => setNonTechnicalMode((v) => !v)}
          >
            {nonTechnicalMode ? t`Show the technical command` : t`I'm not technical, explain this`}
          </Button>
        </div>

        {nonTechnicalMode ? (
          <Steps
            direction="vertical"
            size="small"
            items={[
              {
                title: t`1. Generate a one-time command`,
                description: t`Click "Generate command" below. It creates a secret, single-use link valid for 10 minutes — nobody can reuse it after you.`
              },
              {
                title: t`2. Give it to your AI agent`,
                description: t`Paste the command into Claude Code (or your coding assistant's terminal) and ask it to run it. Your agent will set itself up automatically.`
              },
              {
                title: t`3. Done`,
                description: t`Your agent can now send emails, manage contacts and read stats on this workspace — without you ever seeing or handling the secret key.`
              }
            ]}
          />
        ) : (
          <>
            <Paragraph type="secondary">
              {t`This command is valid once, for a short time, and never contains your real API key — only a one-time ticket exchanged securely by the installer. Give it to your agent (Claude Code or similar) as-is.`}
            </Paragraph>

            {!installCommand ? (
              <Button type="primary" onClick={handleGenerateInstallCommand} loading={generatingInstall}>
                {t`Generate command`}
              </Button>
            ) : (
              <>
                <Alert
                  type="warning"
                  showIcon
                  className="mb-4"
                  message={t`This command expires in ${Math.round(installExpiresInSeconds / 60)} minutes and can only be used once.`}
                />
                <Input.TextArea
                  value={installCommand}
                  autoSize={{ minRows: 2, maxRows: 4 }}
                  readOnly
                  className="mb-2"
                />
                <Button
                  icon={<FontAwesomeIcon icon={faCopy} />}
                  onClick={() => copyToClipboard(installCommand, t`Command copied to clipboard`)}
                >
                  {t`Copy`}
                </Button>
              </>
            )}
          </>
        )}
      </Modal>
    </>
  )
}
