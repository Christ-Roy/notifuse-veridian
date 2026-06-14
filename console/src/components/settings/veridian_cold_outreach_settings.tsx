/**
 * Veridian — section "Cold outreach" de Settings.
 *
 * Administre la config du tunnel de vente outbound sans curl :
 *   1. Débits par classe de provider destinataire (emails/MINUTE, fractions OK)
 *      → workspace settings `veridian_provider_class_rates` (fallback quand un
 *      broadcast ne pose pas ses propres rates dans metadata). Borne la VITESSE.
 *   2. Plafond JOURNALIER par classe (emails/JOUR MAX)
 *      → workspace settings `veridian_provider_class_daily_cap`. Borne le VOLUME
 *      du jour. Distinct du débit minute : on peut vouloir "1 mail/jour" vers
 *      Google (inexprimable en /min). Le plus restrictif des deux gagne.
 *   3. Plafond JOURNALIER par destinataire (anti-harcèlement, global)
 *      → workspace settings `veridian_per_recipient_daily_cap`. Max d'emails vers
 *      une MÊME adresse par jour. 0 = illimité.
 *   4. Politique du pixel d'ouverture (email.opened) par classe
 *      → workspace settings `veridian_open_pixel_by_class`. Défaut tunnel :
 *      ON freemail_fr/yahoo_aol/corporate, OFF google/microsoft (réputation).
 *
 * En contexte, chaque carte affiche le NOMBRE de contacts de cette classe dans
 * le workspace (endpoint R1 /api/veridian/contacts.providerBreakdown), pour
 * dimensionner le throttle en connaissance de cause ("5000 Google à 1/jour =
 * 5000 jours").
 *
 * Le backend (internal/domain/veridian_provider_class.go + veridian_daily_cap.go
 * + veridian_open_pixel.go) reste la source de vérité ; cette UI lit/écrit via
 * `POST /api/workspaces.update` (aucun endpoint dédié — settings JSON). Le shape
 * envoyé est strictement celui que le backend sait lire (non-régression).
 *
 * Non-owner = lecture seule (cartes statiques), owner = formulaire éditable.
 */

import { useEffect, useState } from 'react'
import { App, Alert, Button, Card, Col, Form, InputNumber, Row, Space, Switch, Tag, Tooltip, Typography } from 'antd'
import { CheckCircleFilled, CloseCircleFilled, InfoCircleOutlined, TeamOutlined } from '@ant-design/icons'
import { useQuery } from '@tanstack/react-query'
import { useLingui } from '@lingui/react/macro'
import { Workspace } from '../../services/api/types'
import { workspaceService } from '../../services/api/workspace'
import {
  VERIDIAN_DEFAULT_OPEN_PIXEL,
  VERIDIAN_PROVIDER_CLASSES,
  VeridianProviderClass
} from '../../services/api/workspace'
import { contactsApi } from '../../services/api/contacts'
import { SettingsSectionHeader } from './SettingsSectionHeader'

const { Text, Paragraph } = Typography

interface Props {
  workspace: Workspace | null
  onWorkspaceUpdate: (workspace: Workspace) => void
  isOwner: boolean
}

// Les "gros" / sensibles providers (réputation sensible au pixel d'ouverture).
// security_gateway = passerelle anti-spam pro qui scrute les pixels → traitée
// comme un gros provider pour l'avertissement UI.
const BIG_PROVIDERS: VeridianProviderClass[] = ['google', 'microsoft', 'security_gateway']

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
      return 'Corporate (unknown by suffix)'
    case 'ovh':
      return 'OVH (MX-resolved)'
    case 'ionos':
      return 'IONOS / 1&1 (MX-resolved)'
    case 'apple_icloud':
      return 'Apple iCloud (MX-resolved)'
    case 'security_gateway':
      return 'Anti-spam gateway (Vade, Mailinblack, Proofpoint…)'
    case 'other_hoster':
      return 'Other hosters (Infomaniak, Gandi, Zoho…)'
    case 'corporate_selfhost':
      return 'Corporate self-hosted (MX unknown)'
  }
}

// Form field names plats : rate_<classe>, cap_<classe>, pixel_<classe>.
const rateField = (c: VeridianProviderClass) => `rate_${c}`
const capField = (c: VeridianProviderClass) => `cap_${c}`
const pixelField = (c: VeridianProviderClass) => `pixel_${c}`
const PER_RECIPIENT_FIELD = 'per_recipient_daily_cap'

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
  const dailyCaps = workspace?.settings.veridian_provider_class_daily_cap
  const perRecipientCap = workspace?.settings.veridian_per_recipient_daily_cap
  const pixels = workspace?.settings.veridian_open_pixel_by_class

  // Nombre de contacts par classe dans le workspace (R1) — contexte pour régler
  // les débits/caps. Lecture seule, ne bloque jamais l'écran si l'appel échoue.
  const { data: breakdown, isLoading: breakdownLoading } = useQuery({
    queryKey: ['veridian-provider-breakdown', workspace?.id],
    queryFn: () =>
      workspace?.id ? contactsApi.providerBreakdown({ workspace_id: workspace.id }) : null,
    enabled: !!workspace?.id,
    staleTime: 60_000
  })

  // Compte de contacts pour une classe (undefined si breakdown pas encore chargé).
  const contactCount = (c: VeridianProviderClass): number | undefined => breakdown?.breakdown?.[c]

  useEffect(() => {
    if (!isOwner) return
    const values: Record<string, number | boolean | undefined> = {}
    for (const c of VERIDIAN_PROVIDER_CLASSES) {
      values[rateField(c)] = rates?.[c]
      values[capField(c)] = dailyCaps?.[c]
      values[pixelField(c)] = effectivePixel(pixels, c)
    }
    values[PER_RECIPIENT_FIELD] = perRecipientCap
    form.setFieldsValue(values)
    setTouched(false)
  }, [workspace, form, isOwner, rates, dailyCaps, perRecipientCap, pixels])

  const handleSave = async (values: Record<string, number | boolean | undefined>) => {
    if (!workspace) return
    setSaving(true)
    try {
      // Rates : on ne garde que les débits strictement positifs (un champ vide
      // = pas de throttle pour cette classe, on l'omet). Map vide → undefined
      // (pas de clé) pour rester en non-régression côté backend.
      const nextRates: Partial<Record<VeridianProviderClass, number>> = {}
      const nextCaps: Partial<Record<VeridianProviderClass, number>> = {}
      const nextPixels: Partial<Record<VeridianProviderClass, boolean>> = {}
      for (const c of VERIDIAN_PROVIDER_CLASSES) {
        const r = values[rateField(c)]
        if (typeof r === 'number' && r > 0) nextRates[c] = r
        // Cap journalier : entier strictement positif. 0 ou vide = pas de
        // plafond journalier pour cette classe (on l'omet).
        const cap = values[capField(c)]
        if (typeof cap === 'number' && cap > 0) nextCaps[c] = Math.floor(cap)
        const p = values[pixelField(c)]
        if (typeof p === 'boolean') nextPixels[c] = p
      }

      // Cap par destinataire : entier ≥ 1. 0/vide = illimité → on omet le champ
      // (omitempty côté Go = 0 = illimité, comportement identique).
      const recip = values[PER_RECIPIENT_FIELD]
      const nextRecipient =
        typeof recip === 'number' && recip > 0 ? Math.floor(recip) : undefined

      await workspaceService.update({
        ...workspace,
        settings: {
          ...workspace.settings,
          veridian_provider_class_rates:
            Object.keys(nextRates).length > 0
              ? (nextRates as Record<VeridianProviderClass, number>)
              : undefined,
          veridian_provider_class_daily_cap:
            Object.keys(nextCaps).length > 0
              ? (nextCaps as Record<VeridianProviderClass, number>)
              : undefined,
          veridian_per_recipient_daily_cap: nextRecipient,
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

  // Petit badge "X contacts" affiché sur chaque carte de classe.
  const contactBadge = (c: VeridianProviderClass) => {
    if (breakdownLoading) return null
    const n = contactCount(c)
    if (n === undefined) return null
    return (
      <Tag icon={<TeamOutlined />} color="blue" style={{ marginInlineEnd: 0 }}>
        {t`${n} contacts`}
      </Tag>
    )
  }

  // Encart pédagogique : ce que l'admin doit comprendre en 30 secondes. Insiste
  // sur la différence VITESSE (rate/min) vs VOLUME (cap/jour), demandée par Robert.
  const explainer = (
    <Alert
      type="info"
      showIcon
      icon={<InfoCircleOutlined />}
      className="!mb-6"
      message={t`How cold outreach throttling works`}
      description={
        <Paragraph style={{ marginBottom: 0 }} type="secondary">
          {t`Recipients are grouped by their mailbox provider. Two independent limits protect your reputation: the per-minute rate caps the SPEED of sending toward each group, while the daily cap limits the TOTAL VOLUME sent to that group in a single day (e.g. "1 email/day toward Google" — impossible to express as a per-minute rate). Both apply together, and the stricter one wins. The per-recipient daily cap (top of this section) limits how many emails a single address can receive per day to avoid harassing the same contact. The open-tracking pixel is kept OFF on Google/Microsoft by default because it can hurt your sender reputation there; it stays ON for smaller providers. Link click tracking is never affected — it stays ON everywhere.`}
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
          description={t`Per-provider sending rates, daily caps and open-tracking policy for outbound campaigns.`}
        />
        {explainer}

        <Card size="small" className="!mb-4">
          <Row gutter={[16, 8]} align="middle" wrap>
            <Col xs={24} sm={14}>
              <Text strong>{t`Per-recipient daily cap`}</Text>
              <div>
                <Text type="secondary" style={{ fontSize: 12 }}>
                  {t`Max emails toward a single address per day (anti-harassment).`}
                </Text>
              </div>
            </Col>
            <Col xs={24} sm={10}>
              {perRecipientCap && perRecipientCap > 0 ? (
                <Text>{t`${perRecipientCap} / day`}</Text>
              ) : (
                <Text type="secondary">{t`Unlimited`}</Text>
              )}
            </Col>
          </Row>
        </Card>

        <Space direction="vertical" size="middle" style={{ width: '100%' }}>
          {VERIDIAN_PROVIDER_CLASSES.map((c) => {
            const rate = rates?.[c]
            const cap = dailyCaps?.[c]
            const pixelOn = effectivePixel(pixels, c)
            return (
              <Card key={c} size="small">
                <Row gutter={[16, 12]} align="middle" wrap>
                  <Col xs={24} sm={8}>
                    <Space size="small" wrap>
                      <Text strong>{classLabel(c)}</Text>
                      {contactBadge(c)}
                    </Space>
                  </Col>
                  <Col xs={8} sm={5}>
                    <Text type="secondary" style={{ fontSize: 12 }}>
                      {t`Rate`}
                    </Text>
                    <div>
                      {rate ? (
                        <Text>{t`${rate} / min`}</Text>
                      ) : (
                        <Text type="secondary">{t`No throttle`}</Text>
                      )}
                    </div>
                  </Col>
                  <Col xs={8} sm={5}>
                    <Text type="secondary" style={{ fontSize: 12 }}>
                      {t`Daily cap`}
                    </Text>
                    <div>
                      {cap ? (
                        <Text>{t`${cap} / day`}</Text>
                      ) : (
                        <Text type="secondary">{t`No cap`}</Text>
                      )}
                    </div>
                  </Col>
                  <Col xs={8} sm={6}>
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
        description={t`Per-provider sending rates, daily caps and open-tracking policy for outbound campaigns.`}
      />
      {explainer}

      <Form form={form} layout="vertical" onFinish={handleSave} onValuesChange={() => setTouched(true)}>
        {/* Cap global par destinataire (anti-harcèlement) — en tête, hors carte de classe. */}
        <Card size="small" className="!mb-4">
          <Row gutter={[24, 8]} align="bottom" wrap>
            <Col xs={24} sm={14} style={{ display: 'flex', alignItems: 'center' }}>
              <div>
                <Text strong>{t`Per-recipient daily cap`}</Text>
                <div>
                  <Text type="secondary" style={{ fontSize: 12 }}>
                    {t`Max emails toward a single address per day — protects a contact from being harassed across campaigns. Leave empty (0) for unlimited.`}
                  </Text>
                </div>
              </div>
            </Col>
            <Col xs={24} sm={10}>
              <Form.Item
                name={PER_RECIPIENT_FIELD}
                label={t`Emails / recipient / day`}
                style={{ marginBottom: 0 }}
                tooltip={t`Hard daily limit per email address, all classes combined. Counted from message history since midnight. 0 or empty = unlimited.`}
              >
                <InputNumber min={0} step={1} precision={0} placeholder={t`Unlimited`} style={{ width: '100%' }} />
              </Form.Item>
            </Col>
          </Row>
        </Card>

        <Space direction="vertical" size="middle" style={{ width: '100%' }}>
          {VERIDIAN_PROVIDER_CLASSES.map((c) => {
            const isBig = BIG_PROVIDERS.includes(c)
            return (
              <Card key={c} size="small">
                <Row gutter={[24, 8]} align="bottom" wrap>
                  <Col xs={24} sm={24} md={7} style={{ display: 'flex', alignItems: 'center' }}>
                    <Space size="small" wrap>
                      <Text strong>{classLabel(c)}</Text>
                      {contactBadge(c)}
                      {isBig && <Tag color="orange">{t`pixel OFF by default`}</Tag>}
                    </Space>
                  </Col>
                  <Col xs={12} sm={8} md={5}>
                    <Form.Item
                      name={rateField(c)}
                      label={
                        <Tooltip title={t`Speed limit: how fast emails go out toward this provider.`}>
                          <span>{t`Rate (emails/min)`}</span>
                        </Tooltip>
                      }
                      style={{ marginBottom: 0 }}
                      tooltip={t`Maximum emails per MINUTE toward this provider (SPEED). Fractions allowed (0.5 = one email every 2 minutes). Leave empty for no per-class rate.`}
                    >
                      <InputNumber min={0} step={0.5} placeholder={t`No throttle`} style={{ width: '100%' }} />
                    </Form.Item>
                  </Col>
                  <Col xs={12} sm={8} md={6}>
                    <Form.Item
                      name={capField(c)}
                      label={
                        <Tooltip title={t`Volume limit: how many emails total toward this provider in a single day.`}>
                          <span>{t`Daily cap (emails/day)`}</span>
                        </Tooltip>
                      }
                      style={{ marginBottom: 0 }}
                      tooltip={t`Maximum emails per DAY toward this provider (VOLUME), counted since midnight from message history. Use this to express "1 email/day" which a per-minute rate cannot. Leave empty for no daily cap.`}
                    >
                      <InputNumber min={0} step={1} precision={0} placeholder={t`No cap`} style={{ width: '100%' }} />
                    </Form.Item>
                  </Col>
                  <Col xs={12} sm={8} md={6}>
                    <Form.Item
                      name={pixelField(c)}
                      label={t`Open pixel`}
                      valuePropName="checked"
                      style={{ marginBottom: 0 }}
                      tooltip={t`Open-tracking pixel for this provider class. Default: ON for smaller providers, OFF for Google/Microsoft to protect deliverability.`}
                    >
                      <Switch checkedChildren={t`On`} unCheckedChildren={t`Off`} />
                    </Form.Item>
                  </Col>
                </Row>
              </Card>
            )
          })}
        </Space>

        <Paragraph type="secondary" style={{ fontSize: 12, marginTop: 16 }}>
          {t`These defaults apply when a campaign does not set its own per-class rates or caps. A campaign can still override them from its own settings.`}
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
