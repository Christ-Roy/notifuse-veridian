import React from 'react'
import { Button, Empty, Result, Skeleton, theme } from 'antd'
import { CheckCircleFilled, CheckCircleOutlined, PaperClipOutlined } from '@ant-design/icons'
import { useLingui } from '@lingui/react/macro'

import { mailErrorKind, type MailSummary } from '../../services/api/veridian_mail'
import { DirectionGlyph, MailKindBadge, MailKindDot } from './veridian_mail_kind'
import { exactDate, MAIL_TIMEZONE, relativeDate, useMailLabels } from './veridian_mail_labels'
import { counterpart, messageDate, previewOf } from './veridian_mail_rules'

interface RowProps {
  item: MailSummary
  selected: boolean
  timezone: string
  onSelect: (id: string) => void
  onToggleHandled: (item: MailSummary) => void
  busy: boolean
}

const Row: React.FC<RowProps> = ({ item, selected, timezone, onSelect, onToggleHandled, busy }) => {
  const { t } = useLingui()
  const { token } = theme.useToken()
  const { kindLabel } = useMailLabels()
  const handled = !!item.handled_at
  const unread = !handled && item.direction === 'in' && item.kind === 'reply_human'
  const when = messageDate(item)
  const who = counterpart(item)
  const subject = item.subject || t`(no subject)`
  const preview = previewOf(item)
  const direction = item.direction === 'in' ? t`Received` : t`Sent`

  return (
    <li
      data-testid="mail-row"
      data-id={item.id}
      data-selected={selected ? 'true' : 'false'}
      style={{
        display: 'flex',
        alignItems: 'stretch',
        borderBottom: `1px solid ${token.colorBorderSecondary}`,
        borderLeft: `3px solid ${selected ? token.colorPrimary : 'transparent'}`,
        background: selected ? token.colorPrimaryBg : 'transparent',
        opacity: handled && !selected ? 0.82 : 1
      }}
    >
      <button
        type="button"
        onClick={() => onSelect(item.id)}
        aria-current={selected ? 'true' : undefined}
        aria-label={`${kindLabel(item.kind)}, ${direction}, ${who}, ${subject}${handled ? `, ${t`handled`}` : ''}`}
        className="focus-visible:outline-2 focus-visible:-outline-offset-2 focus-visible:outline-[#7763f1]"
        style={{
          flex: 1,
          minWidth: 0,
          display: 'flex',
          gap: 12,
          padding: '12px 4px 12px 12px',
          border: 0,
          background: 'transparent',
          textAlign: 'left',
          font: 'inherit',
          color: 'inherit',
          cursor: 'pointer'
        }}
      >
        <MailKindDot kind={item.kind} size={34} />
        <span style={{ flex: 1, minWidth: 0, display: 'block' }}>
          <span style={{ display: 'flex', alignItems: 'baseline', gap: 8 }}>
            <DirectionGlyph direction={item.direction} label={direction} />
            <span style={{ flex: 1, minWidth: 0, overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap', fontWeight: unread ? 700 : 600 }}>{who}</span>
            <time dateTime={when} title={exactDate(when, timezone)} style={{ fontSize: 12, color: token.colorTextSecondary, whiteSpace: 'nowrap' }}>
              {relativeDate(when)}
            </time>
          </span>
          <span style={{ display: 'flex', alignItems: 'center', gap: 6, marginTop: 1 }}>
            <span style={{ flex: 1, minWidth: 0, overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap', fontWeight: unread ? 600 : 400 }}>{subject}</span>
            {item.has_attachments && (
              <span role="img" aria-label={t`Has attachments`} style={{ color: token.colorTextSecondary, fontSize: 13 }}>
                <PaperClipOutlined />
              </span>
            )}
          </span>
          {preview && (
            <span
              style={{
                display: '-webkit-box',
                WebkitLineClamp: 2,
                WebkitBoxOrient: 'vertical',
                overflow: 'hidden',
                marginTop: 2,
                fontSize: 13,
                lineHeight: 1.45,
                color: token.colorTextSecondary,
                overflowWrap: 'anywhere'
              }}
            >
              {preview}
            </span>
          )}
          {(item.kind !== 'reply_human' || (item.labels?.length ?? 0) > 0) && (
            <span style={{ display: 'flex', flexWrap: 'wrap', gap: 6, marginTop: 6 }}>
              {item.kind !== 'reply_human' && <MailKindBadge kind={item.kind} compact />}
              {(item.labels ?? []).slice(0, 3).map((l) => (
                <span key={l} style={{ fontSize: 11, padding: '0 7px', borderRadius: 999, border: `1px solid ${token.colorBorder}`, color: token.colorTextSecondary, lineHeight: '18px' }}>
                  {l}
                </span>
              ))}
            </span>
          )}
        </span>
      </button>
      <button
        type="button"
        onClick={() => onToggleHandled(item)}
        disabled={busy}
        aria-pressed={handled}
        aria-label={handled ? t`Mark as to handle: ${subject}` : t`Mark as handled: ${subject}`}
        title={handled ? t`Handled. Click to put it back to handle.` : t`Mark as handled`}
        className="focus-visible:outline-2 focus-visible:-outline-offset-2 focus-visible:outline-[#7763f1]"
        style={{
          flex: '0 0 44px',
          border: 0,
          background: 'transparent',
          cursor: busy ? 'progress' : 'pointer',
          fontSize: 20,
          color: handled ? token.colorSuccess : token.colorTextTertiary
        }}
      >
        {handled ? <CheckCircleFilled /> : <CheckCircleOutlined />}
      </button>
    </li>
  )
}

interface Props {
  items: MailSummary[]
  selectedId?: string
  loading: boolean
  error: unknown
  hasMore: boolean
  loadingMore: boolean
  filtered: boolean
  timezone?: string
  busyId?: string
  onSelect: (id: string) => void
  onToggleHandled: (item: MailSummary) => void
  onLoadMore: () => void
  onRetry: () => void
  onResetFilters: () => void
}

// Liste des messages : états chargement, erreur, vide travaillés, pagination par curseur.
export const VeridianMailList: React.FC<Props> = ({
  items,
  selectedId,
  loading,
  error,
  hasMore,
  loadingMore,
  filtered,
  timezone = MAIL_TIMEZONE,
  busyId,
  onSelect,
  onToggleHandled,
  onLoadMore,
  onRetry,
  onResetFilters
}) => {
  const { t } = useLingui()
  const { errorText } = useMailLabels()

  if (loading && items.length === 0) {
    return (
      <div data-testid="mail-list-loading" aria-busy="true" aria-label={t`Loading messages`} style={{ padding: 16 }}>
        <Skeleton active avatar paragraph={{ rows: 2 }} />
        <Skeleton active avatar paragraph={{ rows: 2 }} />
        <Skeleton active avatar paragraph={{ rows: 2 }} />
      </div>
    )
  }
  if (error && items.length === 0) {
    const text = errorText(mailErrorKind(error))
    return (
      <div data-testid="mail-list-error">
        <Result
          status="warning"
          title={text.title}
          subTitle={text.hint}
          extra={
            <Button type="primary" onClick={onRetry}>
              {t`Retry`}
            </Button>
          }
        />
      </div>
    )
  }
  if (items.length === 0) {
    return (
      <div data-testid="mail-list-empty" style={{ padding: '32px 16px' }}>
        <Empty
          image={Empty.PRESENTED_IMAGE_SIMPLE}
          description={filtered ? t`No message matches these filters.` : t`No message yet. Replies, bounces and sent mails will appear here.`}
        >
          {filtered && <Button onClick={onResetFilters}>{t`Clear the filters`}</Button>}
        </Empty>
      </div>
    )
  }
  return (
    <div>
      <ul aria-label={t`Messages`} data-testid="mail-list" style={{ listStyle: 'none', margin: 0, padding: 0 }}>
        {items.map((item) => (
          <Row
            key={item.id}
            item={item}
            selected={item.id === selectedId}
            timezone={timezone}
            onSelect={onSelect}
            onToggleHandled={onToggleHandled}
            busy={busyId === item.id}
          />
        ))}
      </ul>
      {error ? (
        <div style={{ padding: 12, textAlign: 'center' }}>
          <Button onClick={onLoadMore}>{t`Could not load more. Retry`}</Button>
        </div>
      ) : hasMore ? (
        <div style={{ padding: 12, textAlign: 'center' }}>
          <Button onClick={onLoadMore} loading={loadingMore}>
            {t`Load more`}
          </Button>
        </div>
      ) : (
        <div style={{ padding: 12, textAlign: 'center', fontSize: 12 }} aria-live="polite">
          {t`End of the list`}
        </div>
      )}
    </div>
  )
}
