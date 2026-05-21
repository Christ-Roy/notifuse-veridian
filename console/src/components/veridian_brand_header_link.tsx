/**
 * Veridian — lien discret "← Back to Veridian dashboard" pour le header
 * console quand le mode est `veridian-managed`.
 *
 * Lit `GET /api/veridian/mode` au mount (cached via TanStack Query) et
 * affiche uniquement si mode === 'veridian-managed' ET `hub_url` présent.
 *
 * Choix UX (à valider en session calme Robert) :
 *   - Position : à intégrer dans `WorkspaceLayout` Header, à gauche du
 *     Select workspace
 *   - Style : Button type="link" small, icône ArrowLeft, label compact
 *   - Couleur : par défaut Ant Design (primary muted) — pas de palette
 *     custom à ce stade
 */

import { Button } from 'antd'
import { ArrowLeftOutlined } from '@ant-design/icons'
import { useLingui } from '@lingui/react/macro'
import { useQuery } from '@tanstack/react-query'
import { veridianApi, type VeridianModeResponse } from '../services/api/veridian'

export function VeridianBrandHeaderLink() {
  const { t } = useLingui()
  const { data } = useQuery<VeridianModeResponse | null>({
    queryKey: ['veridian', 'mode'],
    queryFn: () => veridianApi.getMode().catch(() => null),
    staleTime: 10 * 60 * 1000,
    retry: false
  })

  if (!data || data.mode !== 'veridian-managed' || !data.hub_url) {
    return null
  }

  return (
    <Button
      type="link"
      size="small"
      icon={<ArrowLeftOutlined />}
      onClick={() => window.open(data.hub_url, '_blank', 'noreferrer')}
      style={{ paddingLeft: 0 }}
    >
      {t`Veridian dashboard`}
    </Button>
  )
}
