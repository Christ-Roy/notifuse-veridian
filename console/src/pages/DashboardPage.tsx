import { Card, Typography, Button, Empty } from 'antd'
import { PlusOutlined } from '@ant-design/icons'
import { useEffect } from 'react'
import { useAuth } from '../contexts/AuthContext'
import { useNavigate, useSearch } from '@tanstack/react-router'
import { MainLayout, MainLayoutSidebar } from '../layouts/MainLayout'
import { isRootUser } from '../services/api/auth'
import { useLingui } from '@lingui/react/macro'
import { SystemSettingsDrawer } from '../components/settings/SystemSettingsDrawer'
import { recallSpace } from '../utils/veridian_last_space'

const { Text } = Typography

export function DashboardPage() {
  const { t } = useLingui()
  const { workspaces, user } = useAuth()
  const navigate = useNavigate()

  // Retour du Hub sans espace demandé : le dernier que cette personne a ouvert dans ce navigateur, sinon elle choisit. Jamais un autre à sa place.
  const { rebond } = useSearch({ strict: false }) as { rebond?: unknown }
  const remembered = rebond === undefined ? undefined : recallSpace(user?.id)
  const target = workspaces.some((w) => w.id === remembered) ? remembered : undefined
  useEffect(() => {
    if (target) navigate({ to: '/console/workspace/$workspaceId', params: { workspaceId: target }, replace: true })
  }, [target, navigate])

  const handleWorkspaceClick = (workspaceId: string) => {
    navigate({
      to: '/console/workspace/$workspaceId',
      params: { workspaceId }
    })
  }

  const handleCreateWorkspace = () => {
    navigate({ to: '/console/workspace/create' })
  }

  return (
    <MainLayout>
      <MainLayoutSidebar
        title={t`Select workspace`}
        extra={
          isRootUser(user?.email) ? (
            <div style={{ display: 'flex', gap: '8px' }}>
              <SystemSettingsDrawer />
              <Button
                type="primary"
                ghost
                icon={<PlusOutlined />}
                onClick={handleCreateWorkspace}
                style={{ padding: '4px', lineHeight: 1 }}
              />
            </div>
          ) : undefined
        }
      >
        {workspaces.length === 0 ? (
          <Empty description={t`No workspaces`} style={{ margin: '24px 0' }} />
        ) : (
          <div style={{ display: 'flex', flexDirection: 'column', gap: '12px' }}>
            {workspaces.map((workspace) => (
              <Card
                key={workspace.id}
                hoverable
                size="small"
                onClick={() => handleWorkspaceClick(workspace.id)}
                style={{ marginBottom: '8px' }}
              >
                <div style={{ display: 'flex', alignItems: 'center', gap: '8px' }}>
                  <div
                    style={{
                      width: '32px',
                      height: '32px',
                      display: 'flex',
                      alignItems: 'center',
                      justifyContent: 'center',
                      background: workspace.settings.logo_url ? '#f5f5f5' : '#e6f7ff',
                      borderRadius: '4px',
                      overflow: 'hidden'
                    }}
                  >
                    {workspace.settings.logo_url ? (
                      <img
                        alt={workspace.name}
                        src={workspace.settings.logo_url}
                        style={{
                          maxWidth: '100%',
                          maxHeight: '100%',
                          objectFit: 'contain'
                        }}
                      />
                    ) : (
                      <Typography.Text strong style={{ color: '#1890ff' }}>
                        {workspace.name.substring(0, 2).toUpperCase()}
                      </Typography.Text>
                    )}
                  </div>
                  <div>
                    <div style={{ fontWeight: 500 }}>{workspace.name}</div>
                    <Text type="secondary" style={{ fontSize: '11px' }} ellipsis>
                      {t`ID:`} {workspace.id}
                    </Text>
                  </div>
                </div>
              </Card>
            ))}
          </div>
        )}
      </MainLayoutSidebar>
    </MainLayout>
  )
}
