import React, { useState } from 'react'
import { App, Button, theme, Tooltip } from 'antd'
import {
  AudioOutlined,
  CalendarOutlined,
  DownloadOutlined,
  FileExcelOutlined,
  FileImageOutlined,
  FileOutlined,
  FilePdfOutlined,
  FilePptOutlined,
  FileTextOutlined,
  FileWordOutlined,
  FileZipOutlined,
  VideoCameraOutlined
} from '@ant-design/icons'
import { useLingui } from '@lingui/react/macro'

import { mailService, mailErrorKind, type MailAttachment } from '../../services/api/veridian_mail'
import { saveBlob } from './veridian_mail_download'
import { useAttachmentThumbnail } from './veridian_mail_hooks'
import { useMailLabels } from './veridian_mail_labels'
import { attachmentGlyph, thumbnailType, type AttachmentGlyph } from './veridian_mail_rules'

const GLYPHS: Record<AttachmentGlyph, React.ReactElement> = {
  image: <FileImageOutlined />,
  pdf: <FilePdfOutlined />,
  word: <FileWordOutlined />,
  excel: <FileExcelOutlined />,
  powerpoint: <FilePptOutlined />,
  archive: <FileZipOutlined />,
  text: <FileTextOutlined />,
  audio: <AudioOutlined />,
  video: <VideoCameraOutlined />,
  calendar: <CalendarOutlined />,
  other: <FileOutlined />
}

const AttachmentCard: React.FC<{ workspaceId: string; messageId: string; att: MailAttachment }> = ({ workspaceId, messageId, att }) => {
  const { t } = useLingui()
  const { message } = App.useApp()
  const { token } = theme.useToken()
  const { size, errorText } = useMailLabels()
  const [busy, setBusy] = useState(false)
  const thumb = useAttachmentThumbnail(workspaceId, messageId, att)
  const glyph = attachmentGlyph(att)
  const name = att.name || t`Unnamed attachment`

  const download = async () => {
    setBusy(true)
    try {
      const file = await mailService.attachment(workspaceId, messageId, att.index, name)
      saveBlob(file.blob, file.filename || name)
    } catch (err) {
      message.error(errorText(mailErrorKind(err)).title)
    } finally {
      setBusy(false)
    }
  }

  return (
    <li
      data-testid="attachment"
      style={{
        display: 'flex',
        alignItems: 'center',
        gap: 12,
        padding: 10,
        border: `1px solid ${token.colorBorderSecondary}`,
        borderRadius: 8,
        background: token.colorBgContainer,
        minWidth: 0
      }}
    >
      {thumb ? (
        <img
          src={thumb}
          alt={t`Preview of ${name}`}
          style={{ width: 56, height: 56, objectFit: 'cover', borderRadius: 6, border: `1px solid ${token.colorBorderSecondary}`, flex: '0 0 auto' }}
        />
      ) : (
        <span
          aria-hidden="true"
          data-glyph={glyph}
          style={{ width: 56, height: 56, display: 'inline-flex', alignItems: 'center', justifyContent: 'center', fontSize: 26, color: token.colorTextSecondary, background: token.colorFillQuaternary, borderRadius: 6, flex: '0 0 auto' }}
        >
          {GLYPHS[glyph]}
        </span>
      )}
      <div style={{ minWidth: 0, flex: 1 }}>
        <Tooltip title={name}>
          <div style={{ fontWeight: 600, overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' }}>{name}</div>
        </Tooltip>
        <div style={{ fontSize: 12, color: token.colorTextSecondary }}>
          {size(att.size)}
          {!att.available && ` · ${t`Content not kept`}`}
          {att.available && thumbnailType(att) === null && glyph === 'image' && ` · ${t`No preview`}`}
        </div>
      </div>
      <Button
        icon={<DownloadOutlined />}
        loading={busy}
        disabled={!att.available}
        onClick={download}
        aria-label={t`Download ${name}`}
      >
        {t`Download`}
      </Button>
    </li>
  )
}

export const VeridianMailAttachments: React.FC<{ workspaceId: string; messageId: string; attachments: MailAttachment[] }> = ({
  workspaceId,
  messageId,
  attachments
}) => {
  const { t } = useLingui()
  if (attachments.length === 0) return null
  return (
    <section aria-label={t`Attachments`} data-testid="attachments">
      <div style={{ fontWeight: 600, marginBottom: 8 }}>{attachments.length === 1 ? t`1 attachment` : t`${attachments.length} attachments`}</div>
      <ul style={{ listStyle: 'none', margin: 0, padding: 0, display: 'grid', gridTemplateColumns: 'repeat(auto-fill, minmax(280px, 1fr))', gap: 10 }}>
        {attachments.map((att) => (
          <AttachmentCard key={att.index} workspaceId={workspaceId} messageId={messageId} att={att} />
        ))}
      </ul>
    </section>
  )
}
