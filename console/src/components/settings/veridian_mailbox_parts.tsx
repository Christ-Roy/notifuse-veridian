import { useMemo, useState } from 'react'
import { App, Button, theme, Tooltip } from 'antd'
import { CopyOutlined, EyeInvisibleOutlined, EyeOutlined } from '@ant-design/icons'
import { useLingui } from '@lingui/react/macro'

import { qrMatrix } from './veridian_mailbox_qr'
import { copyText } from './veridian_mailbox_rules'

// Briques communes de l'écran « Mailbox access » : copier une valeur, afficher ou masquer un mot de passe,
// code QR. Le mot de passe ne vit que dans l'état du composant qui l'affiche : jamais dans le stockage du
// navigateur, jamais dans une URL ni un journal.

export function CopyButton({ value, what }: { value: string; what: string }) {
  const { t } = useLingui()
  const { message } = App.useApp()
  return (
    <Tooltip title={t`Copy`}>
      <Button
        size="small"
        icon={<CopyOutlined />}
        aria-label={t`Copy ${what}`}
        onClick={async () => {
          if (await copyText(value)) message.success(t`Copied`)
          else message.error(t`Copy did not work: select the text and copy it by hand.`)
        }}
      />
    </Tooltip>
  )
}

/** Une valeur à recopier dans l'application : libellé, valeur en police fixe, bouton copier. */
export function ValueRow({ label, value, secret }: { label: string; value: string; secret?: boolean }) {
  const { token } = theme.useToken()
  return (
    <div
      role="group"
      aria-label={label}
      style={{
        display: 'flex',
        flexWrap: 'wrap',
        alignItems: 'center',
        gap: 8,
        padding: '8px 0',
        borderBottom: `1px solid ${token.colorBorderSecondary}`
      }}
    >
      <span style={{ flex: '1 1 140px', color: token.colorTextSecondary }}>{label}</span>
      {secret ? (
        <SecretValue value={value} label={label} />
      ) : (
        <>
          <code style={{ flex: '2 1 160px', overflowWrap: 'anywhere', color: token.colorText }}>{value}</code>
          <CopyButton value={value} what={label} />
        </>
      )}
    </div>
  )
}

/** Valeur secrète : masquée par défaut, bouton afficher / masquer, bouton copier. */
export function SecretValue({ value, label }: { value: string; label: string }) {
  const { t } = useLingui()
  const { token } = theme.useToken()
  const [shown, setShown] = useState(false)
  return (
    <>
      <code
        data-testid="secret-value"
        style={{ flex: '2 1 160px', overflowWrap: 'anywhere', color: token.colorText }}
      >
        {shown ? value : '•'.repeat(Math.min(Math.max(value.length, 8), 24))}
      </code>
      <Button
        size="small"
        icon={shown ? <EyeInvisibleOutlined /> : <EyeOutlined />}
        aria-pressed={shown}
        aria-label={shown ? t`Hide ${label}` : t`Show ${label}`}
        onClick={() => setShown((s) => !s)}
      >
        {shown ? t`Hide` : t`Show`}
      </Button>
      <CopyButton value={value} what={label} />
    </>
  )
}

/** Code QR en SVG, noir sur blanc quel que soit le thème (un lecteur exige le contraste). */
export function QrCode({ value, label, size = 192 }: { value: string; label: string; size?: number }) {
  const rows = useMemo(() => {
    try {
      return qrMatrix(value)
    } catch {
      return null
    }
  }, [value])
  if (!rows) return null
  const quiet = 4
  const n = rows.length + quiet * 2
  let path = ''
  rows.forEach((row, y) =>
    row.forEach((dark, x) => {
      if (dark) path += `M${x + quiet} ${y + quiet}h1v1h-1z`
    })
  )
  return (
    <svg
      role="img"
      aria-label={label}
      width={size}
      height={size}
      viewBox={`0 0 ${n} ${n}`}
      shapeRendering="crispEdges"
      style={{ maxWidth: '100%', height: 'auto', background: '#fff', borderRadius: 4 }}
    >
      <rect width={n} height={n} fill="#fff" />
      <path d={path} fill="#000" />
    </svg>
  )
}
