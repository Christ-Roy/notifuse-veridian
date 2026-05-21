/**
 * Veridian — badge plan_source dans Settings → Plan.
 *
 * Affiche un badge contextuel selon l'origine du plan tenant (cf.
 * `domain.PlanSource` côté Go) :
 *
 *   - `lifetime_partner`        → "Lifetime — accès offert par Veridian" (vert)
 *   - `lifetime_site_vitrine`   → "Lifetime — inclus avec votre site vitrine" (bleu)
 *   - `internal`                → "Compte interne Veridian" (gris) — Robert / équipe
 *   - `manual`                  → "Plan admin manuel" (orange)
 *   - `stripe` / vide           → pas de badge custom (Stripe = standard)
 *
 * Pivot pricing 2026-05-21 : ne JAMAIS afficher de comparatif "Pro vs Free"
 * ni de feature list grisée. Le badge est juste un marqueur informatif.
 *
 * Choix UX (à valider en session calme avec Robert) :
 *   - Couleurs Ant Design `<Tag color="...">` standard, pas de palette
 *     custom Veridian
 *   - Badge "internal" visible pour tous — Robert pourra restreindre via
 *     email pattern en session calme s'il préfère
 */

import { Tag } from 'antd'
import { useLingui } from '@lingui/react/macro'
import type { PlanSource } from '../../services/api/veridian_plan'

interface Props {
  planSource: PlanSource | undefined | null
}

export function VeridianPlanSourceBadge({ planSource }: Props) {
  const { t } = useLingui()

  if (!planSource || planSource === 'stripe') {
    return null
  }

  switch (planSource) {
    case 'lifetime_partner':
      return <Tag color="green">{t`Lifetime — access offered by Veridian`}</Tag>
    case 'lifetime_site_vitrine':
      return <Tag color="blue">{t`Lifetime — included with your Veridian website`}</Tag>
    case 'internal':
      return <Tag color="default">{t`Internal Veridian account`}</Tag>
    case 'manual':
      return <Tag color="orange">{t`Manual admin plan`}</Tag>
    default:
      return null
  }
}
