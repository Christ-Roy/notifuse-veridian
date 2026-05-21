/**
 * Veridian — footer "Powered by Veridian — Manage subscription".
 *
 * Affichage discret en bas de page console quand mode === 'veridian-managed'.
 * Le pivot pricing 2026-05-21 acte que le branding sur l'app reste neutre
 * sauf white-label Business+ (feature gate côté backend). On garde un
 * footer informatif léger qui sert deux objectifs :
 *
 *   1. Trace de l'opérateur (Veridian) pour le user qui ne sait pas où il
 *      est arrivé après auto-login depuis le Hub
 *   2. Raccourci CTA "Manage subscription" → Stripe Billing portal côté Hub
 *
 * Choix UX (à valider en session calme Robert) :
 *   - Texte gris (#9ca3af tailwind gray-400) small (11px)
 *   - Centré horizontalement
 *   - Padding vertical modeste (8px) pour ne pas voler de l'espace
 *   - Lien "Manage subscription" → app.veridian.site/dashboard
 *
 * Robert peut faire évoluer en session calme :
 *   - Logo Veridian discret à gauche du texte
 *   - Lien docs / support / status page
 */

import { useQuery } from '@tanstack/react-query'
import { useLingui } from '@lingui/react/macro'
import { veridianApi, type VeridianModeResponse } from '../services/api/veridian'

const VERIDIAN_HUB_URL = 'https://app.veridian.site/dashboard'

export function VeridianBrandFooter() {
  const { t } = useLingui()
  const { data } = useQuery<VeridianModeResponse | null>({
    queryKey: ['veridian', 'mode'],
    queryFn: () => veridianApi.getMode().catch(() => null),
    staleTime: 10 * 60 * 1000,
    retry: false
  })

  if (!data || data.mode !== 'veridian-managed') {
    return null
  }

  const hubUrl = data.hub_url || VERIDIAN_HUB_URL

  return (
    <div
      style={{
        textAlign: 'center',
        padding: '8px 16px',
        fontSize: 11,
        color: '#9ca3af'
      }}
    >
      {t`Powered by Veridian`}
      <span style={{ margin: '0 6px' }}>—</span>
      <a
        href={hubUrl}
        target="_blank"
        rel="noreferrer"
        style={{ color: '#9ca3af', textDecoration: 'underline' }}
      >
        {t`Manage subscription`}
      </a>
    </div>
  )
}
