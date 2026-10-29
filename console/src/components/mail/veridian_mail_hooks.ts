// Boîte unifiée : hooks de données (images incorporées, miniatures, flux d'événements).
// Aucun contenu de mail ni adresse n'est journalisé.

import { useEffect, useMemo, useRef, useState } from 'react'

import { mailService, type MailAttachment, type MailMessage, type MailStreamEvent } from '../../services/api/veridian_mail'
import { blobToDataUrl } from './veridian_mail_download'
import { normalizeCid } from './veridian_mail_frame_rules'
import { thumbnailType } from './veridian_mail_rules'

const INLINE_MAX_BYTES = 1.5 * 1024 * 1024
const INLINE_MAX_COUNT = 12

/**
 * Images incorporées (cid:) lues par l'API authentifiée puis converties en URL data: : l'iframe,
 * dont la CSP n'autorise que data:, peut ainsi les afficher sans jamais appeler le réseau.
 */
export function useInlineImages(workspaceId: string, message: MailMessage | undefined): Record<string, string> | undefined {
  const [map, setMap] = useState<Record<string, string>>({})
  const [forId, setForId] = useState('')
  const wanted = useMemo(
    () =>
      (message?.attachments ?? [])
        .filter((a) => a.inline && a.content_id && a.available && a.size <= INLINE_MAX_BYTES && thumbnailType(a))
        .slice(0, INLINE_MAX_COUNT),
    [message?.attachments]
  )
  const id = message?.id ?? ''
  useEffect(() => {
    if (!id || wanted.length === 0) return
    const ctl = new AbortController()
    void (async () => {
      const next: Record<string, string> = {}
      for (const att of wanted) {
        try {
          const file = await mailService.attachment(workspaceId, id, att.index, att.name, ctl.signal)
          const type = thumbnailType(att)
          if (!type) continue
          next[normalizeCid(att.content_id ?? '')] = await blobToDataUrl(new Blob([file.blob], { type }))
        } catch {
          /* une image incorporée illisible reste absente : le mail s'affiche sans elle */
        }
      }
      if (!ctl.signal.aborted) {
        setMap(next)
        setForId(id)
      }
    })()
    return () => ctl.abort()
  }, [workspaceId, id, wanted])
  if (wanted.length === 0) return undefined
  return forId === id ? map : undefined
}

/** Miniature d'une pièce jointe image : lue à la demande, URL d'objet libérée au démontage. */
export function useAttachmentThumbnail(workspaceId: string, messageId: string, att: MailAttachment): string | null {
  const [url, setUrl] = useState<string | null>(null)
  const type = thumbnailType(att)
  useEffect(() => {
    if (!type) return
    const ctl = new AbortController()
    let created: string | null = null
    void mailService
      .attachment(workspaceId, messageId, att.index, att.name, ctl.signal)
      .then((file) => {
        if (ctl.signal.aborted) return
        created = URL.createObjectURL(new Blob([file.blob], { type }))
        setUrl(created)
      })
      .catch(() => undefined)
    return () => {
      ctl.abort()
      if (created) URL.revokeObjectURL(created)
      setUrl(null)
    }
  }, [workspaceId, messageId, att.index, att.name, type])
  return url
}

export type StreamState = 'connecting' | 'open' | 'retry' | 'stopped'

/** Suit les nouveaux messages (flux SSE authentifié, reprise par Last-Event-ID), nettoyé au démontage. */
export function useMailEvents(workspaceId: string | undefined, onEvent: (e: MailStreamEvent) => void, enabled = true): StreamState {
  const [state, setState] = useState<StreamState>('connecting')
  const handler = useRef(onEvent)
  useEffect(() => {
    handler.current = onEvent
  }, [onEvent])
  useEffect(() => {
    if (!workspaceId || !enabled) return
    const ctl = new AbortController()
    void mailService.stream({
      workspaceId,
      direction: 'any',
      signal: ctl.signal,
      onEvent: (e) => handler.current(e),
      onState: (s) => setState(s === 'open' ? 'open' : s === 'retry' ? 'retry' : 'stopped')
    })
    return () => ctl.abort()
  }, [workspaceId, enabled])
  return state
}
