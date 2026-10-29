import React, { useEffect, useRef, useState } from 'react'
import { Badge, Input, Segmented, Select, theme } from 'antd'
import { SearchOutlined } from '@ant-design/icons'
import { useLingui } from '@lingui/react/macro'

import type { Mailbox } from '../../services/api/veridian_mail'
import { useMailLabels } from './veridian_mail_labels'
import { NATURE_FILTERS, STATE_FILTERS, type NatureFilter, type StateFilter } from './veridian_mail_rules'

const ALL = '__all__'

interface Props {
  mailboxes: readonly Mailbox[]
  boite?: string
  nature: NatureFilter
  etat: StateFilter
  q: string
  onBoite: (id: string | undefined) => void
  onNature: (n: NatureFilter) => void
  onEtat: (e: StateFilter) => void
  onQuery: (q: string) => void
}

// Filtres de la liste : boîte (une par profil d'envoi, avec ses compteurs), nature en français,
// traité ou à traiter, recherche (déclenchée 350 ms après la frappe).
export const VeridianMailFilters: React.FC<Props> = ({ mailboxes, boite, nature, etat, q, onBoite, onNature, onEtat, onQuery }) => {
  const { t } = useLingui()
  const { token } = theme.useToken()
  const { natureLabel, stateLabel } = useMailLabels()
  const [draft, setDraft] = useState(q)
  const lastSent = useRef(q)

  useEffect(() => {
    if (draft.trim() === lastSent.current) return
    const timer = setTimeout(() => {
      lastSent.current = draft.trim()
      onQuery(draft.trim())
    }, 350)
    return () => clearTimeout(timer)
  }, [draft, onQuery])

  const totalUnhandled = mailboxes.reduce((n, m) => n + m.count_unhandled, 0)
  const options = [
    { value: ALL, label: t`All mailboxes`, address: '', unhandled: totalUnhandled },
    ...mailboxes.map((m) => ({ value: m.id, label: m.profile_name || m.address || m.id, address: m.address, unhandled: m.count_unhandled }))
  ]

  return (
    <div data-testid="mail-filters" style={{ display: 'flex', flexDirection: 'column', gap: 10 }}>
      <Select
        value={boite ?? ALL}
        onChange={(v) => onBoite(v === ALL ? undefined : v)}
        options={options}
        aria-label={t`Mailbox`}
        style={{ width: '100%' }}
        optionRender={(opt) => {
          const o = opt.data as (typeof options)[number]
          return (
            <div style={{ display: 'flex', alignItems: 'center', gap: 8, justifyContent: 'space-between' }}>
              <span style={{ minWidth: 0 }}>
                <span style={{ display: 'block', fontWeight: 600, overflow: 'hidden', textOverflow: 'ellipsis' }}>{o.label}</span>
                {o.address && <span style={{ display: 'block', fontSize: 12, color: token.colorTextSecondary, overflow: 'hidden', textOverflow: 'ellipsis' }}>{o.address}</span>}
              </span>
              {o.unhandled > 0 && <Badge count={o.unhandled} overflowCount={999} color={token.colorPrimary} title={t`${o.unhandled} to handle`} />}
            </div>
          )
        }}
      />
      <div role="radiogroup" aria-label={t`Kind of message`} style={{ display: 'flex', flexWrap: 'wrap', gap: 6 }}>
        {NATURE_FILTERS.map((n) => {
          const active = n === nature
          return (
            <button
              key={n}
              type="button"
              role="radio"
              aria-checked={active}
              onClick={() => onNature(n)}
              className="focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-[#7763f1]"
              style={{
                padding: '3px 12px',
                borderRadius: 999,
                fontSize: 13,
                fontWeight: active ? 700 : 500,
                cursor: 'pointer',
                border: `1px solid ${active ? token.colorPrimary : token.colorBorder}`,
                background: active ? token.colorPrimaryBg : 'transparent',
                color: active ? token.colorPrimaryText : token.colorText
              }}
            >
              {natureLabel(n)}
            </button>
          )
        })}
      </div>
      <div style={{ display: 'flex', gap: 8, flexWrap: 'wrap' }}>
        <Segmented
          size="small"
          value={etat}
          onChange={(v) => onEtat(v as StateFilter)}
          options={STATE_FILTERS.map((s) => ({ value: s, label: stateLabel(s) }))}
          aria-label={t`Handling state`}
        />
        <Input
          allowClear
          size="small"
          prefix={<SearchOutlined />}
          value={draft}
          onChange={(e) => setDraft(e.target.value)}
          placeholder={t`Search subject, sender…`}
          aria-label={t`Search messages`}
          maxLength={200}
          style={{ flex: '1 1 160px', minWidth: 140 }}
        />
      </div>
    </div>
  )
}
