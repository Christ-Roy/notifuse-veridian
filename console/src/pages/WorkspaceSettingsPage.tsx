import { useState, useEffect } from 'react'
import { useParams, useNavigate } from '@tanstack/react-router'
import { Button, Drawer, Grid, Layout } from 'antd'
import { MenuOutlined } from '@ant-design/icons'
import { useLingui } from '@lingui/react/macro'
import { workspaceService } from '../services/api/workspace'
import { Workspace, WorkspaceMember } from '../services/api/types'
import { WorkspaceMembers } from '../components/settings/WorkspaceMembers'
import { ApiAgentsSettings } from '../components/settings/ApiAgentsSettings'
import { GeneralSettings } from '../components/settings/GeneralSettings'
import { Integrations } from '../components/settings/Integrations'
import { CustomFieldsConfiguration } from '../components/settings/CustomFieldsConfiguration'
import { WebhooksSettings } from '../components/settings/WebhooksSettings'
import { useAuth } from '../contexts/AuthContext'
import { DeleteWorkspaceSection } from '../components/settings/DeleteWorkspace'
import { SettingsSidebar, SettingsSection } from '../components/settings/SettingsSidebar'
// === Veridian patch === section Plan ajoutée au sidebar settings.
import { isWorkspaceAdministrator, isWorkspaceOwner } from '../lib/veridian_workspace_role'
import { VeridianPlanSettings } from '../components/settings/veridian_plan_settings'
import { VeridianMailboxAccess } from '../components/settings/veridian_mailbox_access'
import { VeridianSendingDomains } from '../components/settings/veridian_sending_domains'
import { VeridianColdOutreachSettings } from '../components/settings/veridian_cold_outreach_settings'

const { Sider, Content } = Layout

export function WorkspaceSettingsPage() {
  const { t } = useLingui()
  const { workspaceId, section } = useParams({
    from: '/console/workspace/$workspaceId/settings/$section'
  })
  const [workspace, setWorkspace] = useState<Workspace | null>(null)
  const [members, setMembers] = useState<WorkspaceMember[]>([])
  const [loadingMembers, setLoadingMembers] = useState(false)
  const [isOwner, setIsOwner] = useState(false)
  // Veridian : « administre » = owner ou admin (isOwner garde son nom en prop) ; la suppression
  // de l'espace reste au proprietaire seul (isWorkspaceOwner).
  const [isStrictOwner, setIsStrictOwner] = useState(false)
  // === Veridian patch — mission "API & agents" (2026-10-03) === "admin" au
  // sens de ce fork : owner, ou member avec write sur la ressource
  // "workspace" (il n'existe pas de rôle "admin" littéral côté backend).
  const [canManageApiKeys, setCanManageApiKeys] = useState(false)
  const { refreshWorkspaces, user, workspaces } = useAuth()
  const navigate = useNavigate()
  // Téléphone : le menu des réglages passe dans un tiroir, la page prend toute la largeur (marges de 16 px).
  const screens = Grid.useBreakpoint()
  const isMobile = screens.md === false
  const [menuOpen, setMenuOpen] = useState(false)

  // Valid settings sections
  const validSections: SettingsSection[] = [
    'team',
    'integrations',
    'mailboxes',
    'domains',
    'webhooks',
    'custom-fields',
    'general',
    'plan',
    'cold-outreach',
    'api-agents',
    'danger-zone'
  ]

  // Get active section from URL or default to 'team'
  const activeSection: SettingsSection = validSections.includes(section as SettingsSection)
    ? (section as SettingsSection)
    : 'team'

  useEffect(() => {
    // Redirect to team section if invalid section is provided
    if (!validSections.includes(section as SettingsSection)) {
      navigate({
        to: '/console/workspace/$workspaceId/settings/$section',
        params: { workspaceId, section: 'team' },
        replace: true
      })
    }
  // eslint-disable-next-line react-hooks/exhaustive-deps -- validSections is static
  }, [section, workspaceId, navigate])

  useEffect(() => {
    // Find the workspace from the auth context
    const currentWorkspace = workspaces.find((w) => w.id === workspaceId) || null
    setWorkspace(currentWorkspace)

    fetchMembers()
  // eslint-disable-next-line react-hooks/exhaustive-deps -- fetchMembers is stable
  }, [workspaceId, workspaces])

  const fetchMembers = async () => {
    setLoadingMembers(true)
    try {
      const response = await workspaceService.getMembers(workspaceId)
      setMembers(response.members)

      // Check if current user is an owner
      if (user) {
        const currentUserMember = response.members.find((member) => member.user_id === user.id)
        const owner = isWorkspaceAdministrator(currentUserMember?.role)
        setIsOwner(owner)
        setIsStrictOwner(isWorkspaceOwner(currentUserMember?.role))
        setCanManageApiKeys(owner || currentUserMember?.permissions?.workspace?.write === true)
      }
    } catch (error) {
      console.error(t`Failed to fetch workspace members`, error)
    } finally {
      setLoadingMembers(false)
    }
  }

  const handleWorkspaceUpdate = async (updatedWorkspace: Workspace) => {
    setWorkspace(updatedWorkspace)
    // Refresh the workspaces in auth context to stay in sync
    await refreshWorkspaces()
  }

  const handleWorkspaceDelete = async () => {
    navigate({ to: '/console' })
    await refreshWorkspaces()
  }

  const handleSectionChange = (newSection: SettingsSection) => {
    setMenuOpen(false)
    navigate({
      to: '/console/workspace/$workspaceId/settings/$section',
      params: { workspaceId, section: newSection }
    })
  }

  const renderSection = () => {
    switch (activeSection) {
      case 'team':
        return (
          <WorkspaceMembers
            workspaceId={workspaceId}
            members={members}
            loading={loadingMembers}
            onMembersChange={fetchMembers}
            isOwner={isOwner}
            currentUserId={user?.id}
          />
        )
      case 'integrations':
        return (
          <Integrations
            workspace={workspace}
            loading={false}
            onSave={handleWorkspaceUpdate}
            isOwner={isOwner}
          />
        )
      case 'mailboxes':
        // === Veridian patch === accès de boîte (IMAP, SMTP) : fiche 84.
        return <VeridianMailboxAccess workspaceId={workspaceId} workspace={workspace} isOwner={isOwner} />
      case 'domains':
        // === Veridian patch === domaines d'envoi authentifiés : fiche 97.
        return <VeridianSendingDomains workspaceId={workspaceId} />
      case 'webhooks':
        return workspace ? <WebhooksSettings workspaceId={workspace.id} /> : null
      case 'custom-fields':
        return (
          <CustomFieldsConfiguration
            workspace={workspace}
            onWorkspaceUpdate={handleWorkspaceUpdate}
            isOwner={isOwner}
          />
        )
      case 'general':
        return (
          <GeneralSettings
            workspace={workspace}
            onWorkspaceUpdate={handleWorkspaceUpdate}
            isOwner={isOwner}
          />
        )
      case 'plan':
        // === Veridian patch === section Plan : badge plan_source + CTA Hub.
        return <VeridianPlanSettings workspaceId={workspaceId} />
      case 'cold-outreach':
        // === Veridian patch === config tunnel cold outreach (débits + pixel/classe).
        return (
          <VeridianColdOutreachSettings
            workspace={workspace}
            onWorkspaceUpdate={handleWorkspaceUpdate}
            isOwner={isOwner}
          />
        )
      case 'api-agents':
        return <ApiAgentsSettings workspaceId={workspaceId} canManageKeys={canManageApiKeys} />
      case 'danger-zone':
        return workspace && isStrictOwner ? (
          <DeleteWorkspaceSection workspace={workspace} onDeleteSuccess={handleWorkspaceDelete} />
        ) : null
      default:
        return null
    }
  }

  const sidebar = (
    <SettingsSidebar activeSection={activeSection} onSectionChange={handleSectionChange}
      isOwner={isOwner}
      canDeleteWorkspace={isStrictOwner}
    />
  )

  if (isMobile) {
    return (
      <Layout style={{ minHeight: 'calc(100vh - 48px)' }}>
        <Content>
          <div style={{ padding: '12px 16px 0' }}>
            <Button icon={<MenuOutlined />} onClick={() => setMenuOpen(true)}>
              {t`Settings`}
            </Button>
          </div>
          <Drawer
            open={menuOpen}
            onClose={() => setMenuOpen(false)}
            placement="left"
            width="min(280px, 85vw)"
            styles={{ body: { padding: 0 } }}
            closable={false}
          >
            {sidebar}
          </Drawer>
          <div style={{ padding: 16, maxWidth: '100%', overflowWrap: 'anywhere' }}>{renderSection()}</div>
        </Content>
      </Layout>
    )
  }

  return (
    <Layout style={{ minHeight: 'calc(100vh - 48px)' }}>
      <Sider
        width={250}
        style={{
          borderRight: '1px solid #f0f0f0',
          overflow: 'auto'
        }}
      >
        {sidebar}
      </Sider>
      <Layout>
        <Content>
          <div style={{ maxWidth: '700px', padding: '24px' }}>{renderSection()}</div>
        </Content>
      </Layout>
    </Layout>
  )
}
