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
 * `POST /api/workspaces.update` (aucun endpoint dédié — settings JSON). Le shape
 * envoyé est strictement celui que le backend sait lire (non-régression).
 *
 * Non-owner = lecture seule (cartes statiques), owner = formulaire éditable.
 */

import { useEffect, useState } from 'react'
import { App, Alert, Button, Card, Col, Form, InputNumber, Row, Space, Switch, Tag, Typography } from 'antd'
import { CheckCircleFilled, CloseCircleFilled, InfoCircleOutlined } from '@ant-design/icons'
import { useLingui } from '@lingui/react/macro'
import { Workspace } from '../../services/api/types'
import { workspaceService } from '../../services/api/workspace'
import {
  VERIDIAN_DEFAULT_OPEN_PIXEL,
  VERIDIAN_PROVIDER_CLASSES,
  VeridianProviderClass
} from '../../services/api/workspace'
import { SettingsSectionHeader } from './SettingsSectionHeader'

const { Text, Paragraph } = Typography

interface Props {
  workspace: Workspace | null
  onWorkspaceUpdate: (workspace: Workspace) => void
  isOwner: boolean
}

// Les "gros" providers (réputation sensible au pixel d'ouverture).
const BIG_PROVIDERS: VeridianProviderClass[] = ['google', 'microsoft']

// Libellé lisible d'une classe (les values restent canoniques côté code/API).
//
// ⚠️ Libellés LITTÉRAUX (pas `t`...``) : ce sont des noms propres de providers
// (Google = Google partout) qui ne se traduisent pas. Surtout, passer un `t`
// en paramètre d'une fonction HORS composant casse l'extracteur statique Lingui
// (il ne capte pas les `t`...`` ainsi placés) → clés absentes du catalogue →
// `t` renvoie VIDE en runtime. Bug P0 vécu en prod 2026-06-14 (5 cartes de
// classe sans nom dans Settings → Cold outreach). Littéral = zéro dépendance
// i18n = zéro risque de vide.
function classLabel(c: VeridianProviderClass): string {
  switch (c) {
    case 'google':
      return 'Google (Gmail / Workspace)'
    case 'microsoft':
      return 'Microsoft (Outlook / Microsoft 365)'
    case 'yahoo_aol':
      return 'Yahoo / AOL'
    case 'freemail_fr':
      return 'French ISPs (Orange, SFR, Free…)'
    case 'corporate':
      return 'Corporate (any other domain)'
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
      message.success(t`Cold outreach settings saved`)
    } catch (error: unknown) {
      const errorMessage = (error as Error)?.message || t`Failed to save cold outreach settings`
      message.error(errorMessage)
    } finally {
      setSaving(false)
    }
  }

  // Remet le pixel de chaque classe sur le défaut tunnel (sans toucher aux rates),
  // marque le formulaire comme modifié pour que l'admin puisse sauver.
  const resetPixelsToDefault = () => {
    const values: Record<string, boolean> = {}
    for (const c of VERIDIAN_PROVIDER_CLASSES) {
      values[pixelField(c)] = VERIDIAN_DEFAULT_OPEN_PIXEL[c]
    }
    form.setFieldsValue(values)
    setTouched(true)
  }

  // Encart pédagogique : ce que l'admin doit comprendre en 30 secondes.
  const explainer = (
    <Alert
      type="info"
      showIcon
      icon={<InfoCircleOutlined />}
      className="!mb-6"
      message={t`How cold outreach throttling works`}
      description={
        <Paragraph style={{ marginBottom: 0 }} type="secondary">
          {t`Recipients are grouped by their mailbox provider. Each provider tolerates a different sending pace, so you can cap how many emails per minute go to each group — slower toward Google and Microsoft, faster toward smaller providers. The open-tracking pixel is kept OFF on Google/Microsoft by default because it can hurt your sender reputation there; it stays ON for smaller providers. Link click tracking is never affected — it stays ON everywhere.`}
        </Paragraph>
      }
    />
  )

  // ── Non-owner : lecture seule ────────────────────────────────────────────
  if (!isOwner) {
    return (
      <>
        <SettingsSectionHeader
          title={t`Veridian — Cold outreach`}
          description={t`Per-provider sending rates and open-tracking policy for outbound campaigns.`}
        />
        {explainer}
        <Space direction="vertical" size="middle" style={{ width: '100%' }}>
          {VERIDIAN_PROVIDER_CLASSES.map((c) => {
            const rate = rates?.[c]
            const pixelOn = effectivePixel(pixels, c)
            return (
              <Card key={c} size="small">
                <Row gutter={[16, 12]} align="middle" wrap>
                  <Col xs={24} sm={10}>
                    <Text strong>{classLabel(c)}</Text>
                  </Col>
                  <Col xs={12} sm={7}>
                    <Text type="secondary" style={{ fontSize: 12 }}>
                      {t`Rate`}
                    </Text>
                    <div>
                      {rate ? (
                        <Text>{t`${rate} emails/min`}</Text>
                      ) : (
                        <Text type="secondary">{t`No throttle`}</Text>
                      )}
                    </div>
                  </Col>
                  <Col xs={12} sm={7}>
                    <Text type="secondary" style={{ fontSize: 12 }}>
                      {t`Open pixel`}
                    </Text>
                    <div>
                      {pixelOn ? (
                        <Text style={{ color: '#52c41a' }}>
                          <CheckCircleFilled style={{ marginRight: 6 }} />
                          {t`On`}
                        </Text>
                      ) : (
                        <Text type="secondary">
                          <CloseCircleFilled style={{ marginRight: 6 }} />
                          {t`Off`}
                        </Text>
                      )}
                    </div>
                  </Col>
                </Row>
              </Card>
            )
          })}
        </Space>
      </>
    )
  }

  // ── Owner : formulaire éditable ──────────────────────────────────────────
  return (
    <>
      <SettingsSectionHeader
        title={t`Veridian — Cold outreach`}
        description={t`Per-provider sending rates and open-tracking policy for outbound campaigns.`}
      />
      {explainer}

      <Form form={form} layout="vertical" onFinish={handleSave} onValuesChange={() => setTouched(true)}>
        <Space direction="vertical" size="middle" style={{ width: '100%' }}>
          {VERIDIAN_PROVIDER_CLASSES.map((c) => {
            const isBig = BIG_PROVIDERS.includes(c)
            return (
              <Card key={c} size="small">
                <Row gutter={[24, 8]} align="bottom" wrap>
                  <Col xs={24} sm={10} style={{ display: 'flex', alignItems: 'center' }}>
                    <Space size="small" wrap>
                      <Text strong>{classLabel(c)}</Text>
                      {isBig && <Tag color="orange">{t`pixel OFF by default`}</Tag>}
                    </Space>
                  </Col>
                  <Col xs={14} sm={8}>
                    <Form.Item
                      name={rateField(c)}
                      label={t`Rate (emails/min)`}
                      style={{ marginBottom: 0 }}
                      tooltip={t`Maximum emails per minute toward this provider. Fractions allowed (0.5 = one email every 2 minutes). Leave empty for no per-class throttle.`}
                    >
                      <InputNumber
                        min={0}
                        step={0.5}
                        placeholder={t`No throttle`}
                        style={{ width: '100%' }}
                      />
                    </Form.Item>
                  </Col>
                  <Col xs={10} sm={6}>
                    <Form.Item
                      name={pixelField(c)}
                      label={t`Open pixel`}
                      valuePropName="checked"
                      style={{ marginBottom: 0 }}
                      tooltip={t`Open-tracking pixel for this provider class. Default: ON for smaller providers, OFF for Google/Microsoft to protect deliverability.`}
                    >
                      <Switch
                        checkedChildren={t`On`}
                        unCheckedChildren={t`Off`}
                      />
                    </Form.Item>
                  </Col>
                </Row>
              </Card>
            )
          })}
        </Space>

        <Paragraph type="secondary" style={{ fontSize: 12, marginTop: 16 }}>
          {t`These defaults apply when a campaign does not set its own per-class rates. A campaign can still override them from its own settings.`}
        </Paragraph>

        <Space style={{ marginTop: 16 }}>
          <Button type="primary" htmlType="submit" loading={saving} disabled={!touched}>
            {t`Save changes`}
          </Button>
          <Button type="text" onClick={resetPixelsToDefault} disabled={saving}>
            {t`Reset pixel to defaults`}
          </Button>
        </Space>
      </Form>
    </>
  )
}
