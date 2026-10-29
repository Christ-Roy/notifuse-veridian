import React from 'react'
import { theme } from 'antd'
import {
  ArrowDownOutlined,
  ArrowUpOutlined,
  ClockCircleOutlined,
  CloseCircleOutlined,
  FileTextOutlined,
  MailOutlined,
  MessageOutlined,
  RobotOutlined
} from '@ant-design/icons'

import type { MailDirection, MailKind } from '../../services/api/veridian_mail'
import { useKindStyle } from './veridian_mail_theme'
import { useMailLabels } from './veridian_mail_labels'

export function KindIcon({ kind }: { kind: MailKind }): React.ReactElement {
  switch (kind) {
    case 'sent':
      return <ArrowUpOutlined />
    case 'reply_human':
      return <MessageOutlined />
    case 'reply_auto':
      return <RobotOutlined />
    case 'bounce_hard':
      return <CloseCircleOutlined />
    case 'bounce_soft':
      return <ClockCircleOutlined />
    case 'dsn':
      return <FileTextOutlined />
    default:
      return <MailOutlined />
  }
}

/** Pastille de nature : icône + mot, couleur à contraste AA (la couleur n'est jamais le seul signal). */
export const MailKindBadge: React.FC<{ kind: MailKind; compact?: boolean }> = ({ kind, compact }) => {
  const { kindLabel } = useMailLabels()
  const style = useKindStyle(kind)
  return (
    <span
      data-testid={`kind-badge-${kind}`}
      style={{
        display: 'inline-flex',
        alignItems: 'center',
        gap: 6,
        padding: compact ? '0 7px' : '2px 10px',
        borderRadius: 999,
        fontSize: compact ? 11 : 12,
        fontWeight: 600,
        lineHeight: compact ? '18px' : '20px',
        whiteSpace: 'nowrap',
        color: style.fg,
        background: style.bg,
        border: `1px solid ${style.border}`
      }}
    >
      <KindIcon kind={kind} />
      {kindLabel(kind)}
    </span>
  )
}

/** Sens du mail : flèche entrante (reçu) ou sortante (envoyé), avec un libellé pour les lecteurs d'écran. */
export const DirectionGlyph: React.FC<{ direction: MailDirection; label: string }> = ({ direction, label }) => {
  const { token } = theme.useToken()
  const Icon = direction === 'in' ? ArrowDownOutlined : ArrowUpOutlined
  return (
    <span role="img" aria-label={label} title={label} style={{ color: token.colorTextSecondary, fontSize: 12 }}>
      <Icon />
    </span>
  )
}

/** Puce ronde de la chronologie : l'icône de la nature sur son fond teinté. */
export const MailKindDot: React.FC<{ kind: MailKind; size?: number }> = ({ kind, size = 32 }) => {
  const style = useKindStyle(kind)
  return (
    <span
      aria-hidden="true"
      data-testid={`kind-dot-${kind}`}
      style={{
        display: 'inline-flex',
        alignItems: 'center',
        justifyContent: 'center',
        width: size,
        height: size,
        borderRadius: '50%',
        color: style.fg,
        background: style.bg,
        border: `2px solid ${style.border}`,
        fontSize: size * 0.45,
        flex: '0 0 auto'
      }}
    >
      <KindIcon kind={kind} />
    </span>
  )
}
