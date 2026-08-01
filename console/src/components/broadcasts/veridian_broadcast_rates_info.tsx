/**
 * Veridian — affichage LECTURE SEULE de la config cold outreach posée sur un
 * broadcast (broadcast.metadata) : débits par classe de provider
 * (veridian_provider_class_rates) + politique du pixel d'ouverture par classe
 * (veridian_open_pixel_by_class) quand elle est explicitement définie.
 *
 * But : le sender doit VOIR avec quels débits / quelle politique pixel une
 * campagne cold va partir, sans inspecter le JSON. N'affiche rien si rien
 * n'est posé (broadcast classique → composant transparent, zéro bruit visuel).
 *
 * La config s'édite au niveau workspace (Settings → Cold outreach) ou par API
 * sur le metadata du broadcast ; ici c'est purement informatif.
 */

import { Alert, Space, Tag, Typography } from 'antd'
import { useLingui } from '@lingui/react/macro'
import {
  VERIDIAN_PROVIDER_CLASSES,
  VeridianProviderClass
} from '../../services/api/workspace'
import { parseBroadcastPixels, parseBroadcastRates } from './veridian_broadcast_metadata'

const { Text } = Typography

interface Props {
  metadata?: Record<string, unknown>
}

function classLabel(c: VeridianProviderClass): string {
  switch (c) {
    case 'google':
      return 'Google'
    case 'microsoft':
      return 'Microsoft'
    case 'yahoo_aol':
      return 'Yahoo/AOL'
    case 'freemail_fr':
      return 'FR ISPs'
    case 'corporate':
      return 'Corporate'
    case 'ovh':
      return 'OVH'
    case 'ionos':
      return 'IONOS'
    case 'apple_icloud':
      return 'Apple iCloud'
    case 'security_gateway':
      return 'Anti-spam GW'
    case 'other_hoster':
      return 'Other hosters'
    case 'corporate_selfhost':
      return 'Corporate self-host'
  }
}

export function VeridianBroadcastRatesInfo({ metadata }: Props) {
  const { t } = useLingui()
  const rates = parseBroadcastRates(metadata)
  const pixels = parseBroadcastPixels(metadata)
  const rateClasses = VERIDIAN_PROVIDER_CLASSES.filter((c) => rates[c] !== undefined)
  const pixelClasses = VERIDIAN_PROVIDER_CLASSES.filter((c) => pixels[c] !== undefined)

  if (rateClasses.length === 0 && pixelClasses.length === 0) return null

  return (
    <Alert
      type="info"
      showIcon
      className="!mb-4"
      message={t`Veridian cold outreach — per-provider settings for this campaign`}
      description={
        <Space direction="vertical" size={8} style={{ width: '100%', marginTop: 4 }}>
          {rateClasses.length > 0 && (
            <div>
              <Text type="secondary" style={{ fontSize: 12, marginRight: 8 }}>
                {t`Sending rate`}
              </Text>
              <Space size={[4, 4]} wrap>
                {rateClasses.map((c) => (
                  <Tag key={c} color="blue">
                    {classLabel(c)}: {t`${rates[c]} emails/min`}
                  </Tag>
                ))}
              </Space>
            </div>
          )}
          {pixelClasses.length > 0 && (
            <div>
              <Text type="secondary" style={{ fontSize: 12, marginRight: 8 }}>
                {t`Open pixel`}
              </Text>
              <Space size={[4, 4]} wrap>
                {pixelClasses.map((c) => (
                  <Tag key={c} color={pixels[c] ? 'green' : 'default'}>
                    {classLabel(c)}: {pixels[c] ? t`On` : t`Off`}
                  </Tag>
                ))}
              </Space>
            </div>
          )}
        </Space>
      }
    />
  )
}
