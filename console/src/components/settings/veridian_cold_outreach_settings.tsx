/**
 * Veridian — section "Cold outreach" de Settings.
 *
 * Administre la config du tunnel de vente outbound sans curl :
 *   1. Débits par classe de provider destinataire (emails/minute, fractions OK)
 *      → workspace settings `veridian_provider_class_rates` (fallback quand un
 *      broadcast ne pose pas ses propres rates dans metadata).
 *   2. Politique du pixel d'ouverture (email.opened) par classe
 *      → workspace settings `veridian_open_pixel_by_class`. Défaut tunnel :
 *      ON freemail_fr/yahoo_aol/corporate, OFF google/microsoft (réputation).
 *
 * Le backend (internal/domain/veridian_provider_class.go +
 * veridian_open_pixel.go) reste la source de vérité ; cette UI lit/écrit via
 * `POST /api/workspaces.update` (aucun endpoint dédié — settings JSON).
 *
 * Non-owner = lecture seule (Descriptions), owner = formulaire éditable.
 */

import { useEffect, useState } from 'react'
import { App, Button, Descriptions, Form, InputNumber, Switch, Tag, Tooltip, Typography } from 'antd'
import { CheckCircleOutlined, CloseCircleOutlined } from '@ant-design/icons'
import { useLingui } from '@lingui/react/macro'
import { Workspace } from '../../services/api/types'
import { workspaceService } from '../../services/api/workspace'
import {
  VERIDIAN_DEFAULT_OPEN_PIXEL,
  VERIDIAN_PROVIDER_CLASSES,
  VeridianProviderClass
} from '../../services/api/workspace'
import { SettingsSectionHeader } from './SettingsSectionHeader'

const { Text } = Typography

interface Props {
  workspace: Workspace | null
  onWorkspaceUpdate: (workspace: Workspace) => void
  isOwner: boolean
}

// Libellé lisible d'une classe (les values restent canoniques côté code/API).
function classLabel(c: VeridianProviderClass): string {
  switch (c) {
    case 'google':
      return 'Google (Gmail / Workspace)'
    case 'microsoft':
      return 'Microsoft (Outlook / M365)'
    case 'yahoo_aol':
      return 'Yahoo / AOL'
    case 'freemail_fr':
      return 'FAI français (Orange, SFR, Free…)'
    case 'corporate':
      return 'Corporate (autres domaines)'
  }
}

// Form field names plats : rate_<classe> et pixel_<classe>.
const rateField = (c: VeridianProviderClass) => `rate_${c}`
const pixelField = (c: VeridianProviderClass) => `pixel_${c}`

// Le pixel effectif AFFICHÉ pour une classe : valeur configurée si présente,
// sinon le défaut tunnel (placeholder, pas persisté tant qu'on ne sauve pas).
function effectivePixel(
  configured: Record<VeridianProviderClass, boolean> | undefined,
  c: VeridianProviderClass
): boolean {
  if (configured && c in configured) return configured[c]
  return VERIDIAN_DEFAULT_OPEN_PIXEL[c]
}

export function VeridianColdOutreachSettings({ workspace, onWorkspaceUpdate, isOwner }: Props) {
  const { t } = useLingui()
  const [saving, setSaving] = useState(false)
  const [touched, setTouched] = useState(false)
  const [form] = Form.useForm()
  const { message } = App.useApp()

  const rates = workspace?.settings.veridian_provider_class_rates
  const pixels = workspace?.settings.veridian_open_pixel_by_class

  useEffect(() => {
    if (!isOwner) return
    const values: Record<string, number | boolean | undefined> = {}
    for (const c of VERIDIAN_PROVIDER_CLASSES) {
      values[rateField(c)] = rates?.[c]
      values[pixelField(c)] = effectivePixel(pixels, c)
    }
    form.setFieldsValue(values)
    setTouched(false)
  }, [workspace, form, isOwner, rates, pixels])

  const handleSave = async (values: Record<string, number | boolean | undefined>) => {
    if (!workspace) return
    setSaving(true)
    try {
      // Rates : on ne garde que les débits strictement positifs (un champ vide
      // = pas de throttle pour cette classe, on l'omet). Map vide → undefined
      // (pas de clé) pour rester en non-régression côté backend.
      const nextRates: Partial<Record<VeridianProviderClass, number>> = {}
      const nextPixels: Partial<Record<VeridianProviderClass, boolean>> = {}
      for (const c of VERIDIAN_PROVIDER_CLASSES) {
        const r = values[rateField(c)]
        if (typeof r === 'number' && r > 0) nextRates[c] = r
        const p = values[pixelField(c)]
        if (typeof p === 'boolean') nextPixels[c] = p
      }

      await workspaceService.update({
        ...workspace,
        settings: {
          ...workspace.settings,
          veridian_provider_class_rates:
            Object.keys(nextRates).length > 0
              ? (nextRates as Record<VeridianProviderClass, number>)
              : undefined,
          veridian_open_pixel_by_class:
            Object.keys(nextPixels).length > 0
              ? (nextPixels as Record<VeridianProviderClass, boolean>)
              : undefined
        }
      })

      const response = await workspaceService.get(workspace.id)
      onWorkspaceUpdate(response.workspace)
      setTouched(false)
      message.success(t`Cold outreach settings updated successfully`)
    } catch (error: unknown) {
      const errorMessage = (error as Error)?.message || t`Failed to update cold outreach settings`
      message.error(errorMessage)
    } finally {
      setSaving(false)
    }
  }

  // ── Non-owner : lecture seule ────────────────────────────────────────────
  if (!isOwner) {
    return (
      <>
        <SettingsSectionHeader
          title={t`Veridian — Cold outreach`}
          description={t`Per-provider sending rates and open-tracking policy for outbound campaigns.`}
        />
        <Descriptions
          bordered
          column={1}
          size="small"
          styles={{ label: { width: '280px', fontWeight: '500' } }}
        >
          {VERIDIAN_PROVIDER_CLASSES.map((c) => {
            const rate = rates?.[c]
            const pixelOn = effectivePixel(pixels, c)
            return (
              <Descriptions.Item key={c} label={classLabel(c)}>
                <span>
                  {rate
                    ? t`${rate} emails/min`
                    : t`No throttle (uses sender rate limit)`}
                </span>
                <span style={{ marginLeft: 16 }}>
                  {pixelOn ? (
                    <Text style={{ color: '#52c41a' }}>
                      <CheckCircleOutlined style={{ marginRight: 6 }} />
                      {t`Open pixel ON`}
                    </Text>
                  ) : (
                    <Text style={{ color: '#ff4d4f' }}>
                      <CloseCircleOutlined style={{ marginRight: 6 }} />
                      {t`Open pixel OFF`}
                    </Text>
                  )}
                </span>
              </Descriptions.Item>
            )
          })}
        </Descriptions>
      </>
    )
  }

  // ── Owner : formulaire éditable ──────────────────────────────────────────
  return (
    <>
      <SettingsSectionHeader
        title={t`Veridian — Cold outreach`}
        description={t`Per-provider sending rates and open-tracking policy for outbound campaigns. Defaults keep the open pixel OFF on Google/Microsoft to protect deliverability.`}
      />

      <Form form={form} layout="vertical" onFinish={handleSave} onValuesChange={() => setTouched(true)}>
        <Descriptions
          bordered
          column={1}
          size="small"
          styles={{ label: { width: '280px', fontWeight: '500', verticalAlign: 'top' } }}
        >
          {VERIDIAN_PROVIDER_CLASSES.map((c) => {
            const isBig = c === 'google' || c === 'microsoft'
            return (
              <Descriptions.Item
                key={c}
                label={
                  <span>
                    {classLabel(c)}
                    {isBig && (
                      <Tag color="orange" style={{ marginLeft: 8 }}>
                        {t`pixel OFF by default`}
                      </Tag>
                    )}
                  </span>
                }
              >
                <div style={{ display: 'flex', gap: 24, alignItems: 'center', flexWrap: 'wrap' }}>
                  <Form.Item
                    name={rateField(c)}
                    label={t`Rate (emails/min)`}
                    style={{ marginBottom: 0 }}
                    tooltip={t`Max emails per minute toward this provider. Fractions allowed (0.5 = 1 email / 2 min). Empty = no per-class throttle.`}
                  >
                    <InputNumber min={0} step={0.5} placeholder={t`no throttle`} style={{ width: 140 }} />
                  </Form.Item>
                  <Form.Item
                    name={pixelField(c)}
                    label={
                      <Tooltip
                        title={t`Open tracking pixel for this provider class. Default: ON for small providers, OFF for Google/Microsoft.`}
                      >
                        {t`Open pixel`}
                      </Tooltip>
                    }
                    valuePropName="checked"
                    style={{ marginBottom: 0 }}
                  >
                    <Switch />
                  </Form.Item>
                </div>
              </Descriptions.Item>
            )
          })}
        </Descriptions>

        <div style={{ marginTop: 16 }}>
          <Text type="secondary" style={{ fontSize: 12 }}>
            {t`These defaults apply when a campaign does not set its own per-class rates. Link click tracking stays ON for every class regardless of the open pixel.`}
          </Text>
        </div>

        <Form.Item style={{ marginTop: 24 }}>
          <Button type="primary" htmlType="submit" loading={saving} disabled={!touched}>
            {t`Save Changes`}
          </Button>
        </Form.Item>
      </Form>
    </>
  )
}
