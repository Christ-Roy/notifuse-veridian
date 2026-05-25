/**
 * Veridian — cards cross-app affichées sur la page sélection workspace.
 *
 * Appelle `GET /api/veridian/hub-discovery/me` et, si le user a d'autres
 * tenants Veridian (Prospection, Analytics, CMS) en plus de Notifuse,
 * affiche une card par app avec un bouton "Ouvrir" vers le Hub.
 *
 * Best-effort total :
 *   - Hub down / disabled / 404 → composant retourne `null` (rien affiché)
 *   - Pas de tenants autres que notifuse → null
 *   - User pas encore loggué → null (hook désactivé)
 *
 * Le Hub (`app.veridian.site`) sert lui-même de redirecteur cross-app :
 * cliquer sur "Ouvrir Prospection" envoie sur app.veridian.site qui sait
 * router vers la bonne app selon les tenants du user.
 *
 * Cf. ticket todo/2026-05-24-veridian-hub-discovery-handler-e2e-validation.md.
 */

import { Card, Button, Typography, Space } from 'antd'
import { useLingui } from '@lingui/react/macro'
import { useQuery } from '@tanstack/react-query'
import { useAuth } from '../contexts/AuthContext'
import { useVeridianHubDiscovery } from '../hooks/useVeridianHubDiscovery'
import { veridianApi, type VeridianModeResponse } from '../services/api/veridian'

const DEFAULT_HUB_URL = 'https://app.veridian.site/dashboard'

const APP_LABELS: Record<string, { label: string; emoji: string }> = {
  prospection: { label: 'Prospection', emoji: '🎯' },
  analytics: { label: 'Analytics', emoji: '📊' },
  cms: { label: 'CMS', emoji: '📝' }
}

export function VeridianCrossAppCards() {
  const { t } = useLingui()
  const { isAuthenticated } = useAuth()
  const { data: discovery } = useVeridianHubDiscovery(isAuthenticated)
  const { data: mode } = useQuery<VeridianModeResponse | null>({
    queryKey: ['veridian', 'mode'],
    queryFn: () => veridianApi.getMode().catch(() => null),
    staleTime: 10 * 60 * 1000,
    retry: false
  })

  if (!discovery || !discovery.hub_available || !discovery.exists) {
    return null
  }

  const otherApps = discovery.tenants.filter((tenant) => tenant.app !== 'notifuse')
  if (otherApps.length === 0) {
    return null
  }

  const hubUrl = mode?.hub_url || DEFAULT_HUB_URL

  return (
    <div
      data-testid="veridian-cross-app-cards"
      style={{ marginTop: '24px' }}
    >
      <Typography.Title level={5} style={{ marginBottom: '12px' }}>
        {t`Your other Veridian apps`}
      </Typography.Title>
      <Space direction="vertical" size="small" style={{ width: '100%' }}>
        {otherApps.map((tenant) => {
          const meta = APP_LABELS[tenant.app] || { label: tenant.app, emoji: '🔗' }
          return (
            <Card
              key={tenant.app}
              size="small"
              data-testid={`veridian-cross-app-card-${tenant.app}`}
            >
              <div
                style={{
                  display: 'flex',
                  alignItems: 'center',
                  justifyContent: 'space-between',
                  gap: '12px'
                }}
              >
                <Space size="small">
                  <span style={{ fontSize: '20px' }} aria-hidden="true">
                    {meta.emoji}
                  </span>
                  <div>
                    <div style={{ fontWeight: 500 }}>{meta.label}</div>
                    <Typography.Text type="secondary" style={{ fontSize: '11px' }}>
                      {tenant.role}
                    </Typography.Text>
                  </div>
                </Space>
                <Button
                  type="link"
                  size="small"
                  onClick={() => window.open(hubUrl, '_blank', 'noreferrer')}
                >
                  {t`Open`}
                </Button>
              </div>
            </Card>
          )
        })}
      </Space>
    </div>
  )
}
