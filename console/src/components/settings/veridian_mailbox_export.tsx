import { useState } from 'react'
import { Alert, Button, Space, Typography } from 'antd'
import { DownloadOutlined } from '@ant-design/icons'
import { useLingui } from '@lingui/react/macro'

import type { Integration } from '../../services/api/workspace'
import {
  mailboxAccessService,
  MAILBOX_FOLDERS,
  type MailboxFolder
} from '../../services/api/veridian_mailbox_access'
import { useMailboxLabels } from './veridian_mailbox_labels'
import { emailProfiles } from './veridian_mailbox_rules'

const { Paragraph, Text } = Typography

// Export .mbox par profil et par dossier : le fichier arrive par fetch + blob avec l'en-tête Authorization.
export function VeridianMailboxExport({
  workspaceId,
  integrations
}: {
  workspaceId: string
  integrations: Integration[] | undefined
}) {
  const { t } = useLingui()
  const labels = useMailboxLabels()
  const profiles = emailProfiles(integrations)
  const [busy, setBusy] = useState('')
  const [error, setError] = useState('')

  if (profiles.length === 0) return null

  const download = async (profile: Integration, folder: MailboxFolder) => {
    setBusy(`${profile.id}:${folder}`)
    setError('')
    try {
      const file = await mailboxAccessService.exportMbox(workspaceId, profile.id, folder)
      const url = URL.createObjectURL(file.blob)
      const a = document.createElement('a')
      a.href = url
      a.download = file.filename
      document.body.appendChild(a)
      a.click()
      a.remove()
      URL.revokeObjectURL(url)
    } catch (err) {
      setError(labels.errorOf(err))
    } finally {
      setBusy('')
    }
  }

  return (
    <section aria-labelledby="mbx-export-title" style={{ marginTop: 32 }}>
      <h3 id="mbx-export-title" style={{ marginBottom: 4 }}>
        {t`Export your mail`}
      </h3>
      <Paragraph type="secondary">{t`Import it in Thunderbird, Apple Mail or another provider.`}</Paragraph>
      {error && <Alert type="error" showIcon role="alert" message={error} style={{ marginBottom: 12 }} />}
      {profiles.map((p) => (
        <div key={p.id} style={{ marginBottom: 12 }}>
          <Text strong style={{ display: 'block', marginBottom: 4, overflowWrap: 'anywhere' }}>
            {p.name}
          </Text>
          <Space wrap>
            {MAILBOX_FOLDERS.map((f) => (
              <FolderButton
                key={f}
                folderLabel={labels.folder(f)}
                profileName={p.name}
                loading={busy === `${p.id}:${f}`}
                disabled={busy !== '' && busy !== `${p.id}:${f}`}
                onClick={() => download(p, f)}
              />
            ))}
          </Space>
        </div>
      ))}
    </section>
  )
}

function FolderButton({
  folderLabel,
  profileName,
  loading,
  disabled,
  onClick
}: {
  folderLabel: string
  profileName: string
  loading: boolean
  disabled: boolean
  onClick: () => void
}) {
  const { t } = useLingui()
  return (
    <Button
      size="small"
      icon={<DownloadOutlined />}
      loading={loading}
      disabled={disabled}
      aria-label={t`Download ${folderLabel} (${profileName})`}
      onClick={onClick}
    >
      {t`Download ${folderLabel}`}
    </Button>
  )
}
