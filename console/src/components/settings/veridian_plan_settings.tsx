/**
 * Veridian — section "Plan" de Settings.
 *
 * Affichage strict conforme au pivot pricing 2026-05-21 :
 *
 *   - Pas de comparatif "Pro vs Free"
 *   - Pas de feature list grisée
 *   - Pas de compteur "il vous reste X mails"
 *   - Pas de menu "🔒 Pro" / "Upgrade pour débloquer"
 *
 * Affiche uniquement :
 *   1. Le plan actuel ("Free — unlimited access during your 15-day trial" / "Pro" / etc.)
 *   2. Le badge plan_source (lifetime/internal/manual) si pertinent
 *   3. Un CTA "Manage subscription" qui renvoie vers app.veridian.site
 *      (où Stripe Billing portal vit)
 *
 * Choix UX (à valider en session calme Robert) :
 *   - Layout simple Card + Descriptions Ant Design
 *   - Pas d'illustration custom (réservé polish session calme)
 */

import { Card, Descriptions, Button, Spin, Space, Typography } from 'antd'
import { useLingui } from '@lingui/react/macro'
import { ExportOutlined } from '@ant-design/icons'
import { useVeridianPlan } from '../../hooks/useVeridianPlan'
import { VeridianPlanSourceBadge } from './veridian_plan_source_badge'

const { Text } = Typography

interface Props {
  workspaceId: string
}

const VERIDIAN_HUB_URL = 'https://app.veridian.site/dashboard'

// ⚠️ Libellés LITTÉRAUX (pas `t`...``) : même piège Lingui que classLabel dans
// veridian_cold_outreach_settings.tsx — un `t` passé en paramètre d'une fonction
// HORS composant n'est PAS capté par l'extracteur statique → clés absentes du
// catalogue → `t` renvoie VIDE en runtime (libellé de plan vide dans
// Settings → Plan). Les noms de plan sont des noms propres Veridian (Pro =
// Pro partout) qui ne se traduisent pas ; littéral = zéro risque de vide.
function planLabel(plan: string | undefined): string {
  if (!plan || plan === 'free') return 'Free — unlimited access during your 15-day trial'
  if (plan === 'pro') return 'Pro'
  if (plan === 'business') return 'Business'
  if (plan === 'enterprise') return 'Enterprise'
  // Inconnu : retourne tel quel en minuscule + neutre, pas "Upgrade"
  return plan
}

export function VeridianPlanSettings({ workspaceId }: Props) {
  const { t } = useLingui()
  const { data, isLoading } = useVeridianPlan(workspaceId)

  return (
    <div>
      <h2 style={{ marginBottom: 24 }}>{t`Plan`}</h2>
      <Card>
        {isLoading ? (
          <div style={{ textAlign: 'center', padding: 24 }}>
            <Spin />
          </div>
        ) : (
          <>
            <Descriptions column={1} colon={false}>
              <Descriptions.Item label={t`Current plan`}>
                <Space size="small">
                  <Text strong>{planLabel(data?.plan)}</Text>
                  <VeridianPlanSourceBadge planSource={data?.plan_source} />
                </Space>
              </Descriptions.Item>
              <Descriptions.Item label={t`Status`}>
                <Text>{data?.status || t`Active`}</Text>
              </Descriptions.Item>
            </Descriptions>
            <div style={{ marginTop: 16 }}>
              <Text type="secondary" style={{ fontSize: 12 }}>
                {t`All features are unlimited on every plan. Your subscription is managed by Veridian.`}
              </Text>
            </div>
            <div style={{ marginTop: 24 }}>
              <Button
                type="primary"
                icon={<ExportOutlined />}
                onClick={() => window.open(VERIDIAN_HUB_URL, '_blank', 'noreferrer')}
              >
                {t`Manage subscription`}
              </Button>
            </div>
          </>
        )}
      </Card>
    </div>
  )
}
