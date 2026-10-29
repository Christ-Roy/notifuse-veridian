import React, { useState } from 'react'
import { Alert, Button, Collapse, Empty, Skeleton, theme } from 'antd'
import { useQuery } from '@tanstack/react-query'
import { Link, useNavigate } from '@tanstack/react-router'
import { useLingui } from '@lingui/react/macro'

import { mailErrorKind, mailService, type MailThread } from '../../services/api/veridian_mail'
import { MailKindBadge } from './veridian_mail_kind'
import { exactDate, relativeDate, useMailLabels } from './veridian_mail_labels'
import { mailKeys } from './veridian_mail_queries'
import { VeridianMailTimeline } from './veridian_mail_timeline'

const ThreadPanel: React.FC<{ workspaceId: string; threadId: string }> = ({ workspaceId, threadId }) => {
  const { t } = useLingui()
  const navigate = useNavigate()
  const q = useQuery({ queryKey: mailKeys.thread(workspaceId, threadId), queryFn: () => mailService.thread(workspaceId, threadId), retry: false })
  if (q.isLoading) return <Skeleton active paragraph={{ rows: 3 }} />
  if (q.isError || !q.data) {
    return (
      <Alert
        type="warning"
        showIcon
        message={t`Could not load this conversation`}
        action={
          <Button size="small" onClick={() => void q.refetch()}>
            {t`Retry`}
          </Button>
        }
      />
    )
  }
  return (
    <VeridianMailTimeline
      workspaceId={workspaceId}
      thread={q.data}
      onOpen={(id) =>
        void navigate({ to: '/console/workspace/$workspaceId/inbox', params: { workspaceId }, search: { message: id, nature: 'toutes' } as never })
      }
    />
  )
}

// Fiche contact, section « Courriels » : ses fils (envois, relances, réponse, rebond), chacun se
// déplie en chronologie, avec le lien « ouvrir dans la boîte ».
export const VeridianContactMail: React.FC<{ workspaceId: string; email: string }> = ({ workspaceId, email }) => {
  const { t } = useLingui()
  const { token } = theme.useToken()
  const { errorText } = useMailLabels()
  const [openKeys, setOpenKeys] = useState<string[]>([])
  const q = useQuery({
    queryKey: [...mailKeys.contactThreads(workspaceId, email), 'summaries'],
    queryFn: () => mailService.threadSummaries(workspaceId, email, 20),
    enabled: !!email,
    retry: false
  })

  if (q.isLoading) return <Skeleton active paragraph={{ rows: 4 }} />
  if (q.isError) {
    const text = errorText(mailErrorKind(q.error))
    return (
      <Alert
        type="warning"
        showIcon
        message={text.title}
        description={text.hint}
        action={
          <Button size="small" onClick={() => void q.refetch()}>
            {t`Retry`}
          </Button>
        }
      />
    )
  }
  const threads: MailThread[] = q.data ?? []
  if (threads.length === 0) {
    return <Empty image={Empty.PRESENTED_IMAGE_SIMPLE} description={t`No mail exchanged with this contact yet.`} />
  }
  return (
    <div data-testid="contact-mail">
      <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center', marginBottom: 12, flexWrap: 'wrap', gap: 8 }}>
        <span style={{ color: token.colorTextSecondary }}>
          {threads.length === 1 ? t`1 conversation` : t`${threads.length} conversations`}
        </span>
        <Link
          to="/console/workspace/$workspaceId/inbox"
          params={{ workspaceId }}
          search={{ nature: 'toutes', q: email } as never}
        >
          {t`Open in the inbox`}
        </Link>
      </div>
      <Collapse
        activeKey={openKeys}
        onChange={(keys) => setOpenKeys(Array.isArray(keys) ? keys : [keys])}
        items={threads.map((th) => ({
          key: th.thread_id,
          label: (
            <span style={{ display: 'flex', flexWrap: 'wrap', alignItems: 'center', gap: '4px 10px' }}>
              {th.last_kind && <MailKindBadge kind={th.last_kind} compact />}
              <strong style={{ overflowWrap: 'anywhere' }}>{th.subject || t`(no subject)`}</strong>
              <span style={{ color: token.colorTextSecondary, fontSize: 12.5 }} title={exactDate(th.last_at)}>
                {th.message_count === 1 ? t`1 message` : t`${th.message_count} messages`} · {relativeDate(th.last_at)}
              </span>
            </span>
          ),
          children: openKeys.includes(th.thread_id) ? <ThreadPanel workspaceId={workspaceId} threadId={th.thread_id} /> : null
        }))}
      />
    </div>
  )
}
