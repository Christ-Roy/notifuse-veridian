import React, { useMemo, useState } from 'react'
import { Alert, Button, Form, Input, Modal, Select, Space, theme } from 'antd'
import { SendOutlined } from '@ant-design/icons'
import { useLingui } from '@lingui/react/macro'

import {
  mailErrorKind,
  mailService,
  refusalOf,
  type Mailbox,
  type MailMessage,
  type MailRefusal,
  type MailSendResult
} from '../../services/api/veridian_mail'
import { exactDate, useMailLabels } from './veridian_mail_labels'
import { defaultReplyProfile, replySubject, replyTarget } from './veridian_mail_rules'

const MAX_TEXT = 200_000

// Répondre depuis la boîte : le mail part par le profil qui a reçu (modifiable), dans le fil
// (In-Reply-To posé par le serveur), avec les MÊMES plafonds que les séquences. Rien ne part sans
// confirmation explicite ; un refus de politique (409) s'affiche avec sa raison.

interface ReplyProps {
  workspaceId: string
  message: MailMessage
  mailboxes: readonly Mailbox[]
  onSent?: (result: MailSendResult) => void
}

const ReplyInner: React.FC<ReplyProps> = ({ workspaceId, message, mailboxes, onSent }) => {
  const { t } = useLingui()
  const { token } = theme.useToken()
  const { errorText } = useMailLabels()
  const [open, setOpen] = useState(false)
  const [confirming, setConfirming] = useState(false)
  const [sending, setSending] = useState(false)
  const [text, setText] = useState('')
  const [subject, setSubject] = useState(() => replySubject(message.subject))
  const [profile, setProfile] = useState(() => defaultReplyProfile(message, mailboxes))
  const [refusal, setRefusal] = useState<MailRefusal | null>(null)
  const [failure, setFailure] = useState<string | null>(null)
  const [queued, setQueued] = useState<MailSendResult | null>(null)

  const profiles = useMemo(
    () =>
      mailboxes
        .filter((m) => m.profile_id)
        .map((m) => ({ value: m.profile_id as string, label: m.address ? `${m.profile_name || m.profile_id} (${m.address})` : m.profile_name || (m.profile_id as string) })),
    [mailboxes]
  )
  const target = replyTarget(message)
  const canSend = text.trim().length > 0 && text.length <= MAX_TEXT && subject.trim().length > 0 && profile !== ''
  const profileLabel = profiles.find((p) => p.value === profile)?.label ?? profile

  const send = async () => {
    setSending(true)
    setRefusal(null)
    setFailure(null)
    try {
      const result = await mailService.reply({
        workspace_id: workspaceId,
        in_reply_to_message: message.id,
        profile_id: profile,
        subject: subject.trim(),
        text
      })
      setQueued(result)
      setConfirming(false)
      setOpen(false)
      setText('')
      onSent?.(result)
    } catch (err) {
      setConfirming(false)
      const refused = refusalOf(err)
      if (refused) setRefusal(refused)
      else setFailure(errorText(mailErrorKind(err)).title)
    } finally {
      setSending(false)
    }
  }

  return (
    <section aria-label={t`Reply`} data-testid="reply-section">
      {queued && (
        <Alert
          type="success"
          showIcon
          data-testid="reply-queued"
          style={{ marginBottom: 12 }}
          message={t`Queued: your reply will leave as soon as the sending limits allow it`}
          description={queued.note || t`It will appear in the conversation once the relay has accepted it.`}
          closable
          onClose={() => setQueued(null)}
        />
      )}
      {refusal && (
        <Alert
          type="error"
          showIcon
          data-testid="reply-refused"
          style={{ marginBottom: 12 }}
          message={t`Sending refused: ${refusal.reason}`}
          description={
            <>
              {refusal.detail && <div>{refusal.detail}</div>}
              {refusal.retry_at && <div>{t`Retry possible from ${exactDate(refusal.retry_at)}`}</div>}
            </>
          }
          closable
          onClose={() => setRefusal(null)}
        />
      )}
      {failure && <Alert type="error" showIcon style={{ marginBottom: 12 }} message={failure} closable onClose={() => setFailure(null)} />}

      {!open ? (
        <Button type="primary" icon={<SendOutlined />} onClick={() => setOpen(true)}>
          {t`Reply`}
        </Button>
      ) : (
        <Form layout="vertical" onFinish={() => setConfirming(true)} style={{ border: `1px solid ${token.colorBorderSecondary}`, borderRadius: 8, padding: 16, background: token.colorBgContainer }}>
          <div style={{ marginBottom: 12, fontSize: 13, color: token.colorTextSecondary }}>
            {t`To`} <strong data-testid="reply-target">{target}</strong>
          </div>
          <Form.Item label={t`Sending profile`} style={{ marginBottom: 12 }}>
            <Select
              value={profile || undefined}
              options={profiles}
              onChange={setProfile}
              placeholder={t`Choose a profile`}
              aria-label={t`Sending profile`}
              data-testid="reply-profile"
            />
          </Form.Item>
          <Form.Item label={t`Subject`} style={{ marginBottom: 12 }}>
            <Input value={subject} onChange={(e) => setSubject(e.target.value)} maxLength={300} aria-label={t`Subject`} data-testid="reply-subject" />
          </Form.Item>
          <Form.Item label={t`Your reply`} style={{ marginBottom: 12 }}>
            <Input.TextArea
              value={text}
              onChange={(e) => setText(e.target.value)}
              autoSize={{ minRows: 6, maxRows: 18 }}
              aria-label={t`Your reply`}
              data-testid="reply-text"
              maxLength={MAX_TEXT}
            />
          </Form.Item>
          <Space wrap>
            <Button type="primary" htmlType="submit" icon={<SendOutlined />} disabled={!canSend}>
              {t`Review and send`}
            </Button>
            <Button onClick={() => setOpen(false)}>{t`Cancel`}</Button>
          </Space>
        </Form>
      )}

      <Modal
        open={confirming}
        title={t`Send this reply?`}
        okText={t`Send`}
        cancelText={t`Cancel`}
        confirmLoading={sending}
        onOk={() => void send()}
        onCancel={() => !sending && setConfirming(false)}
        maskClosable={false}
      >
        <p data-testid="reply-confirm-text" style={{ fontWeight: 600 }}>
          {t`This mail will leave with the same limits as the sequences.`}
        </p>
        <dl style={{ display: 'grid', gridTemplateColumns: 'max-content 1fr', gap: '4px 12px', margin: 0 }}>
          <dt>{t`To`}</dt>
          <dd style={{ margin: 0 }}>{target}</dd>
          <dt>{t`Profile`}</dt>
          <dd style={{ margin: 0 }}>{profileLabel}</dd>
          <dt>{t`Subject`}</dt>
          <dd style={{ margin: 0 }}>{subject}</dd>
        </dl>
      </Modal>
    </section>
  )
}

/** Un autre message ouvre une réponse vierge (remontage par identifiant). */
export const VeridianMailReply: React.FC<ReplyProps> = (props) => <ReplyInner key={props.message.id} {...props} />
