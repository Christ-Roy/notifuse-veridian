import { useMemo, useState } from 'react'
import { Button, Popover, Tag, Tooltip } from 'antd'
import { useLingui } from '@lingui/react/macro'
import dayjs from '../../lib/dayjs'
import {
  pickContactSegments,
  type ContactSegmentEntry,
  type ContactSegmentView
} from './veridian_contact_segments'

interface SegmentLookup {
  id: string
  name?: string
  color?: string
}

interface ContactSegmentsCellProps {
  contactSegments?: Array<{
    segment_id: string
    version?: number
    matched_at?: string
    computed_at?: string
  }>
  segments?: SegmentLookup[]
  timezone: string
}

const compactTag: React.CSSProperties = {
  margin: 0,
  fontSize: 12,
  lineHeight: '20px',
  maxWidth: 140,
  overflow: 'hidden',
  textOverflow: 'ellipsis',
  whiteSpace: 'nowrap',
  display: 'inline-block',
  verticalAlign: 'middle'
}

// Colonne « Segments » : 2 pastilles compactes au nom court, le reste sous un bouton « +N ».
// Tous les noms sont rendus en texte (jamais de HTML).
export function ContactSegmentsCell({ contactSegments, segments, timezone }: ContactSegmentsCellProps) {
  const { t } = useLingui()
  const [open, setOpen] = useState(false)

  const { visible, hidden } = useMemo(() => {
    const byId = new Map((segments ?? []).map((segment) => [segment.id, segment]))
    const entries: ContactSegmentEntry[] = (contactSegments ?? []).map((cs) => {
      const found = byId.get(cs.segment_id)
      return {
        segment_id: cs.segment_id,
        name: found?.name,
        color: found?.color,
        matched_at: cs.matched_at,
        version: cs.version
      }
    })
    return pickContactSegments(entries)
  }, [contactSegments, segments])

  if (visible.length === 0) return null

  const renderPill = (segment: ContactSegmentView) => {
    const matchedDate = segment.matched_at
      ? dayjs(segment.matched_at).tz(timezone).format('LL - HH:mm')
      : t`Unknown date`
    const tooltipTitle = (
      <>
        <div>
          <strong>{segment.name}</strong>
        </div>
        <div>
          {t`Matched on:`} {matchedDate}
        </div>
        {segment.version && (
          <div>
            {t`Version:`} {segment.version}
          </div>
        )}
        <div>
          <small>
            {t`Timezone:`} {timezone}
          </small>
        </div>
      </>
    )
    return (
      <Tooltip key={segment.segment_id} title={tooltipTitle} trigger={['hover', 'focus']}>
        <Tag bordered={false} color={segment.color} style={compactTag} tabIndex={0}>
          {segment.shortName}
        </Tag>
      </Tooltip>
    )
  }

  const moreLabel =
    hidden.length === 1 ? t`Show 1 more segment` : t`Show ${hidden.length} more segments`

  const hiddenList = (
    <div
      style={{ display: 'flex', flexDirection: 'column', gap: 4, maxWidth: 320 }}
      data-testid="contact-segments-hidden"
    >
      {hidden.map((segment) => (
        <Tag
          key={segment.segment_id}
          bordered={false}
          color={segment.color}
          style={{
            margin: 0,
            fontSize: 12,
            lineHeight: '20px',
            whiteSpace: 'normal',
            wordBreak: 'break-word'
          }}
        >
          {segment.name}
        </Tag>
      ))}
    </div>
  )

  return (
    <div style={{ display: 'flex', flexWrap: 'wrap', alignItems: 'center', gap: 4 }}>
      {visible.map(renderPill)}
      {hidden.length > 0 && (
        <Popover
          trigger={['hover', 'click']}
          open={open}
          onOpenChange={setOpen}
          content={hiddenList}
          placement="bottomLeft"
        >
          <Button
            type="text"
            size="small"
            aria-label={moreLabel}
            aria-haspopup="true"
            aria-expanded={open}
            style={{ fontSize: 12, minWidth: 28, height: 24, padding: '0 6px' }}
          >
            +{hidden.length}
          </Button>
        </Popover>
      )}
    </div>
  )
}
