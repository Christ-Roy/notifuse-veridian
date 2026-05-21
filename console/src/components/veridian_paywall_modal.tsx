/**
 * Veridian — modal paywall global.
 *
 * Écoute les CustomEvents dispatched par `veridian_402_interceptor.ts` :
 *
 *   - `veridian:paywall` (HTTP 402) → modal d'erreur bloquant + CTA Veridian
 *   - `veridian:hub-sync-dead` (HTTP 503 dégradé) → toast informatif moins
 *     anxiogène (le service reviendra automatiquement)
 *
 * Choix UX (à valider en session calme Robert) :
 *   - Modal.error vs Modal.warning : on prend Modal.error pour 402 (bloque
 *     l'usage, le user doit aller chez Veridian)
 *   - Notification (toast) vs Modal pour hub_sync_dead : toast warning car
 *     c'est temporaire et l'app reste utilisable en lecture
 *   - CTA unique vers app.veridian.site/dashboard (Stripe Billing portal)
 *   - Pas de bouton "Plus tard" sur 402 — c'est le but du paywall
 *
 * Le composant n'a pas de UI propre, il consomme `useApp()` Ant Design.
 * À monter une fois dans WorkspaceLayout (sous AntApp).
 */

import { useEffect, useRef } from 'react'
import { App } from 'antd'
import { useLingui } from '@lingui/react/macro'
import {
  VERIDIAN_PAYWALL_EVENT,
  VERIDIAN_HUB_SYNC_DEAD_EVENT,
  type VeridianPaywallEventDetail
} from '../services/api/veridian_402_interceptor'

const VERIDIAN_HUB_URL = 'https://app.veridian.site/dashboard'

export function VeridianPaywallModal() {
  const { t } = useLingui()
  const { modal, notification } = App.useApp()
  // Throttle : un seul modal/toast à la fois même si l'interceptor envoie
  // plusieurs events rapprochés (ex: dashboard fetche 5 widgets en // qui
  // se prennent 5x 402). Reset à 30s.
  const modalOpenRef = useRef<boolean>(false)
  const hubSyncNotifiedAtRef = useRef<number>(0)

  useEffect(() => {
    const handlePaywall = (evt: Event) => {
      if (modalOpenRef.current) return
      const detail = (evt as CustomEvent<VeridianPaywallEventDetail>).detail
      modalOpenRef.current = true
      modal.error({
        title: t`Account suspended`,
        content: detail.error
          ? t`Your subscription is no longer active. Manage your billing at Veridian to restore access.`
          : t`Your subscription is no longer active. Manage your billing at Veridian to restore access.`,
        okText: t`Open Veridian`,
        onOk: () => {
          window.open(VERIDIAN_HUB_URL, '_blank', 'noreferrer')
          modalOpenRef.current = false
        },
        onCancel: () => {
          modalOpenRef.current = false
        },
        // Pas de bouton Cancel sur paywall actif : c'est bloquant business.
        // Robert peut adoucir en session calme (ex: "Export my data first").
        closable: false,
        maskClosable: false
      })
    }

    const handleHubSyncDead = (evt: Event) => {
      const detail = (evt as CustomEvent<VeridianPaywallEventDetail>).detail
      // Anti-spam : 1 toast / 5min max.
      const now = Date.now()
      if (now - hubSyncNotifiedAtRef.current < 5 * 60 * 1000) return
      hubSyncNotifiedAtRef.current = now

      const retryHours = detail.retryAfter
        ? Math.max(1, Math.round(detail.retryAfter / 3600))
        : 1
      notification.warning({
        message: t`Temporary degraded mode`,
        description: t`Veridian Hub is unreachable. The app is in read-only mode — please try again in ${retryHours}h.`,
        duration: 10
      })
    }

    window.addEventListener(VERIDIAN_PAYWALL_EVENT, handlePaywall)
    window.addEventListener(VERIDIAN_HUB_SYNC_DEAD_EVENT, handleHubSyncDead)

    return () => {
      window.removeEventListener(VERIDIAN_PAYWALL_EVENT, handlePaywall)
      window.removeEventListener(VERIDIAN_HUB_SYNC_DEAD_EVENT, handleHubSyncDead)
    }
  }, [modal, notification, t])

  return null
}
