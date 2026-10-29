import React, { useCallback, useEffect, useMemo, useRef, useState } from 'react'
import { theme } from 'antd'
import { useLingui } from '@lingui/react/macro'

import { i18n } from '@lingui/core'
import { buildSrcDoc, MAIL_FRAME_SANDBOX } from './veridian_mail_frame_rules'
import { effectiveFrameMode, useMailThemeMode, type FrameSurface } from './veridian_mail_theme'

interface Props {
  html: string
  allowRemoteImages: boolean
  /** content-id normalisé -> URL data: des images incorporées. */
  inlineImages?: Record<string, string>
  surface?: FrameSurface
  /** Libellé accessible de l'iframe. */
  title?: string
  minHeight?: number
}

const MAX_HEIGHT = 30000

// Le corps HTML d'un mail, dans un iframe isolé : bac à sable sans scripts, CSP stricte, hauteur
// automatique. Rien de ce HTML n'entre dans le DOM de la console.
export const VeridianMailBodyFrame: React.FC<Props> = ({ html, allowRemoteImages, inlineImages, surface = 'auto', title, minHeight = 120 }) => {
  const { t } = useLingui()
  const { token } = theme.useToken()
  const themeMode = useMailThemeMode()
  const mode = effectiveFrameMode(themeMode, html, surface)
  const frameRef = useRef<HTMLIFrameElement>(null)
  const observerRef = useRef<ResizeObserver | null>(null)
  const [height, setHeight] = useState(minHeight)

  const srcDoc = useMemo(
    () =>
      buildSrcDoc({
        html,
        allowRemoteImages,
        mode,
        lang: i18n.locale || 'fr',
        inlineImages,
        palette: mode === 'dark' ? { background: token.colorBgContainer } : undefined
      }),
    [html, allowRemoteImages, mode, inlineImages, token.colorBgContainer]
  )

  const measure = useCallback(() => {
    const doc = frameRef.current?.contentDocument
    if (!doc?.documentElement) return
    const h = Math.ceil(doc.documentElement.getBoundingClientRect().height)
    if (h > 0) setHeight(Math.min(MAX_HEIGHT, Math.max(minHeight, h)))
  }, [minHeight])

  const attach = useCallback(() => {
    measure()
    observerRef.current?.disconnect()
    const root = frameRef.current?.contentDocument?.documentElement
    if (root && typeof ResizeObserver !== 'undefined') {
      observerRef.current = new ResizeObserver(() => measure())
      observerRef.current.observe(root)
    }
  }, [measure])

  // le flux d'observation est nettoyé au démontage
  useEffect(() => () => observerRef.current?.disconnect(), [])

  return (
    <iframe
      ref={frameRef}
      title={title || t`Message content`}
      sandbox={MAIL_FRAME_SANDBOX}
      srcDoc={srcDoc}
      referrerPolicy="no-referrer"
      onLoad={attach}
      data-testid="mail-body-frame"
      style={{
        display: 'block',
        width: '100%',
        height,
        border: 0,
        borderRadius: 6,
        background: mode === 'dark' ? token.colorBgContainer : '#ffffff'
      }}
    />
  )
}
