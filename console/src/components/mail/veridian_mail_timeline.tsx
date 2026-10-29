import React, { useMemo, useState } from 'react'
import { Button, theme } from 'antd'
import { DownOutlined, PaperClipOutlined, RightOutlined } from '@ant-design/icons'
import { useLingui } from '@lingui/react/macro'

import type { MailMessage, MailThread } from '../../services/api/veridian_mail'
import { VeridianMailBody } from './veridian_mail_body'
import { MailKindBadge, MailKindDot } from './veridian_mail_kind'
import { useKindStyle } from './veridian_mail_theme'
import { exactDate, MAIL_TIMEZONE, useMailLabels } from './veridian_mail_labels'
import { excerptOf, gapBetween, messageDate } from './veridian_mail_rules'

interface StepProps {
  workspaceId: string
  message: MailMessage
  label: string
  gapText: string
  current: boolean
  expanded: boolean
  last: boolean
  timezone: string
  onToggle: () => void
  onOpen?: (id: string) => void
}

const Step: React.FC<StepProps> = ({ workspaceId, message, label, gapText, current, expanded, last, timezone, onToggle, onOpen }) => {
  const { t } = useLingui()
  const { token } = theme.useToken()
  const style = useKindStyle(message.kind)
  const { kindLabel } = useMailLabels()
  const bodyId = `step-body-${message.id}`
  const excerpt = excerptOf(message)
  const attachCount = (message.attachments ?? []).filter((a) => !(a.inline && a.content_id)).length
  const reason = message.dsn?.reason_fr

  return (
    <li
      data-testid="timeline-step"
      data-kind={message.kind}
      data-current={current ? 'true' : 'false'}
      aria-current={current ? 'step' : undefined}
      style={{ display: 'flex', gap: 14, position: 'relative', listStyle: 'none' }}
    >
      <div style={{ display: 'flex', flexDirection: 'column', alignItems: 'center', flex: '0 0 32px' }}>
        <MailKindDot kind={message.kind} />
        {!last && <span aria-hidden="true" style={{ flex: 1, width: 2, background: token.colorBorder, marginTop: 4, marginBottom: 4, minHeight: 16 }} />}
      </div>
      <div
        style={{
          flex: 1,
          minWidth: 0,
          marginBottom: last ? 0 : 14,
          padding: '10px 14px',
          borderRadius: 8,
          border: `${current ? 2 : 1}px solid ${current ? style.fg : token.colorBorderSecondary}`,
          background: current ? style.bg : token.colorBgContainer
        }}
      >
        <button
          type="button"
          onClick={onToggle}
          aria-expanded={expanded}
          aria-controls={bodyId}
          style={{
            boxSizing: 'border-box',
            display: 'block',
            width: '100%',
            cursor: 'pointer',
            borderRadius: 6,
            border: 0,
            padding: 0,
            background: 'transparent',
            textAlign: 'left',
            font: 'inherit',
            color: 'inherit'
          }}
          className="focus-visible:outline-2 focus-visible:outline-offset-4 focus-visible:outline-[#7763f1]"
        >
          <span style={{ display: 'flex', flexWrap: 'wrap', alignItems: 'baseline', gap: '2px 10px' }}>
            <span style={{ fontWeight: 700, color: style.fg, fontSize: 15 }}>{label}</span>
            {current && <span style={{ fontSize: 11, fontWeight: 700, color: token.colorText, textTransform: 'uppercase', letterSpacing: 0.4 }}>{t`Shown`}</span>}
            <span style={{ fontSize: 13, color: token.colorTextSecondary }}>
              {exactDate(messageDate(message), timezone)}
              {gapText ? ` · ${gapText}` : ''}
            </span>
            <span style={{ marginLeft: 'auto', color: token.colorTextSecondary }} aria-hidden="true">
              {expanded ? <DownOutlined /> : <RightOutlined />}
            </span>
          </span>
          <span style={{ display: 'block', marginTop: 2, fontWeight: 500, overflowWrap: 'anywhere' }}>{message.subject || t`(no subject)`}</span>
          {reason ? (
            <span data-testid="step-reason" style={{ display: 'block', marginTop: 4, color: style.fg, fontWeight: 600 }}>
              {reason}
            </span>
          ) : (
            excerpt && (
              <span style={{ display: 'block', marginTop: 2, color: token.colorTextSecondary, fontSize: 13.5, overflowWrap: 'anywhere' }}>{excerpt}</span>
            )
          )}
          {attachCount > 0 && (
            <span style={{ display: 'inline-flex', alignItems: 'center', gap: 4, marginTop: 4, fontSize: 12, color: token.colorTextSecondary }}>
              <PaperClipOutlined aria-hidden="true" />
              {attachCount === 1 ? t`1 attachment` : t`${attachCount} attachments`}
            </span>
          )}
        </button>
        {expanded && (
          <div id={bodyId} data-testid="step-body" style={{ marginTop: 12, borderTop: `1px solid ${token.colorBorderSecondary}`, paddingTop: 12 }}>
            <div style={{ display: 'flex', flexWrap: 'wrap', gap: 8, alignItems: 'center', marginBottom: 10 }}>
              <MailKindBadge kind={message.kind} compact />
              <span style={{ fontSize: 12.5, color: token.colorTextSecondary }}>
                {message.direction === 'out' ? t`From ${message.from}` : t`${kindLabel(message.kind)} from ${message.from}`}
              </span>
              {!current && onOpen && (
                <Button size="small" onClick={() => onOpen(message.id)} style={{ marginLeft: 'auto' }}>
                  {t`Open this message`}
                </Button>
              )}
            </div>
            <VeridianMailBody workspaceId={workspaceId} message={message} compact />
          </div>
        )}
      </div>
    </li>
  )
}

// Fil de conversation en chronologie : une puce par message (envoi bleu, réponse verte, réponse
// automatique grise, rebond rouge ou orangé, rapport neutre), jalon en français fourni par le
// serveur, date exacte et écart avec l'étape précédente, extrait, et le mail rendu en dépliant.
export const VeridianMailTimeline: React.FC<{
  workspaceId: string
  thread: MailThread
  currentId?: string
  timezone?: string
  /** Déplie d'emblée le message courant (panneau « Conversation »). */
  expandCurrent?: boolean
  onOpen?: (id: string) => void
}> = ({ workspaceId, thread, currentId, timezone = MAIL_TIMEZONE, expandCurrent = false, onOpen }) => {
  const { t } = useLingui()
  const { gapLabel, kindLabel } = useMailLabels()
  const messages = useMemo(() => thread.messages ?? [], [thread.messages])
  const labels = useMemo(() => new Map((thread.milestones ?? []).map((m) => [m.id, m.label])), [thread.milestones])
  const [open, setOpen] = useState<Set<string>>(() => new Set(expandCurrent && currentId ? [currentId] : []))

  const toggle = (id: string) =>
    setOpen((prev) => {
      const next = new Set(prev)
      if (next.has(id)) next.delete(id)
      else next.add(id)
      return next
    })

  if (messages.length === 0) return null
  return (
    <ol aria-label={t`Conversation timeline`} data-testid="timeline" style={{ margin: 0, padding: 0 }}>
      {messages.map((m, i) => (
        <Step
          key={m.id}
          workspaceId={workspaceId}
          message={m}
          label={labels.get(m.id) || kindLabel(m.kind)}
          gapText={i > 0 ? gapLabel(gapBetween(messageDate(messages[i - 1]), messageDate(m))) : ''}
          current={m.id === currentId}
          expanded={open.has(m.id)}
          last={i === messages.length - 1}
          timezone={timezone}
          onToggle={() => toggle(m.id)}
          onOpen={onOpen}
        />
      ))}
    </ol>
  )
}
