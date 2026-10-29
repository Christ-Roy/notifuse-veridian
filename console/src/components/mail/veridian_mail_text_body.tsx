import React, { useMemo, useState } from 'react'
import { Button, theme } from 'antd'
import { useLingui } from '@lingui/react/macro'

import { parseTextBody, type TextBlock } from './veridian_mail_rules'

// Corps texte d'un mail : rendu par React (échappé, jamais de HTML), liens http(s) cliquables,
// citations « > » repliées derrière un bouton.

const QuoteBlock: React.FC<{ block: Extract<TextBlock, { type: 'quote' }> }> = ({ block }) => {
  const { t } = useLingui()
  const { token } = theme.useToken()
  const [open, setOpen] = useState(false)
  return (
    <div style={{ margin: '8px 0' }}>
      <Button size="small" type="text" aria-expanded={open} onClick={() => setOpen((v) => !v)} style={{ color: token.colorTextSecondary }}>
        {open ? t`Hide the quoted text` : t`Show the quoted text`}
      </Button>
      {open && (
        <blockquote
          data-testid="quoted-text"
          style={{
            margin: '6px 0 0',
            padding: '2px 0 2px 14px',
            borderLeft: `3px solid ${token.colorBorder}`,
            color: token.colorTextSecondary,
            whiteSpace: 'pre-wrap',
            overflowWrap: 'anywhere'
          }}
        >
          {block.lines.join('\n')}
        </blockquote>
      )}
    </div>
  )
}

export const VeridianMailTextBody: React.FC<{ text: string }> = ({ text }) => {
  const blocks = useMemo(() => parseTextBody(text), [text])
  const { token } = theme.useToken()
  return (
    <div
      data-testid="mail-text-body"
      style={{ fontSize: 15, lineHeight: 1.65, color: token.colorText, overflowWrap: 'anywhere', maxWidth: '78ch' }}
    >
      {blocks.map((block, i) =>
        block.type === 'quote' ? (
          <QuoteBlock key={i} block={block} />
        ) : (
          <p key={i} style={{ margin: '0 0 12px', whiteSpace: 'pre-wrap' }}>
            {block.segments.map((seg, j) =>
              seg.type === 'link' ? (
                <a key={j} href={seg.href} target="_blank" rel="noopener noreferrer nofollow">
                  {seg.text}
                </a>
              ) : (
                <React.Fragment key={j}>{seg.text}</React.Fragment>
              )
            )}
          </p>
        )
      )}
    </div>
  )
}
