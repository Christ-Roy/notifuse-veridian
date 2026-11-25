import { useState, type ReactNode } from 'react'
import { useQuery } from '@tanstack/react-query'
import { Alert, App, Button, Collapse, Empty, Popconfirm, Result, Skeleton, Tag, theme, Tooltip, Typography } from 'antd'
import { PlusOutlined, ReloadOutlined } from '@ant-design/icons'
import { useLingui } from '@lingui/react/macro'

import dayjs from '../../lib/dayjs'
import type { Workspace } from '../../services/api/workspace'
import {
  accessState,
  mailboxAccessService,
  mailboxErrorKind,
  type MailboxAccess,
  type MailboxAccessState
} from '../../services/api/veridian_mailbox_access'
import { VeridianMailboxAttempts } from './veridian_mailbox_attempts'
import { VeridianMailboxCreate } from './veridian_mailbox_create'
import { VeridianMailboxExport } from './veridian_mailbox_export'
import { useMailboxLabels } from './veridian_mailbox_labels'
import { expectsAttempts, splitAccesses } from './veridian_mailbox_rules'
import { VeridianMailboxSetup } from './veridian_mailbox_setup'

const { Paragraph, Text } = Typography

const mailboxAccessKey = (workspaceId: string) => ['veridian-mailbox-access', workspaceId] as const

const STATE_COLOR: Record<MailboxAccessState, string> = {
  active: 'success',
  closed: 'warning',
  disabled: 'default',
  revoked: 'error'
}

interface Props {
  workspaceId: string
  workspace: Workspace | null
  isOwner: boolean
}

// Réglages > « Mailbox access » : créer l'accès d'une boîte (IMAP et SMTP), régler son application sans être
// technicien, vérifier ce qu'on a saisi, voir les essais reçus, exporter son courrier. Les verdicts viennent du
// serveur (fiches 84 et 91) ; la console les traduit en phrases d'action. Cartes plutôt qu'un tableau large :
// rien ne déborde sur un téléphone.
export function VeridianMailboxAccess({ workspaceId, workspace, isOwner }: Props) {
  const { t } = useLingui()
  const labels = useMailboxLabels()
  const { message } = App.useApp()

  const query = useQuery({
    queryKey: mailboxAccessKey(workspaceId),
    queryFn: () => mailboxAccessService.list(workspaceId),
    retry: false
  })
  const data = query.data

  const [createOpen, setCreateOpen] = useState(false)
  const [setupId, setSetupId] = useState<string | null>(null)
  // Le mot de passe n'existe qu'ici, en mémoire, le temps de régler l'application.
  const [setupPassword, setSetupPassword] = useState<string | undefined>(undefined)
  const [setupFallback, setSetupFallback] = useState<MailboxAccess | null>(null)
  const [pending, setPending] = useState('')

  const accesses = data?.accesses ?? []
  const { live, revoked } = splitAccesses(accesses)
  const revokedCount = revoked.length
  const setupAccess = accesses.find((a) => a.id === setupId) ?? (setupFallback?.id === setupId ? setupFallback : null)

  const closeSetup = () => {
    setSetupId(null)
    setSetupPassword(undefined)
    setSetupFallback(null)
  }

  const act = async (id: string, run: () => Promise<unknown>, done: string) => {
    setPending(id)
    try {
      await run()
      message.success(done)
      await query.refetch()
    } catch (err) {
      message.error(labels.errorOf(err))
    } finally {
      setPending('')
    }
  }

  const forbidden = query.isError && mailboxErrorKind(query.error) === 'forbidden'

  return (
    <div>
      <h2 style={{ marginBottom: 8 }}>{t`Mailbox access`}</h2>
      <Paragraph type="secondary">
        {t`Connect your phone, Thunderbird or a tool to your mailboxes. Each access has its own password and you can switch it off at any time.`}
      </Paragraph>

      {forbidden ? (
        <Result
          status="403"
          title={t`You cannot manage mailbox access`}
          subTitle={labels.errorText('forbidden')}
        />
      ) : (
        <>
          {accesses.length > 0 && (
            <Button type="primary" icon={<PlusOutlined />} onClick={() => setCreateOpen(true)}>
              {t`Create an access`}
            </Button>
          )}

          <div style={{ marginTop: 16 }}>
            {query.isLoading ? (
              <div role="status" aria-label={t`Loading`}>
                <Skeleton active paragraph={{ rows: 3 }} />
              </div>
            ) : query.isError ? (
              <Alert
                type="error"
                showIcon
                role="alert"
                message={labels.errorOf(query.error)}
                action={
                  <Button size="small" icon={<ReloadOutlined />} onClick={() => query.refetch()}>
                    {t`Try again`}
                  </Button>
                }
              />
            ) : accesses.length === 0 ? (
              <Empty description={t`No access yet. Create one to read and send your mail from your phone or another app.`}>
                <Button type="primary" icon={<PlusOutlined />} onClick={() => setCreateOpen(true)}>
                  {t`Create an access`}
                </Button>
              </Empty>
            ) : (
              <>
                <ul style={{ listStyle: 'none', padding: 0, margin: 0, display: 'grid', gap: 12 }}>
                  {live.map((a) => (
                    <li key={a.id}>
                      <AccessCard
                        access={a}
                        busy={pending === a.id}
                        onSetup={() => setSetupId(a.id)}
                        onToggle={() =>
                          act(
                            a.id,
                            () => mailboxAccessService.setDisabled(workspaceId, a.id, !a.disabled_at),
                            a.disabled_at ? t`Access re-enabled` : t`Access disabled`
                          )
                        }
                        onRevoke={() =>
                          act(a.id, () => mailboxAccessService.revoke(workspaceId, a.id), t`Access revoked`)
                        }
                      />
                    </li>
                  ))}
                </ul>
                {revoked.length > 0 && (
                  <Collapse
                    ghost
                    style={{ marginTop: 8 }}
                    items={[
                      {
                        key: 'revoked',
                        label: t`Revoked accesses (${revokedCount})`,
                        children: (
                          <ul style={{ listStyle: 'none', padding: 0, margin: 0, display: 'grid', gap: 12 }}>
                            {revoked.map((a) => (
                              <li key={a.id}>
                                <AccessCard access={a} busy={false} />
                              </li>
                            ))}
                          </ul>
                        )
                      }
                    ]}
                  />
                )}
              </>
            )}
          </div>

          {data && (
            <VeridianMailboxAttempts
              attempts={data.attempts ?? []}
              since={data.attempts_since}
              connection={data.connection ?? {}}
              expectAttempts={expectsAttempts(accesses, Date.now())}
              refreshing={query.isFetching}
              onRefresh={() => query.refetch()}
            />
          )}
          <VeridianMailboxExport workspaceId={workspaceId} integrations={workspace?.integrations} />
        </>
      )}

      <VeridianMailboxCreate
        open={createOpen}
        onClose={() => setCreateOpen(false)}
        workspaceId={workspaceId}
        integrations={workspace?.integrations}
        isOwner={isOwner}
        onCreated={() => void query.refetch()}
        onSetup={(result) => {
          setSetupFallback(result.access)
          setSetupPassword(result.password)
          setSetupId(result.access.id)
          setCreateOpen(false)
        }}
      />
      <VeridianMailboxSetup
        open={setupAccess !== null}
        onClose={closeSetup}
        workspaceId={workspaceId}
        access={setupAccess}
        connection={data?.connection ?? {}}
        password={setupPassword}
        onTested={() => void query.refetch()}
      />
    </div>
  )
}

interface CardProps {
  access: MailboxAccess
  busy: boolean
  onSetup?: () => void
  onToggle?: () => void
  onRevoke?: () => void
}

function AccessCard({ access, busy, onSetup, onToggle, onRevoke }: CardProps) {
  const { t } = useLingui()
  const labels = useMailboxLabels()
  const { token } = theme.useToken()
  const state = accessState(access)
  const name = access.label || access.address
  const stateText = labels.state(state)
  const help = labels.stateHelp(state, access)

  return (
    <div
      data-state={state}
      style={{
        border: `1px solid ${token.colorBorderSecondary}`,
        borderRadius: 8,
        padding: 16,
        background: token.colorBgContainer,
        opacity: state === 'revoked' ? 0.75 : 1
      }}
    >
      <div style={{ display: 'flex', flexWrap: 'wrap', gap: 8, alignItems: 'flex-start', justifyContent: 'space-between' }}>
        <div style={{ minWidth: 0, flex: '1 1 200px' }}>
          <Text strong style={{ display: 'block', overflowWrap: 'anywhere' }}>
            {access.address}
          </Text>
          {access.label && <Text type="secondary">{access.label}</Text>}
        </div>
        <Tooltip title={help}>
          <Tag
            color={STATE_COLOR[state]}
            tabIndex={0}
            aria-label={`${stateText}. ${help}`}
            style={{ margin: 0 }}
          >
            {stateText}
          </Tag>
        </Tooltip>
      </div>

      <dl style={{ margin: '12px 0 0', display: 'grid', gap: 6 }}>
        <Fact term={t`Belongs to`} value={labels.holder(access)} />
        <Fact term={t`Rights`} value={labels.rights(access)} />
        <Fact
          term={t`Last used`}
          value={
            access.last_used_at ? (
              <Tooltip title={dayjs(access.last_used_at).format('lll')}>
                <span>{dayjs(access.last_used_at).fromNow()}</span>
              </Tooltip>
            ) : (
              t`Never`
            )
          }
        />
      </dl>

      {state === 'closed' && (
        <Paragraph type="secondary" style={{ margin: '12px 0 0' }}>
          {help}
        </Paragraph>
      )}

      {state !== 'revoked' && (
        <div style={{ display: 'flex', flexWrap: 'wrap', gap: 8, marginTop: 12 }}>
          {state === 'active' && (
            <Button type="primary" disabled={busy} onClick={onSetup} aria-label={t`Set up my app for ${name}`}>
              {t`Set up my app`}
            </Button>
          )}
          {(state === 'active' || state === 'disabled') && (
            <Button
              loading={busy}
              onClick={onToggle}
              aria-label={state === 'active' ? t`Disable access ${name}` : t`Re-enable access ${name}`}
            >
              {state === 'active' ? t`Disable` : t`Re-enable`}
            </Button>
          )}
          <Popconfirm
            title={t`Revoke this access?`}
            description={t`It stops working at once and cannot be reopened. Apps that use it will lose their connection.`}
            okText={t`Revoke for good`}
            cancelText={t`Cancel`}
            okButtonProps={{ danger: true }}
            onConfirm={onRevoke}
          >
            <Button danger disabled={busy} aria-label={t`Revoke access ${name}`}>
              {t`Revoke`}
            </Button>
          </Popconfirm>
        </div>
      )}
    </div>
  )
}

function Fact({ term, value }: { term: string; value: ReactNode }) {
  const { token } = theme.useToken()
  return (
    <div style={{ display: 'flex', flexWrap: 'wrap', gap: '0 8px' }}>
      <dt style={{ color: token.colorTextSecondary, flex: '0 0 110px' }}>{term}</dt>
      <dd style={{ margin: 0, flex: '1 1 160px', minWidth: 0, overflowWrap: 'anywhere' }}>{value}</dd>
    </div>
  )
}
