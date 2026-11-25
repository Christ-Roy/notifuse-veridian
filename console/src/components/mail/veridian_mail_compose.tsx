import React, { useMemo, useState } from 'react'
import { Alert, Button, Drawer, Form, Input, Modal, Select, Space } from 'antd'
import { SendOutlined } from '@ant-design/icons'
import { useLingui } from '@lingui/react/macro'

import { mailErrorKind, mailService, refusalOf, type Mailbox, type MailRefusal, type MailSendResult } from '../../services/api/veridian_mail'
import { exactDate, useMailLabels } from './veridian_mail_labels'
import { isRecipient } from './veridian_mail_rules'

const MAX_TEXT = 200_000

// Écrire un nouveau message, depuis la boîte ou depuis une autre app (?ecrire=adresse, le CRM par exemple). La personne écrit et envoie : le lien
// ne fait que proposer un destinataire. Mêmes plafonds que les séquences, confirmation explicite, refus de politique (409) dit avec sa raison.

interface ComposeProps {
  workspaceId: string
  /** Destinataire proposé. `undefined` : fermé. */
  to: string | undefined
  mailboxes: readonly Mailbox[]
  onClose: () => void
  onSent?: (result: MailSendResult) => void
}

const ComposeInner: React.FC<ComposeProps & { to: string }> = ({ workspaceId, to: initial, mailboxes, onClose, onSent }) => {
  const { t } = useLingui()
  const { errorText } = useMailLabels()
  const profiles = useMemo(
    () =>
      mailboxes
        .filter((m) => m.profile_id)
        .map((m) => ({ value: m.profile_id as string, label: m.address ? `${m.profile_name || m.profile_id} (${m.address})` : m.profile_name || (m.profile_id as string) })),
    [mailboxes]
  )
  const [to, setTo] = useState(initial)
  const [chosen, setChosen] = useState('')
  const [subject, setSubject] = useState('')
  const [text, setText] = useState('')
  const [confirming, setConfirming] = useState(false)
  const [sending, setSending] = useState(false)
  const [refusal, setRefusal] = useState<MailRefusal | null>(null)
  const [failure, setFailure] = useState<string | null>(null)
  const [queued, setQueued] = useState<MailSendResult | null>(null)

  const profile = chosen || (profiles.length === 1 ? profiles[0].value : '')
  const profileLabel = profiles.find((p) => p.value === profile)?.label ?? profile
  const canSend = isRecipient(to) && profile !== '' && subject.trim() !== '' && text.trim() !== ''

  const send = async () => {
    setSending(true)
    setRefusal(null)
    setFailure(null)
    try {
      const result = await mailService.send({ workspace_id: workspaceId, profile_id: profile, to: to.trim(), subject: subject.trim(), text })
      setQueued(result)
      onSent?.(result)
    } catch (err) {
      const refused = refusalOf(err)
      if (refused) setRefusal(refused)
      else setFailure(errorText(mailErrorKind(err)).title)
    } finally {
      setConfirming(false)
      setSending(false)
    }
  }

  if (queued) {
    return (
      <Space direction="vertical" style={{ width: '100%' }}>
        <Alert
          type="success"
          showIcon
          data-testid="compose-queued"
          message={t`Queued: your message will leave as soon as the sending limits allow it`}
          description={queued.note || t`It will appear in the conversation once the relay has accepted it.`}
        />
        <Button onClick={onClose}>{t`Close`}</Button>
      </Space>
    )
  }
  return (
    <>
      {refusal && (
        <Alert
          type="error"
          showIcon
          role="alert"
          data-testid="compose-refused"
          style={{ marginBottom: 12 }}
          message={t`Sending refused: ${refusal.reason}`}
          description={
            <>
              {refusal.detail && <div>{refusal.detail}</div>}
              {refusal.retry_at && <div>{t`Retry possible from ${exactDate(refusal.retry_at)}`}</div>}
            </>
          }
        />
      )}
      {failure && <Alert type="error" showIcon role="alert" style={{ marginBottom: 12 }} message={failure} />}
      <Form layout="vertical" onFinish={() => setConfirming(true)}>
        <Form.Item label={t`To`} validateStatus={to !== '' && !isRecipient(to) ? 'error' : undefined} style={{ marginBottom: 12 }}>
          <Input type="email" value={to} onChange={(e) => setTo(e.target.value)} maxLength={320} aria-label={t`To`} data-testid="compose-to" />
        </Form.Item>
        <Form.Item label={t`Sending profile`} style={{ marginBottom: 12 }}>
          <Select value={profile || undefined} options={profiles} onChange={setChosen} placeholder={t`Choose a profile`} aria-label={t`Sending profile`} />
        </Form.Item>
        <Form.Item label={t`Subject`} style={{ marginBottom: 12 }}>
          <Input value={subject} onChange={(e) => setSubject(e.target.value)} maxLength={300} aria-label={t`Subject`} data-testid="compose-subject" />
        </Form.Item>
        <Form.Item label={t`Your message`} style={{ marginBottom: 12 }}>
          <Input.TextArea
            value={text}
            onChange={(e) => setText(e.target.value)}
            autoSize={{ minRows: 8, maxRows: 20 }}
            maxLength={MAX_TEXT}
            aria-label={t`Your message`}
            data-testid="compose-text"
          />
        </Form.Item>
        <Space wrap>
          <Button type="primary" htmlType="submit" icon={<SendOutlined />} disabled={!canSend}>
            {t`Review and send`}
          </Button>
          <Button onClick={onClose}>{t`Cancel`}</Button>
        </Space>
      </Form>
      <Modal
        open={confirming}
        title={t`Send this message?`}
        okText={t`Send`}
        cancelText={t`Cancel`}
        confirmLoading={sending}
        onOk={() => void send()}
        onCancel={() => !sending && setConfirming(false)}
        maskClosable={false}
      >
        <p style={{ fontWeight: 600 }}>{t`This mail will leave with the same limits as the sequences.`}</p>
        <dl style={{ display: 'grid', gridTemplateColumns: 'max-content 1fr', gap: '4px 12px', margin: 0, overflowWrap: 'anywhere' }}>
          <dt>{t`To`}</dt>
          <dd style={{ margin: 0 }}>{to.trim()}</dd>
          <dt>{t`Profile`}</dt>
          <dd style={{ margin: 0 }}>{profileLabel}</dd>
          <dt>{t`Subject`}</dt>
          <dd style={{ margin: 0 }}>{subject.trim()}</dd>
        </dl>
      </Modal>
    </>
  )
}

export const VeridianMailCompose: React.FC<ComposeProps> = (props) => {
  const { t } = useLingui()
  return (
    <Drawer open={props.to !== undefined} onClose={props.onClose} title={t`New message`} width="min(640px, 100vw)" destroyOnHidden styles={{ body: { padding: 16 } }}>
      {props.to !== undefined && <ComposeInner key={props.to} {...props} to={props.to} />}
    </Drawer>
  )
}
