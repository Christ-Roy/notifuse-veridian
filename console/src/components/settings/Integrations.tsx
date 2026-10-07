import { useState, useEffect } from 'react'
import React from 'react'
import { Link } from '@tanstack/react-router'
import {
  Input,
  Button,
  Alert,
  message,
  Space,
  Descriptions,
  Tag,
  Drawer,
  Popconfirm,
  Card,
  Tooltip
} from 'antd'
import { useLingui } from '@lingui/react/macro'

import {
  Workspace,
  Integration,
  DeleteIntegrationRequest
} from '../../services/api/types'
import { workspaceService } from '../../services/api/workspace'
import { listsApi } from '../../services/api/list'
import { faCheck, faTimes } from '@fortawesome/free-solid-svg-icons'
import { FontAwesomeIcon } from '@fortawesome/react-fontawesome'
import { faCopy, faPenToSquare, faTrashCan } from '@fortawesome/free-regular-svg-icons'
import { SupabaseIntegration } from '../integrations/SupabaseIntegration'
import { LLMIntegration } from '../integrations/LLMIntegration'
import { getLLMProviderIcon, getLLMProviderName } from '../integrations/LLMProviders'
import { FirecrawlIntegration } from '../integrations/FirecrawlIntegration'
import { firecrawlProvider } from '../integrations/FirecrawlProviders'
import { LLMProviderKind } from '../../services/api/types'
import { SettingsSectionHeader } from './SettingsSectionHeader'

// Helper function to generate Supabase webhook URLs
const generateSupabaseWebhookURL = (
  hookType: 'auth-email' | 'before-user-created',
  workspaceID: string,
  integrationID: string
): string => {
  let defaultOrigin = window.location.origin
  if (defaultOrigin.includes('notifusedev.com')) {
    defaultOrigin = 'https://localapi.notifuse.com:4000'
  }
  const apiEndpoint = window.API_ENDPOINT?.trim() || defaultOrigin

  return `${apiEndpoint}/webhooks/supabase/${hookType}?workspace_id=${workspaceID}&integration_id=${integrationID}`
}

// Component Props
interface IntegrationsProps {
  workspace: Workspace | null
  onSave: (updatedWorkspace: Workspace) => Promise<void>
  loading: boolean
  isOwner: boolean
}


// Console assumée, lot 3 (08/10/2026) : les profils d'envoi (SMTP, Gmail, API) et les
// boîtes IMAP vivent dans la page « Profils d'envoi ». Cet écran ne garde que les
// autres intégrations (Supabase, LLM, Firecrawl) déjà présentes.

// Main Integrations component
export function Integrations({ workspace, onSave, isOwner }: IntegrationsProps) {
  const { t } = useLingui()

  // Drawer state
  const [supabaseDrawerVisible, setSupabaseDrawerVisible] = useState(false)
  const [editingSupabaseIntegration, setEditingSupabaseIntegration] = useState<Integration | null>(
    null
  )
  const [supabaseSaving, setSupabaseSaving] = useState(false)
  const supabaseFormRef = React.useRef<{ submit: () => void } | null>(null)

  // LLM Integration state
  const [llmDrawerVisible, setLLMDrawerVisible] = useState(false)
  const [editingLLMIntegration, setEditingLLMIntegration] = useState<Integration | null>(null)
  const [selectedLLMProvider, setSelectedLLMProvider] = useState<LLMProviderKind | null>(null)
  const [llmSaving, setLLMSaving] = useState(false)
  const llmFormRef = React.useRef<{ submit: () => void } | null>(null)

  // Firecrawl Integration state
  const [firecrawlDrawerVisible, setFirecrawlDrawerVisible] = useState(false)
  const [editingFirecrawlIntegration, setEditingFirecrawlIntegration] =
    useState<Integration | null>(null)
  const [firecrawlSaving, setFirecrawlSaving] = useState(false)
  const firecrawlFormRef = React.useRef<{ submit: () => void } | null>(null)

  // Lists state for Supabase integration
  const [lists, setLists] = useState<{ id: string; name: string }[]>([])

  // Fetch lists for Supabase integration display
  useEffect(() => {
    const fetchLists = async () => {
      if (!workspace) return
      try {
        const listsResponse = await listsApi.list({
          workspace_id: workspace.id
        })
        setLists(listsResponse.lists || [])
      } catch (error) {
        console.error('Failed to fetch lists:', error)
        setLists([])
      }
    }
    fetchLists()
    // eslint-disable-next-line react-hooks/exhaustive-deps -- Only re-run on workspace change
  }, [workspace?.id])

  if (!workspace) {
    return null
  }


  // Start editing a Supabase integration
  const startEditSupabaseIntegration = (integration: Integration) => {
    setEditingSupabaseIntegration(integration)
    setSupabaseDrawerVisible(true)
  }

  // Save Supabase integration
  const saveSupabaseIntegration = async (integration: Integration) => {
    setSupabaseSaving(true)
    try {
      if (editingSupabaseIntegration) {
        // Update existing integration
        await workspaceService.updateIntegration({
          workspace_id: workspace.id,
          integration_id: integration.id,
          name: integration.name,
          supabase_settings: integration.supabase_settings
        })
      } else {
        // Create new integration
        await workspaceService.createIntegration({
          workspace_id: workspace.id,
          name: integration.name,
          type: 'supabase',
          supabase_settings: integration.supabase_settings
        })
      }

      // Refresh workspace data
      const response = await workspaceService.get(workspace.id)
      await onSave(response.workspace)

      setSupabaseDrawerVisible(false)
      setEditingSupabaseIntegration(null)
      message.success(t`Supabase integration saved successfully`)
    } catch (error) {
      console.error('Error saving Supabase integration:', error)
      message.error(t`Failed to save Supabase integration`)
      throw error
    } finally {
      setSupabaseSaving(false)
    }
  }

  // Start editing an LLM integration
  const startEditLLMIntegration = (integration: Integration) => {
    setEditingLLMIntegration(integration)
    setSelectedLLMProvider(integration.llm_provider?.kind || 'anthropic')
    setLLMDrawerVisible(true)
  }

  // Save LLM integration
  const saveLLMIntegration = async (integration: Integration) => {
    setLLMSaving(true)
    try {
      if (editingLLMIntegration) {
        // Update existing integration
        await workspaceService.updateIntegration({
          workspace_id: workspace.id,
          integration_id: integration.id,
          name: integration.name,
          llm_provider: integration.llm_provider
        })
      } else {
        // Create new integration
        await workspaceService.createIntegration({
          workspace_id: workspace.id,
          name: integration.name,
          type: 'llm',
          llm_provider: integration.llm_provider
        })
      }

      // Refresh workspace data
      const response = await workspaceService.get(workspace.id)
      await onSave(response.workspace)

      setLLMDrawerVisible(false)
      setEditingLLMIntegration(null)
      setSelectedLLMProvider(null)
      message.success(t`LLM integration saved successfully`)
    } catch (error) {
      console.error('Error saving LLM integration:', error)
      message.error(t`Failed to save LLM integration`)
      throw error
    } finally {
      setLLMSaving(false)
    }
  }

  // Start editing a Firecrawl integration
  const startEditFirecrawlIntegration = (integration: Integration) => {
    setEditingFirecrawlIntegration(integration)
    setFirecrawlDrawerVisible(true)
  }

  // Save Firecrawl integration
  const saveFirecrawlIntegration = async (integration: Integration) => {
    setFirecrawlSaving(true)
    try {
      if (editingFirecrawlIntegration) {
        // Update existing integration
        await workspaceService.updateIntegration({
          workspace_id: workspace.id,
          integration_id: integration.id,
          name: integration.name,
          firecrawl_settings: integration.firecrawl_settings
        })
      } else {
        // Create new integration
        await workspaceService.createIntegration({
          workspace_id: workspace.id,
          name: integration.name,
          type: 'firecrawl',
          firecrawl_settings: integration.firecrawl_settings
        })
      }

      // Refresh workspace data
      const response = await workspaceService.get(workspace.id)
      await onSave(response.workspace)

      setFirecrawlDrawerVisible(false)
      setEditingFirecrawlIntegration(null)
      message.success(t`Firecrawl integration saved successfully`)
    } catch (error) {
      console.error('Error saving Firecrawl integration:', error)
      message.error(t`Failed to save Firecrawl integration`)
      throw error
    } finally {
      setFirecrawlSaving(false)
    }
  }


  // Delete an integration
  const deleteIntegration = async (integrationId: string) => {
    if (!workspace) return

    try {
      const deleteRequest: DeleteIntegrationRequest = {
        workspace_id: workspace.id,
        integration_id: integrationId
      }

      await workspaceService.deleteIntegration(deleteRequest)

      // Refresh workspace data
      const response = await workspaceService.get(workspace.id)
      await onSave(response.workspace)

      message.success(t`Integration deleted successfully`)
    } catch (error) {
      console.error('Error deleting integration', error)
      message.error(t`Failed to delete integration`)
    }
  }

  // Render the list of integrations
  const renderWorkspaceIntegrations = () => {
    if (!workspace?.integrations) {
      return null // We'll handle this case differently in the main render
    }

    return (
      <>
        {workspace?.integrations.map((integration) => {
          if (integration.type === 'supabase') {
            const hasAuthEmailHook =
              !!integration.supabase_settings?.auth_email_hook?.has_signature_key
            const hasBeforeUserCreatedHook =
              !!integration.supabase_settings?.before_user_created_hook?.has_signature_key
            const addToLists =
              integration.supabase_settings?.before_user_created_hook?.add_user_to_lists || []
            const customJsonField =
              integration.supabase_settings?.before_user_created_hook?.custom_json_field
            const rejectDisposableEmail =
              integration.supabase_settings?.before_user_created_hook?.reject_disposable_email

            // Generate webhook URLs dynamically
            const authEmailWebhookURL = generateSupabaseWebhookURL(
              'auth-email',
              workspace.id,
              integration.id
            )
            const beforeUserCreatedWebhookURL = generateSupabaseWebhookURL(
              'before-user-created',
              workspace.id,
              integration.id
            )

            return (
              <div key={integration.id} className="mb-4">
                <Card
                  title={
                    <>
                      <div className="float-right">
                        {isOwner && (
                          <Space>
                            <Tooltip title={t`Edit`}>
                              <Button
                                type="text"
                                onClick={() => startEditSupabaseIntegration(integration)}
                                size="small"
                              >
                                <FontAwesomeIcon icon={faPenToSquare} />
                              </Button>
                            </Tooltip>
                            <Popconfirm
                              title={t`Delete this integration?`}
                              description={t`This action cannot be undone.`}
                              onConfirm={() => deleteIntegration(integration.id)}
                              okText={t`Yes`}
                              cancelText={t`No`}
                            >
                              <Tooltip title={t`Delete`}>
                                <Button size="small" type="text">
                                  <FontAwesomeIcon icon={faTrashCan} />
                                </Button>
                              </Tooltip>
                            </Popconfirm>
                          </Space>
                        )}
                      </div>
                      <Tooltip title={integration.id}>
                        <img src="/console/supabase.png" alt="Supabase" style={{ height: 24 }} />
                      </Tooltip>
                    </>
                  }
                >
                  <Descriptions bordered size="small" column={1} className="mt-2">
                    <Descriptions.Item label={t`Name`}>{integration.name}</Descriptions.Item>
                    <Descriptions.Item label={t`Auth Email Hook`}>
                      {hasAuthEmailHook ? (
                        <Space direction="vertical">
                          <Tag bordered={false} color="green" className="mb-2">
                            <FontAwesomeIcon icon={faCheck} className="mr-1" /> {t`Configured`}
                          </Tag>
                          <div className="mt-2 text-xs text-gray-500">{t`Webhook endpoint:`}</div>

                          <Input
                            value={authEmailWebhookURL}
                            readOnly
                            size="small"
                            variant="filled"
                            suffix={
                              <Tooltip title={t`Copy Webhook endpoint`}>
                                <Button
                                  type="link"
                                  size="small"
                                  onClick={() => {
                                    navigator.clipboard.writeText(authEmailWebhookURL)
                                    message.success(t`Webhook endpoint copied to clipboard`)
                                  }}
                                  icon={<FontAwesomeIcon icon={faCopy} />}
                                  className="mt-1"
                                >
                                  {t`Copy`}
                                </Button>
                              </Tooltip>
                            }
                          />
                        </Space>
                      ) : (
                        <Tag bordered={false} color="default">
                          {t`Not configured`}
                        </Tag>
                      )}
                    </Descriptions.Item>
                    <Descriptions.Item label={t`Before User Created Hook`}>
                      {hasBeforeUserCreatedHook ? (
                        <Space direction="vertical">
                          <Tag bordered={false} color="green" className="mb-2">
                            <FontAwesomeIcon icon={faCheck} className="mr-1" /> {t`Configured`}
                          </Tag>
                          <div className="mt-2 text-xs text-gray-500">{t`Webhook endpoint:`}</div>

                          <Input
                            value={beforeUserCreatedWebhookURL}
                            readOnly
                            size="small"
                            variant="filled"
                            suffix={
                              <Tooltip title={t`Copy Webhook endpoint`}>
                                <Button
                                  type="link"
                                  size="small"
                                  onClick={() => {
                                    navigator.clipboard.writeText(beforeUserCreatedWebhookURL)
                                    message.success(t`Webhook endpoint copied to clipboard`)
                                  }}
                                  icon={<FontAwesomeIcon icon={faCopy} />}
                                  className="mt-1"
                                >
                                  {t`Copy`}
                                </Button>
                              </Tooltip>
                            }
                          />
                        </Space>
                      ) : (
                        <Tag bordered={false} color="default">
                          {t`Not configured`}
                        </Tag>
                      )}
                    </Descriptions.Item>
                    {hasBeforeUserCreatedHook && addToLists.length > 0 && (
                      <Descriptions.Item label={t`Auto-subscribe to Lists`}>
                        {addToLists.map((listId) => {
                          const list = lists.find((l) => l.id === listId)
                          return (
                            <Tag key={listId} bordered={false} color="blue" className="mb-1">
                              {list?.name || listId}
                            </Tag>
                          )
                        })}
                      </Descriptions.Item>
                    )}
                    {hasBeforeUserCreatedHook && customJsonField && (
                      <Descriptions.Item label={t`User Metadata Field`}>
                        <Tag bordered={false} color="purple">
                          {workspace.settings?.custom_field_labels?.[customJsonField] ||
                            customJsonField}
                        </Tag>
                      </Descriptions.Item>
                    )}
                    {hasBeforeUserCreatedHook && (
                      <Descriptions.Item label={t`Reject Disposable Email`}>
                        <Tag bordered={false} color={rejectDisposableEmail ? 'green' : 'default'}>
                          {rejectDisposableEmail ? (
                            <>
                              <FontAwesomeIcon icon={faCheck} className="mr-1" /> {t`Enabled`}
                            </>
                          ) : (
                            <>
                              <FontAwesomeIcon icon={faTimes} className="mr-1" /> {t`Disabled`}
                            </>
                          )}
                        </Tag>
                      </Descriptions.Item>
                    )}
                  </Descriptions>
                </Card>
              </div>
            )
          }

          if (integration.type === 'llm' && integration.llm_provider) {
            const provider = integration.llm_provider

            return (
              <div key={integration.id} className="mb-4">
                <Card
                  title={
                    <>
                      <div className="float-right">
                        {isOwner && (
                          <Space>
                            <Tooltip title={t`Edit`}>
                              <Button
                                type="text"
                                onClick={() => startEditLLMIntegration(integration)}
                                size="small"
                              >
                                <FontAwesomeIcon icon={faPenToSquare} />
                              </Button>
                            </Tooltip>
                            <Popconfirm
                              title={t`Delete this integration?`}
                              description={t`This action cannot be undone.`}
                              onConfirm={() => deleteIntegration(integration.id)}
                              okText={t`Yes`}
                              cancelText={t`No`}
                            >
                              <Tooltip title={t`Delete`}>
                                <Button size="small" type="text">
                                  <FontAwesomeIcon icon={faTrashCan} />
                                </Button>
                              </Tooltip>
                            </Popconfirm>
                          </Space>
                        )}
                      </div>
                      <Tooltip title={integration.id}>
                        {getLLMProviderIcon(provider.kind, 14)}
                      </Tooltip>
                    </>
                  }
                >
                  <Descriptions bordered size="small" column={1} className="mt-2">
                    <Descriptions.Item label={t`Name`}>{integration.name}</Descriptions.Item>
                    <Descriptions.Item label={t`Model`}>
                      <Tag bordered={false} color="purple">
                        {provider.kind === 'openai'
                          ? provider.openai?.model || 'Not configured'
                          : provider.anthropic?.model || 'Not configured'}
                      </Tag>
                    </Descriptions.Item>
                    {provider.kind === 'openai' && provider.openai?.base_url && (
                      <Descriptions.Item label={t`Base URL`}>
                        <Tag bordered={false} color="blue">
                          {provider.openai.base_url}
                        </Tag>
                      </Descriptions.Item>
                    )}
                    <Descriptions.Item label={t`API Key`}>
                      <Tag bordered={false} color="green">
                        <FontAwesomeIcon icon={faCheck} className="mr-1" /> {t`Configured`}
                      </Tag>
                    </Descriptions.Item>
                  </Descriptions>
                </Card>
              </div>
            )
          }

          if (integration.type === 'firecrawl' && integration.firecrawl_settings) {
            return (
              <div key={integration.id} className="mb-4">
                <Card
                  title={
                    <>
                      <div className="float-right">
                        {isOwner && (
                          <Space>
                            <Tooltip title={t`Edit`}>
                              <Button
                                type="text"
                                onClick={() => startEditFirecrawlIntegration(integration)}
                                size="small"
                              >
                                <FontAwesomeIcon icon={faPenToSquare} />
                              </Button>
                            </Tooltip>
                            <Popconfirm
                              title={t`Delete this integration?`}
                              description={t`This action cannot be undone.`}
                              onConfirm={() => deleteIntegration(integration.id)}
                              okText={t`Yes`}
                              cancelText={t`No`}
                            >
                              <Tooltip title={t`Delete`}>
                                <Button size="small" type="text">
                                  <FontAwesomeIcon icon={faTrashCan} />
                                </Button>
                              </Tooltip>
                            </Popconfirm>
                          </Space>
                        )}
                      </div>
                      <Tooltip title={integration.id}>{firecrawlProvider.getIcon('', 14)}</Tooltip>
                    </>
                  }
                >
                  <Descriptions bordered size="small" column={1} className="mt-2">
                    <Descriptions.Item label={t`Name`}>{integration.name}</Descriptions.Item>
                    <Descriptions.Item label={t`API Key`}>
                      <Tag bordered={false} color="green">
                        <FontAwesomeIcon icon={faCheck} className="mr-1" /> {t`Configured`}
                      </Tag>
                    </Descriptions.Item>
                    <Descriptions.Item label={t`Tools`}>
                      <Space>
                        <Tag bordered={false} color="blue">
                          scrape_url
                        </Tag>
                        <Tag bordered={false} color="blue">
                          search_web
                        </Tag>
                      </Space>
                    </Descriptions.Item>
                  </Descriptions>
                </Card>
              </div>
            )
          }

          // Profils d'envoi et boîtes IMAP : page « Profils d'envoi ».
          return null
        })}
      </>
    )
  }

  const hasOtherIntegrations = (workspace.integrations || []).some(
    (integration) =>
      integration.type === 'supabase' ||
      (integration.type === 'llm' && !!integration.llm_provider) ||
      (integration.type === 'firecrawl' && !!integration.firecrawl_settings)
  )

  return (
    <>
      <SettingsSectionHeader
        title={t`Integrations`}
        description={t`Connections to other services.`}
      />

      <Alert
        type="info"
        showIcon
        style={{ marginBottom: 16 }}
        message={t`Sending profiles and return inboxes have their own page.`}
        description={
          <Link
            to="/console/workspace/$workspaceId/sending-profiles"
            params={{ workspaceId: workspace.id }}
          >
            {t`Open Sending profiles`}
          </Link>
        }
      />

      {!isOwner && <IntegrationOwnerNotice />}

      {!hasOtherIntegrations && (
        <div style={{ color: '#8c8c8c', marginBottom: 16 }}>{t`No other integration is connected.`}</div>
      )}

      {renderWorkspaceIntegrations()}

      {/* Supabase Integration Drawer */}
      <Drawer
        title={
          editingSupabaseIntegration ? 'Edit SUPABASE Integration' : 'Add New SUPABASE Integration'
        }
        width={600}
        open={supabaseDrawerVisible}
        onClose={() => {
          setSupabaseDrawerVisible(false)
          setEditingSupabaseIntegration(null)
        }}
        footer={
          <div style={{ textAlign: 'right' }}>
            <Space>
              <Button
                onClick={() => {
                  setSupabaseDrawerVisible(false)
                  setEditingSupabaseIntegration(null)
                }}
              >
                {t`Cancel`}
              </Button>
              <Button
                type="primary"
                onClick={() => supabaseFormRef.current?.submit()}
                loading={supabaseSaving}
                disabled={!isOwner}
              >
                {t`Save`}
              </Button>
            </Space>
          </div>
        }
        destroyOnClose
      >
        <SupabaseIntegration
          integration={editingSupabaseIntegration || undefined}
          workspace={workspace}
          onSave={saveSupabaseIntegration}
          isOwner={isOwner}
          formRef={supabaseFormRef}
        />
      </Drawer>

      {/* LLM Integration Drawer */}
      <Drawer
        title={
          editingLLMIntegration
            ? `Edit ${getLLMProviderName(selectedLLMProvider || 'anthropic').toUpperCase()} Integration`
            : `Add New ${getLLMProviderName(selectedLLMProvider || 'anthropic').toUpperCase()} Integration`
        }
        width={600}
        open={llmDrawerVisible}
        onClose={() => {
          setLLMDrawerVisible(false)
          setEditingLLMIntegration(null)
          setSelectedLLMProvider(null)
        }}
        footer={
          <div style={{ textAlign: 'right' }}>
            <Space>
              <Button
                onClick={() => {
                  setLLMDrawerVisible(false)
                  setEditingLLMIntegration(null)
                  setSelectedLLMProvider(null)
                }}
              >
                {t`Cancel`}
              </Button>
              <Button
                type="primary"
                onClick={() => llmFormRef.current?.submit()}
                loading={llmSaving}
                disabled={!isOwner}
              >
                {t`Save`}
              </Button>
            </Space>
          </div>
        }
        destroyOnClose
      >
        {selectedLLMProvider && (
          <LLMIntegration
            integration={editingLLMIntegration || undefined}
            workspace={workspace}
            providerKind={selectedLLMProvider}
            onSave={saveLLMIntegration}
            isOwner={isOwner}
            formRef={llmFormRef}
          />
        )}
      </Drawer>

      {/* Firecrawl Integration Drawer */}
      <Drawer
        title={
          editingFirecrawlIntegration ? 'Edit Firecrawl Integration' : 'Add Firecrawl Integration'
        }
        width={600}
        open={firecrawlDrawerVisible}
        onClose={() => {
          setFirecrawlDrawerVisible(false)
          setEditingFirecrawlIntegration(null)
        }}
        footer={
          <div style={{ textAlign: 'right' }}>
            <Space>
              <Button
                onClick={() => {
                  setFirecrawlDrawerVisible(false)
                  setEditingFirecrawlIntegration(null)
                }}
              >
                {t`Cancel`}
              </Button>
              <Button
                type="primary"
                onClick={() => firecrawlFormRef.current?.submit()}
                loading={firecrawlSaving}
                disabled={!isOwner}
              >
                {t`Save`}
              </Button>
            </Space>
          </div>
        }
        destroyOnClose
      >
        <FirecrawlIntegration
          integration={editingFirecrawlIntegration || undefined}
          workspace={workspace}
          onSave={saveFirecrawlIntegration}
          isOwner={isOwner}
          formRef={firecrawlFormRef}
        />
      </Drawer>
    </>
  )
}


export function IntegrationOwnerNotice() {
  const { t } = useLingui()
  return (
    <Alert type="warning" showIcon message={t`Only workspace owners can modify integrations`} />
  )
}
