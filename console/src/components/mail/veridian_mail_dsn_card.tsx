import React from 'react'
import { Collapse, theme } from 'antd'
import { useLingui } from '@lingui/react/macro'

import type { MailDSN } from '../../services/api/veridian_mail'
import { KIND_STYLE } from './veridian_mail_rules'
import { useMailThemeMode } from './veridian_mail_theme'

// Rapport de non-remise expliqué : la raison en français en grand, le conseil, puis les détails
// techniques (code, fournisseur, adresse en échec) et le diagnostic brut, replié.

export const VeridianMailDsnCard: React.FC<{ dsn: MailDSN }> = ({ dsn }) => {
  const { t } = useLingui()
  const { token } = theme.useToken()
  const mode = useMailThemeMode()
  const tone = dsn.severity === 'hard' ? KIND_STYLE.bounce_hard[mode] : dsn.severity === 'soft' ? KIND_STYLE.bounce_soft[mode] : KIND_STYLE.dsn[mode]

  const title =
    dsn.action === 'failed'
      ? dsn.severity === 'soft'
        ? t`The mail could not be delivered for now`
        : t`The mail was not delivered`
      : dsn.action === 'delayed'
        ? t`Delivery is delayed`
        : dsn.action === 'delivered' || dsn.action === 'relayed' || dsn.action === 'expanded'
          ? t`Delivery report`
          : dsn.severity === 'hard'
            ? t`The mail was not delivered`
            : t`Delivery report`

  const facts: Array<{ label: string; value: string; mono?: boolean }> = [
    dsn.recipient ? { label: t`Failed address`, value: dsn.recipient, mono: true } : null,
    dsn.code ? { label: t`Status code`, value: dsn.code, mono: true } : null,
    dsn.provider ? { label: t`Provider`, value: dsn.provider } : null
  ].filter((f): f is { label: string; value: string; mono?: boolean } => f !== null)

  return (
    <section
      aria-label={title}
      data-testid="dsn-card"
      style={{
        border: `1px solid ${tone.border}`,
        borderLeft: `6px solid ${tone.fg}`,
        background: tone.bg,
        borderRadius: 8,
        padding: '16px 20px'
      }}
    >
      <div style={{ fontSize: 12, fontWeight: 700, letterSpacing: 0.4, textTransform: 'uppercase', color: tone.fg }}>{title}</div>
      <div data-testid="dsn-reason" style={{ fontSize: 20, fontWeight: 600, lineHeight: 1.35, margin: '6px 0 0', color: token.colorText }}>
        {dsn.reason_fr}
      </div>
      {dsn.advice_fr && (
        <p data-testid="dsn-advice" style={{ margin: '10px 0 0', fontSize: 15, lineHeight: 1.55, color: token.colorText }}>
          <strong>{t`What it means for your prospecting`}</strong>
          {' : '}
          {dsn.advice_fr}
        </p>
      )}
      {facts.length > 0 && (
        <dl style={{ display: 'grid', gridTemplateColumns: 'max-content 1fr', gap: '6px 16px', margin: '14px 0 0', fontSize: 13 }}>
          {facts.map((f) => (
            <React.Fragment key={f.label}>
              <dt style={{ color: token.colorTextSecondary }}>{f.label}</dt>
              <dd style={{ margin: 0, fontFamily: f.mono ? 'ui-monospace, SFMono-Regular, Menlo, monospace' : undefined, overflowWrap: 'anywhere' }}>
                {f.value}
              </dd>
            </React.Fragment>
          ))}
        </dl>
      )}
      {dsn.diagnostic && (
        <Collapse
          ghost
          size="small"
          style={{ marginTop: 8, marginLeft: -12 }}
          items={[
            {
              key: 'diag',
              label: t`Technical diagnostic`,
              children: (
                <pre
                  data-testid="dsn-diagnostic"
                  style={{ margin: 0, whiteSpace: 'pre-wrap', overflowWrap: 'anywhere', fontSize: 12.5, color: token.colorTextSecondary }}
                >
                  {dsn.diagnostic}
                </pre>
              )
            }
          ]}
        />
      )}
    </section>
  )
}
