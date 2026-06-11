/**
 * Veridian — affichage LECTURE SEULE des débits par classe de provider posés
 * sur un broadcast (broadcast.metadata.veridian_provider_class_rates).
 *
 * But : le sender doit VOIR avec quels débits une campagne cold va partir,
 * sans avoir à inspecter le JSON. N'affiche rien si aucun rate n'est posé
 * (broadcast classique → composant transparent, zéro bruit visuel).
 *
 * La config s'édite au niveau workspace (Settings → Cold outreach) ou par API
 * sur le metadata du broadcast ; ici c'est purement informatif.
 */

import { Alert, Tag } from 'antd'
import { useLingui } from '@lingui/react/macro'
import {
  VERIDIAN_PROVIDER_CLASSES,
  VeridianProviderClass
} from '../../services/api/workspace'

interface Props {
  metadata?: Record<string, unknown>
}

const RATES_KEY = 'veridian_provider_class_rates'

function classLabel(c: VeridianProviderClass): string {
  switch (c) {
    case 'google':
      return 'Google'
    case 'microsoft':
      return 'Microsoft'
    case 'yahoo_aol':
      return 'Yahoo/AOL'
    case 'freemail_fr':
      return 'FAI FR'
    case 'corporate':
      return 'Corporate'
  }
}

// Extrait la map {classe: rate>0} du metadata, en ignorant tout ce qui n'est
// pas une classe canonique avec un débit numérique strictement positif.
export function parseBroadcastRates(
  metadata?: Record<string, unknown>
): Partial<Record<VeridianProviderClass, number>> {
  const out: Partial<Record<VeridianProviderClass, number>> = {}
  if (!metadata) return out
  const raw = metadata[RATES_KEY]
  if (!raw || typeof raw !== 'object') return out
  const m = raw as Record<string, unknown>
  for (const c of VERIDIAN_PROVIDER_CLASSES) {
    const v = m[c]
    if (typeof v === 'number' && v > 0) out[c] = v
  }
  return out
}

export function VeridianBroadcastRatesInfo({ metadata }: Props) {
  const { t } = useLingui()
  const rates = parseBroadcastRates(metadata)
  const classes = VERIDIAN_PROVIDER_CLASSES.filter((c) => rates[c] !== undefined)
  if (classes.length === 0) return null

  return (
    <Alert
      type="info"
      showIcon
      className="!mb-4"
      message={t`Per-provider sending rates (Veridian cold outreach)`}
      description={
        <div style={{ display: 'flex', gap: 8, flexWrap: 'wrap', marginTop: 4 }}>
          {classes.map((c) => (
            <Tag key={c} color="blue">
              {classLabel(c)}: {t`${rates[c]} emails/min`}
            </Tag>
          ))}
        </div>
      }
    />
  )
}
