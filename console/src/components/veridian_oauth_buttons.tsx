/**
 * Veridian — boutons OAuth Hub sur /signin (Couche 4 bounce OAuth).
 *
 * Spec contractuelle : `veridian-hub/docs/CONTRAT-HUB.md` §6bis.8.1
 * (gravé v1.6, 2026-05-23).
 *
 * Quand un user choisit explicitement OAuth depuis la page login fallback
 * Notifuse, on délègue tout au Hub :
 *
 *   1. Click → `window.location.href = app.veridian.site/login?next=<self_url>`
 *   2. Hub gère le flow OAuth Google/Microsoft
 *   3. Après OAuth, le Hub appelle `POST /api/sso/issue-magic-link` côté
 *      Notifuse (HMAC §6.1) et redirige l'user vers le magic link Notifuse.
 *
 * Ce composant ne fait QUE le pas 1 — la redirection vers le Hub. Aucun
 * provider OAuth local, aucun callback, aucun credential Google/Microsoft.
 *
 * Gating :
 *   - mode = 'self-hosted' (instance autonome Notifuse) → boutons cachés
 *     (pas de Hub disponible, l'user passe par le magic link email)
 *   - hostname *.staging.veridian.site → boutons cachés (les providers
 *     OAuth n'ont pas la redirect_uri staging déclarée, cf. veridian-hub
 *     `utils/auth-helpers/settings.ts`)
 *   - sinon → 2 boutons standardisés affichés
 */

import { Button, Divider } from 'antd'
import { GoogleOutlined, WindowsOutlined } from '@ant-design/icons'
import { useQuery } from '@tanstack/react-query'
import { useLingui } from '@lingui/react/macro'
import { veridianApi, type VeridianModeResponse } from '../services/api/veridian'

const HUB_LOGIN_URL = 'https://app.veridian.site/login'

/**
 * Détecte si on est sur l'environnement staging.
 *
 * Convention Veridian : staging vit derrière Tailscale et n'est PAS déclaré
 * comme redirect URI chez Google/Microsoft (red flag réputation provider).
 * On gate l'affichage des boutons OAuth sur tout hostname `*.staging.veridian.site`.
 *
 * Robert peut tester OAuth :
 *   - en local-dev (localhost:3000, déclaré dans Client Google Hub)
 *   - en prod directe (notifuse.app.veridian.site)
 */
function isStagingEnv(): boolean {
  if (typeof window === 'undefined') return false
  return /\.staging\.veridian\.site$/i.test(window.location.hostname)
}

/**
 * Construit l'URL de bounce vers le Hub avec le param ?next= encodé.
 * Cf. CONTRAT-HUB §6bis.8.1 — pattern standardisé pour TOUTE app downstream.
 */
function buildHubLoginUrl(hubBaseUrl: string): string {
  const next = encodeURIComponent(window.location.href)
  // Tolérant aux variantes : hub_url peut venir avec ou sans trailing slash,
  // avec /dashboard ou nu. On normalise sur l'origine.
  let origin: string
  try {
    origin = new URL(hubBaseUrl).origin
  } catch {
    origin = HUB_LOGIN_URL.replace(/\/login$/, '')
  }
  return `${origin}/login?next=${next}`
}

export function VeridianOAuthButtons() {
  const { t } = useLingui()
  const { data, isLoading } = useQuery<VeridianModeResponse | null>({
    queryKey: ['veridian', 'mode'],
    queryFn: () => veridianApi.getMode().catch(() => null),
    staleTime: 10 * 60 * 1000,
    retry: false
  })

  // Pas de boutons tant qu'on ne sait pas si on est en mode managed (évite
  // un flash de boutons qui disparaissent ensuite en self-hosted).
  if (isLoading || !data) return null
  if (data.mode !== 'veridian-managed') return null
  if (isStagingEnv()) return null

  const hubLoginUrl = buildHubLoginUrl(data.hub_url || HUB_LOGIN_URL)

  const handleClick = () => {
    window.location.href = hubLoginUrl
  }

  return (
    <div data-testid="veridian-oauth-buttons">
      <Button
        block
        size="large"
        icon={<GoogleOutlined />}
        onClick={handleClick}
        style={{ marginBottom: 8 }}
      >
        {t`Continue with Google`}
      </Button>
      <Button
        block
        size="large"
        icon={<WindowsOutlined />}
        onClick={handleClick}
      >
        {t`Continue with Microsoft`}
      </Button>
      <Divider plain style={{ marginTop: 24, marginBottom: 16, color: '#9ca3af', fontSize: 12 }}>
        {t`or`}
      </Divider>
    </div>
  )
}
