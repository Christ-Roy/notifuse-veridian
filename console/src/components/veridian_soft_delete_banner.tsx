/**
 * Veridian — bandeau soft-delete persistant.
 *
 * Affiche un Alert top de page dès qu'une réponse backend a contenu le
 * header `X-Tenant-Soft-Deleted: true` (cf. lot J,
 * `veridian_paywall_softdeleted.go`). Persiste tant que la session UI dure.
 *
 * Source des dates :
 *   - `X-Tenant-Deleted-At` → date à laquelle le soft-delete a été effectué
 *   - `X-Tenant-Purge-At`   → date jusqu'à laquelle restauration possible
 *
 * Choix UX (à valider en session calme Robert) :
 *   - `<Alert type="error">` au-dessus du contenu workspace, dismissible NON
 *     (on veut que le user voie tant qu'il est dans l'app dégradée)
 *   - CTA renvoie vers le Hub Veridian (où sit le bouton "Restaurer"
 *     côté Hub UI — cf. ticket Hub `tenant-sync-strategy`)
 *   - URL Hub par défaut : `app.veridian.site/dashboard?restore=<wsId>` —
 *     Robert ajustera en session calme avec la vraie route Hub
 */

import { useEffect, useState } from 'react'
import { Alert, Button } from 'antd'
import { useLingui } from '@lingui/react/macro'
import {
  VERIDIAN_SOFT_DELETE_EVENT,
  type VeridianSoftDeleteEventDetail
} from '../services/api/veridian_402_interceptor'

const VERIDIAN_HUB_DASHBOARD = 'https://app.veridian.site/dashboard'

function formatDate(iso: string | undefined): string {
  if (!iso) return ''
  try {
    return new Date(iso).toLocaleDateString(undefined, {
      year: 'numeric',
      month: 'short',
      day: 'numeric'
    })
  } catch {
    return iso
  }
}

interface Props {
  workspaceId?: string
}

export function VeridianSoftDeleteBanner({ workspaceId }: Props) {
  const { t } = useLingui()
  const [state, setState] = useState<VeridianSoftDeleteEventDetail | null>(null)

  useEffect(() => {
    const handler = (evt: Event) => {
      const detail = (evt as CustomEvent<VeridianSoftDeleteEventDetail>).detail
      setState(detail)
    }
    window.addEventListener(VERIDIAN_SOFT_DELETE_EVENT, handler)
    return () => window.removeEventListener(VERIDIAN_SOFT_DELETE_EVENT, handler)
  }, [])

  if (!state) return null

  const deletedAtStr = formatDate(state.deletedAt)
  const purgeAtStr = formatDate(state.purgeAt)
  const restoreUrl = workspaceId
    ? `${VERIDIAN_HUB_DASHBOARD}?restore=${encodeURIComponent(workspaceId)}`
    : VERIDIAN_HUB_DASHBOARD

  const message = purgeAtStr
    ? t`Your workspace was deleted on ${deletedAtStr || t`(unknown date)`} — restoration possible until ${purgeAtStr}.`
    : t`Your workspace was deleted on ${deletedAtStr || t`(unknown date)`}.`

  return (
    <Alert
      type="error"
      showIcon
      banner
      message={message}
      action={
        <Button
          size="small"
          danger
          type="primary"
          onClick={() => window.open(restoreUrl, '_blank', 'noreferrer')}
        >
          {t`Restore in Veridian`}
        </Button>
      }
      style={{ marginBottom: 0 }}
    />
  )
}
