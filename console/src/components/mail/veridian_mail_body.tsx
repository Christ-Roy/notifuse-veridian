import React, { useMemo, useState } from 'react'
import { Alert, Button, Segmented, theme } from 'antd'
import { useLingui } from '@lingui/react/macro'

import type { MailMessage } from '../../services/api/veridian_mail'
import { VeridianMailBodyFrame } from './veridian_mail_body_frame'
import { useMailThemeMode, type FrameSurface } from './veridian_mail_theme'
import { countRemoteImages, hasOwnColors } from './veridian_mail_frame_rules'
import { useInlineImages } from './veridian_mail_hooks'
import { VeridianMailTextBody } from './veridian_mail_text_body'

// Corps d'un mail : HTML dans l'iframe isolé (images distantes bloquées jusqu'au clic), sinon texte
// brut rendu par React. Sert à la fiche et aux étapes dépliées de la chronologie.

const BodyInner: React.FC<{ workspaceId: string; message: MailMessage; compact?: boolean }> = ({ workspaceId, message, compact }) => {
  const { t } = useLingui()
  const { token } = theme.useToken()
  const themeMode = useMailThemeMode()
  const [allowRemote, setAllowRemote] = useState(false)
  const [format, setFormat] = useState<'html' | 'text'>('html')
  const [surface, setSurface] = useState<FrameSurface>('auto')
  const html = message.body_html_sanitized ?? ''
  const text = message.body_text ?? ''
  const hasHtml = html.trim() !== ''
  const hasText = text.trim() !== ''
  const remoteCount = useMemo(() => (hasHtml ? countRemoteImages(html) : 0), [hasHtml, html])
  const inlineImages = useInlineImages(workspaceId, message)
  const showHtml = hasHtml && (format === 'html' || !hasText)
  const paperIsLight = surface === 'light' || (surface === 'auto' && hasOwnColors(html))

  return (
    <div>
      {!compact && hasHtml && hasText && (
        <div style={{ marginBottom: 12 }}>
          <Segmented
            size="small"
            value={format}
            onChange={(v) => setFormat(v as 'html' | 'text')}
            options={[
              { label: t`Formatted`, value: 'html' },
              { label: t`Plain text`, value: 'text' }
            ]}
          />
        </div>
      )}
      {showHtml && remoteCount > 0 && !allowRemote && (
        <Alert
          type="info"
          showIcon
          data-testid="remote-images-banner"
          style={{ marginBottom: 12 }}
          message={t`Remote images are blocked (open tracking).`}
          action={
            <Button size="small" onClick={() => setAllowRemote(true)}>
              {t`Show images`}
            </Button>
          }
        />
      )}
      {!compact && themeMode === 'dark' && showHtml && (
        <div style={{ marginBottom: 8 }}>
          <Button size="small" type="link" onClick={() => setSurface(paperIsLight ? 'dark' : 'light')} style={{ paddingLeft: 0 }}>
            {paperIsLight ? t`Use a dark background` : t`Use a light background`}
          </Button>
        </div>
      )}
      {showHtml ? (
        <VeridianMailBodyFrame
          html={html}
          allowRemoteImages={allowRemote}
          inlineImages={inlineImages}
          surface={surface}
          title={t`Content of the mail “${message.subject}”`}
        />
      ) : hasText ? (
        <VeridianMailTextBody text={text} />
      ) : message.body_source === 'unavailable' ? (
        <Alert
          type="info"
          showIcon
          data-testid="body-unavailable"
          message={t`The content of this mail is not available`}
          description={t`It was sent before the unified inbox and its template could not be rebuilt. Headers and delivery status remain readable.`}
        />
      ) : message.dsn ? (
        <span style={{ color: token.colorTextSecondary }}>{t`This delivery report carries no readable body: see the explanation above.`}</span>
      ) : (
        <span style={{ color: token.colorTextSecondary }}>{t`This message has no content.`}</span>
      )}
    </div>
  )
}

export const VeridianMailBody: React.FC<{ workspaceId: string; message: MailMessage; compact?: boolean }> = (props) => (
  <BodyInner key={props.message.id} {...props} />
)
