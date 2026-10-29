import React, { useState } from 'react'
import { Alert, App, Button, Space, theme, Tooltip } from 'antd'
import { ArrowLeftOutlined, CheckOutlined, DownloadOutlined, UndoOutlined } from '@ant-design/icons'
import { useLingui } from '@lingui/react/macro'

import { mailErrorKind, mailService, type Mailbox, type MailMessage, type MailSendResult } from '../../services/api/veridian_mail'
import { VeridianMailAttachments } from './veridian_mail_attachments'
import { VeridianMailBody } from './veridian_mail_body'
import { saveBlob } from './veridian_mail_download'
import { VeridianMailDsnCard } from './veridian_mail_dsn_card'
import { VeridianMailHeader } from './veridian_mail_header'
import { MailKindBadge } from './veridian_mail_kind'
import { exactDate, MAIL_TIMEZONE, useMailLabels } from './veridian_mail_labels'
import { VeridianMailLabelsEditor } from './veridian_mail_labels_editor'
import { VeridianMailReply } from './veridian_mail_reply'
import { handledByKind, isReplyable, listedAttachments } from './veridian_mail_rules'

interface Props {
  workspaceId: string
  message: MailMessage
  mailboxes: readonly Mailbox[]
  timezone?: string
  /** Appelé avec le message mis à jour (traité, libellés) : la page met ses caches à jour. */
  onChange: (m: MailMessage) => void
  onReplySent?: (r: MailSendResult) => void
  /** Retour à la liste (mobile). */
  onBack?: () => void
  /** Contenu sous la fiche (chronologie du fil). */
  footer?: React.ReactNode
}

const Block: React.FC<{ children: React.ReactNode; label?: string }> = ({ children, label }) => {
  const { token } = theme.useToken()
  return (
    <section
      aria-label={label}
      style={{ border: `1px solid ${token.colorBorderSecondary}`, borderRadius: 8, padding: '14px 18px', background: token.colorBgContainer }}
    >
      {children}
    </section>
  )
}

const MessageViewInner: React.FC<Props> = ({ workspaceId, message, mailboxes, timezone = MAIL_TIMEZONE, onChange, onReplySent, onBack, footer }) => {
  const { t } = useLingui()
  const { message: toast } = App.useApp()
  const { token } = theme.useToken()
  const { errorText } = useMailLabels()
  const [handlingBusy, setHandlingBusy] = useState(false)
  const [rawBusy, setRawBusy] = useState(false)

  const attachments = listedAttachments(message)
  const handled = !!message.handled_at
  const toggleHandled = async () => {
    setHandlingBusy(true)
    try {
      onChange(await mailService.update(workspaceId, message.id, { handled: !handled }))
    } catch (err) {
      toast.error(errorText(mailErrorKind(err)).title)
    } finally {
      setHandlingBusy(false)
    }
  }

  const downloadRaw = async () => {
    setRawBusy(true)
    try {
      const file = await mailService.raw(workspaceId, message.id)
      saveBlob(file.blob, file.filename)
      if (file.reconstructed || message.direction === 'out') {
        toast.info(t`This .eml was reconstructed from the stored rendering: it is not the message byte for byte.`)
      }
    } catch (err) {
      toast.error(errorText(mailErrorKind(err)).title)
    } finally {
      setRawBusy(false)
    }
  }

  const handledBy = handledByKind(message.handled_by)

  return (
    <article aria-label={message.subject || t`(no subject)`} data-testid="message-view" style={{ display: 'flex', flexDirection: 'column', gap: 16 }}>
      {onBack && (
        <div>
          <Button type="text" icon={<ArrowLeftOutlined />} onClick={onBack} style={{ marginLeft: -8 }}>
            {t`Back to the list`}
          </Button>
        </div>
      )}

      <div>
        <div style={{ display: 'flex', flexWrap: 'wrap', alignItems: 'center', gap: 8, marginBottom: 8 }}>
          <MailKindBadge kind={message.kind} />
          {message.body_source === 'captured' && (
            <Tooltip title={t`The exact rendering captured when the mail left.`}>
              <span style={{ fontSize: 12, color: token.colorTextSecondary }}>{t`As sent`}</span>
            </Tooltip>
          )}
          {message.body_source === 'rerendered' && (
            <Tooltip title={t`Rebuilt from the template of the time: it may differ from what actually left.`}>
              <span data-testid="rerendered-note" style={{ fontSize: 12, color: token.colorTextSecondary }}>{t`Reconstructed`}</span>
            </Tooltip>
          )}
        </div>
        <h2 style={{ margin: 0, fontSize: 22, lineHeight: 1.3, fontWeight: 650, overflowWrap: 'anywhere' }}>{message.subject || t`(no subject)`}</h2>
      </div>

      <Space wrap size={[8, 8]}>
        <Button
          type={handled ? 'default' : 'primary'}
          icon={handled ? <UndoOutlined /> : <CheckOutlined />}
          loading={handlingBusy}
          onClick={() => void toggleHandled()}
        >
          {handled ? t`Mark as to handle` : t`Mark as handled`}
        </Button>
        <Button icon={<DownloadOutlined />} loading={rawBusy} onClick={() => void downloadRaw()}>
          {message.direction === 'out' ? t`Download the .eml (reconstructed)` : t`Download the raw message (.eml)`}
        </Button>
        {handled && (
          <span data-testid="handled-note" style={{ fontSize: 13, color: token.colorTextSecondary }}>
            {handledBy === 'agent' ? t`Handled by an agent` : handledBy === 'human' ? t`Handled by a person` : t`Marked as handled`}
            {message.handled_at ? ` · ${exactDate(message.handled_at, timezone)}` : ''}
          </span>
        )}
      </Space>

      <Block label={t`Message header`}>
        <VeridianMailHeader workspaceId={workspaceId} message={message} mailboxes={mailboxes} timezone={timezone} />
      </Block>

      {message.dsn && <VeridianMailDsnCard dsn={message.dsn} />}

      {message.truncated && (
        <Alert type="warning" showIcon message={t`The raw message was read truncated (size limit): the end of the mail may be missing.`} />
      )}

      <Block label={t`Message content`}>
        <VeridianMailBody workspaceId={workspaceId} message={message} />
      </Block>

      {attachments.length > 0 && (
        <Block>
          <VeridianMailAttachments workspaceId={workspaceId} messageId={message.id} attachments={attachments} />
        </Block>
      )}

      <Block label={t`Labels`}>
        <div style={{ fontWeight: 600, marginBottom: 8 }}>{t`Labels`}</div>
        <VeridianMailLabelsEditor workspaceId={workspaceId} message={message} onChange={onChange} />
      </Block>

      {isReplyable(message) && <VeridianMailReply workspaceId={workspaceId} message={message} mailboxes={mailboxes} onSent={onReplySent} />}

      {footer}
    </article>
  )
}

/** Un autre message repart d'un état vierge (images distantes bloquées, format, réponse). */
export const VeridianMailMessageView: React.FC<Props> = (props) => <MessageViewInner key={props.message.id} {...props} />
