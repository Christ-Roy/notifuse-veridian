import React, { useState } from 'react'
import { App, Input, theme } from 'antd'
import { CloseOutlined } from '@ant-design/icons'
import { useLingui } from '@lingui/react/macro'

import { mailErrorKind, mailService, type MailMessage } from '../../services/api/veridian_mail'
import { useMailLabels } from './veridian_mail_labels'
import { cleanLabels, MAX_LABEL_LENGTH, MAX_LABELS } from './veridian_mail_rules'

// Libellés d'un message : puces éditables. Entrée ou virgule valide un libellé, la croix le retire.

export const VeridianMailLabelsEditor: React.FC<{
  workspaceId: string
  message: Pick<MailMessage, 'id' | 'labels'>
  disabled?: boolean
  onChange: (m: MailMessage) => void
}> = ({ workspaceId, message, disabled, onChange }) => {
  const { t } = useLingui()
  const { token } = theme.useToken()
  const { message: toast } = App.useApp()
  const { errorText } = useMailLabels()
  const [busy, setBusy] = useState(false)
  const [draft, setDraft] = useState('')
  const [local, setLocal] = useState<string[] | null>(null)
  const shown = local ?? message.labels ?? []

  const commit = async (values: string[]) => {
    const { labels, rejected } = cleanLabels(values)
    if (rejected.length > 0) {
      toast.warning(t`Labels are limited to ${MAX_LABEL_LENGTH} characters, ${MAX_LABELS} labels at most.`)
    }
    setLocal(labels)
    setBusy(true)
    try {
      const updated = await mailService.update(workspaceId, message.id, { labels })
      onChange(updated)
    } catch (err) {
      toast.error(errorText(mailErrorKind(err)).title)
    } finally {
      setLocal(null)
      setBusy(false)
    }
  }

  const addDraft = (raw: string) => {
    const parts = raw.split(',')
    const rest = parts.pop() ?? ''
    const added = parts.map((p) => p.trim()).filter(Boolean)
    setDraft(rest)
    if (added.length > 0) void commit([...shown, ...added])
  }

  const submit = () => {
    const value = draft.trim()
    if (!value) return
    setDraft('')
    void commit([...shown, value])
  }

  return (
    <div data-testid="labels-editor">
      {shown.length > 0 && (
        <ul style={{ listStyle: 'none', margin: '0 0 10px', padding: 0, display: 'flex', flexWrap: 'wrap', gap: 6 }}>
          {shown.map((label) => (
            <li
              key={label}
              style={{ display: 'inline-flex', alignItems: 'center', gap: 4, padding: '2px 4px 2px 10px', borderRadius: 999, border: `1px solid ${token.colorBorder}`, fontSize: 13 }}
            >
              <span>{label}</span>
              <button
                type="button"
                disabled={disabled || busy}
                onClick={() => void commit(shown.filter((l) => l !== label))}
                aria-label={t`Remove the label ${label}`}
                className="focus-visible:outline-2 focus-visible:outline-offset-1 focus-visible:outline-[#7763f1]"
                style={{ border: 0, background: 'transparent', cursor: 'pointer', color: token.colorTextSecondary, width: 22, height: 22, borderRadius: 11, display: 'inline-flex', alignItems: 'center', justifyContent: 'center' }}
              >
                <CloseOutlined style={{ fontSize: 10 }} />
              </button>
            </li>
          ))}
        </ul>
      )}
      <Input
        value={draft}
        disabled={disabled || busy}
        maxLength={MAX_LABEL_LENGTH + 1}
        onChange={(e) => addDraft(e.target.value)}
        onPressEnter={submit}
        onBlur={submit}
        placeholder={t`Add a label, then press Enter`}
        aria-label={t`Add a label`}
        style={{ maxWidth: 320 }}
      />
    </div>
  )
}
