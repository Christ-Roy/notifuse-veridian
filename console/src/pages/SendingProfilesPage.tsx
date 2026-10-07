import { useCallback, useEffect, useState } from 'react'
import { useParams } from '@tanstack/react-router'
import { Alert, App, Button, Card, Empty, Skeleton, Space, Statistic, Typography } from 'antd'
import { PlusOutlined } from '@ant-design/icons'
import { useLingui } from '@lingui/react/macro'

import { useAuth } from '../contexts/AuthContext'
import { workspaceService } from '../services/api/workspace'
import type { Integration, Workspace } from '../services/api/types'
import {
  emailProfilesOverviewService,
  type EmailProfileInbox,
  type EmailProfileOverview,
  type EmailProfilesOverview
} from '../services/api/veridian_email_profiles'
import { VeridianProfileCard, type ProfileAction } from '../components/sending_profiles/veridian_profile_card'
import { VeridianProfileEditDrawer } from '../components/sending_profiles/veridian_profile_edit_drawer'
import { VeridianProfileWizard } from '../components/sending_profiles/veridian_profile_wizard'
import { VeridianInboxDrawer } from '../components/sending_profiles/veridian_inbox_drawer'
import { formatNumber } from '../components/sending_profiles/veridian_profile_labels'
import { groupProfiles } from '../components/sending_profiles/veridian_profile_rules'
import {
  deleteProfile,
  setPaused,
  setRotation,
  testProfile
} from '../components/sending_profiles/veridian_profile_ops'

const { Title, Text } = Typography

export function SendingProfilesPage() {
  const { t } = useLingui()
  const { message, modal } = App.useApp()
  const { workspaceId } = useParams({ strict: false }) as { workspaceId?: string }
  const { user } = useAuth()
  const [overview, setOverview] = useState<EmailProfilesOverview | null>(null)
  const [workspace, setWorkspace] = useState<Workspace | null>(null)
  const [isOwner, setIsOwner] = useState(false)
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState<string | null>(null)
  const [busy, setBusy] = useState<{ id: string; action: ProfileAction } | null>(null)
  const [wizardOpen, setWizardOpen] = useState(false)
  const [editing, setEditing] = useState<EmailProfileOverview | null>(null)
  const [inboxDrawer, setInboxDrawer] = useState<{ open: boolean; inbox: Integration | null }>({
    open: false,
    inbox: null
  })

  const reload = useCallback(async () => {
    if (!workspaceId) return
    try {
      const [ov, ws] = await Promise.all([
        emailProfilesOverviewService.get(workspaceId),
        workspaceService.get(workspaceId)
      ])
      setOverview(ov)
      setWorkspace(ws.workspace)
      setError(null)
    } catch (err) {
      setError(err instanceof Error ? err.message : t`Could not load the profiles`)
    } finally {
      setLoading(false)
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps -- t est stable pour une langue donnée
  }, [workspaceId])

  useEffect(() => {
    void reload()
  }, [reload])

  useEffect(() => {
    if (!workspaceId || !user) return
    let active = true
    workspaceService
      .getMembers(workspaceId)
      .then((response) => {
        if (!active) return
        setIsOwner(response.members.find((m) => m.user_id === user.id)?.role === 'owner')
      })
      .catch(() => {
        if (active) setIsOwner(false)
      })
    return () => {
      active = false
    }
  }, [workspaceId, user])

  if (!workspaceId) return <div>{t`Loading...`}</div>

  const run = async (profile: EmailProfileOverview, action: ProfileAction, work: () => Promise<string | void>) => {
    setBusy({ id: profile.integration_id, action })
    try {
      const done = await work()
      if (done) message.success(done)
      await reload()
    } catch (err) {
      message.error(err instanceof Error ? err.message : t`The action failed`)
    } finally {
      setBusy(null)
    }
  }

  const onAction = (action: ProfileAction, profile: EmailProfileOverview) => {
    const id = profile.integration_id
    switch (action) {
      case 'test': {
        const to = user?.email ?? ''
        void run(profile, action, async () => {
          const response = await testProfile(workspaceId, id, to)
          if (!response.success) throw new Error(response.error || t`The test message was refused`)
          return t`Test message sent to ${to}`
        })
        break
      }
      case 'pause':
        void run(profile, action, async () => {
          await setPaused(workspaceId, id, true)
          return t`Profile paused`
        })
        break
      case 'resume':
        void run(profile, action, async () => {
          await setPaused(workspaceId, id, false)
          return t`Profile resumed`
        })
        break
      case 'rotation': {
        const enable = !profile.in_rotation
        void run(profile, action, async () => {
          await setRotation(workspaceId, id, enable)
          return enable ? t`Profile added to the rotation` : t`Profile removed from the rotation`
        })
        break
      }
      case 'edit':
        setEditing(profile)
        break
      case 'delete':
        void run(profile, action, async () => {
          await deleteProfile(workspaceId, id)
          return t`Profile deleted`
        })
        break
    }
  }

  const groups = groupProfiles(overview?.profiles ?? [])
  const totals = overview?.totals
  const allProfiles = overview?.profiles ?? []
  const imapIntegration = (inbox: EmailProfileInbox): Integration | null =>
    (workspace?.integrations || []).find((i) => i.id === inbox.integration_id) ?? null

  const confirmDeleteInbox = (inbox: EmailProfileInbox) => {
    modal.confirm({
      title: t`Delete this return inbox?`,
      content: t`Replies and bounces will no longer be read from ${inbox.address}.`,
      okText: t`Delete`,
      okButtonProps: { danger: true },
      cancelText: t`Cancel`,
      onOk: async () => {
        try {
          await workspaceService.deleteIntegration({
            workspace_id: workspaceId,
            integration_id: inbox.integration_id
          })
          message.success(t`Inbox deleted`)
          await reload()
        } catch (err) {
          message.error(err instanceof Error ? err.message : t`The action failed`)
        }
      }
    })
  }

  const section = (title: string, hint: string, profiles: EmailProfileOverview[], testId: string) => (
    <section data-testid={testId} style={{ marginBottom: 24 }}>
      <Title level={5} style={{ marginBottom: 2 }}>
        {title}
      </Title>
      <Text type="secondary">{hint}</Text>
      <div style={{ marginTop: 12 }}>
        {profiles.length === 0 ? (
          <Empty image={Empty.PRESENTED_IMAGE_SIMPLE} description={t`No profile here yet`} />
        ) : (
          profiles.map((profile) => (
            <VeridianProfileCard
              key={profile.integration_id}
              profile={profile}
              all={allProfiles}
              isOwner={isOwner}
              busy={busy?.id === profile.integration_id ? busy.action : null}
              onAction={onAction}
            />
          ))
        )}
      </div>
    </section>
  )

  return (
    <div className="p-6" style={{ maxWidth: 980 }}>
      <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'flex-start', marginBottom: 16 }}>
        <div>
          <div className="text-2xl font-medium">{t`Sending profiles`}</div>
          <Text type="secondary">
            {t`Every sender account, its rules for today, its reputation and its return inbox.`}
          </Text>
        </div>
        {isOwner && (
          <Button type="primary" icon={<PlusOutlined />} onClick={() => setWizardOpen(true)}>
            {t`Add a profile`}
          </Button>
        )}
      </div>

      {error && <Alert type="error" showIcon message={error} style={{ marginBottom: 16 }} />}
      {loading && <Skeleton active />}

      {overview && totals && (
        <>
          <Space size={32} wrap style={{ marginBottom: 20 }} data-testid="totals">
            <Statistic
              title={t`Commercial sent today`}
              value={totals.commercial_sent_today}
              formatter={(value) => formatNumber(Number(value))}
            />
            <Statistic
              title={t`Commercial capacity today`}
              value={totals.commercial_capacity_today === null ? t`No cap` : totals.commercial_capacity_today}
              formatter={(value) => (typeof value === 'number' ? formatNumber(value) : String(value))}
            />
            <Statistic title={t`Paused profiles`} value={totals.paused_profiles} />
            <Statistic
              title={t`Transactional sent today`}
              value={totals.transactional_sent_today}
              formatter={(value) => formatNumber(Number(value))}
            />
          </Space>

          {overview.usage_conflicts.length > 0 && (
            <Alert
              type="error"
              showIcon
              style={{ marginBottom: 16 }}
              message={t`Some profiles are both in the commercial rotation and transactional`}
              description={t`Open Edit on each of them and pick a single usage.`}
            />
          )}

          {allProfiles.length === 0 && (
            <Empty description={t`No sending profile yet`}>
              {isOwner && (
                <Button type="primary" onClick={() => setWizardOpen(true)}>
                  {t`Add a profile`}
                </Button>
              )}
            </Empty>
          )}

          {allProfiles.length > 0 && (
            <>
              {section(
                t`Commercial`,
                t`Profiles used by campaigns and sequences. Those in the rotation share the volume.`,
                groups.commercial,
                'section-commercial'
              )}
              {section(
                t`Transactional`,
                t`The profile reserved for mail sent from your applications. It has no commercial rule.`,
                groups.transactional,
                'section-transactional'
              )}
            </>
          )}

          <Card
            size="small"
            data-testid="global-inboxes"
            title={t`Workspace return inboxes`}
            extra={
              isOwner && (
                <Button size="small" onClick={() => setInboxDrawer({ open: true, inbox: null })}>
                  {t`Add an inbox`}
                </Button>
              )
            }
          >
            <Text type="secondary">
              {t`Inboxes linked to no profile. Replies and bounces are read from every inbox of the workspace.`}
            </Text>
            {overview.global_inboxes.length === 0 ? (
              <div style={{ marginTop: 8 }}>
                <Text type="secondary">{t`No unlinked inbox`}</Text>
              </div>
            ) : (
              overview.global_inboxes.map((inbox) => (
                <div
                  key={inbox.integration_id}
                  style={{ display: 'flex', justifyContent: 'space-between', marginTop: 8 }}
                >
                  <span>
                    <Text strong>{inbox.address}</Text>
                    <Text type="secondary">
                      {' '}
                      ({inbox.host}
                      {inbox.folder ? `, ${inbox.folder}` : ''})
                    </Text>
                  </span>
                  {isOwner && (
                    <Space size={4}>
                      <Button
                        size="small"
                        onClick={() => setInboxDrawer({ open: true, inbox: imapIntegration(inbox) })}
                      >
                        {t`Edit`}
                      </Button>
                      <Button size="small" danger onClick={() => confirmDeleteInbox(inbox)}>
                        {t`Delete`}
                      </Button>
                    </Space>
                  )}
                </div>
              ))
            )}
          </Card>
        </>
      )}

      <VeridianProfileWizard
        open={wizardOpen}
        workspaceId={workspaceId}
        ownerEmail={user?.email ?? ''}
        onClose={() => setWizardOpen(false)}
        onChanged={() => void reload()}
      />
      {workspace && (
        <VeridianProfileEditDrawer
          open={!!editing}
          workspace={workspace}
          profile={editing}
          onClose={() => setEditing(null)}
          onSaved={() => void reload()}
        />
      )}
      <VeridianInboxDrawer
        open={inboxDrawer.open}
        workspaceId={workspaceId}
        inbox={inboxDrawer.inbox}
        onClose={() => setInboxDrawer({ open: false, inbox: null })}
        onSaved={() => {
          setInboxDrawer({ open: false, inbox: null })
          void reload()
        }}
      />
    </div>
  )
}

