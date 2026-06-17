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
import { App, Alert, Button, Card, Col, Divider, Form, Input, InputNumber, Popconfirm, Row, Select, Space, Switch, Tag, Tooltip, Typography } from 'antd'
import { CheckCircleFilled, ClockCircleOutlined, CloseCircleFilled, InfoCircleOutlined, InboxOutlined, LinkOutlined, RocketOutlined, TeamOutlined } from '@ant-design/icons'
import { useQuery } from '@tanstack/react-query'
import { useLingui } from '@lingui/react/macro'
import { Workspace, EmailProvider, IMAPSettings, Integration } from '../../services/api/types'
import { workspaceService } from '../../services/api/workspace'
import {
  VERIDIAN_DEFAULT_OPEN_PIXEL,
  VERIDIAN_PROVIDER_CLASSES,
  VeridianProviderClass,
  VeridianSendingWindow
} from '../../services/api/workspace'
import {
  VERIDIAN_SENDING_POLICY_PRESETS,
  VeridianSendingPolicyPreset
} from '../../services/cold/sending_policy_presets'
import { contactsApi } from '../../services/api/contacts'
import { SettingsSectionHeader } from './SettingsSectionHeader'

// Signal de preset propagé aux cartes à état local (SendingWindow, ExcludedClasses,
// JitterAntiHash) : à chaque clic sur « Apply » d'un preset, le parent incrémente
// `nonce` et pose le `preset` choisi. Les cartes réagissent au changement de nonce
// en pré-remplissant leur slice (sans sauver). nonce 0 = aucun preset appliqué.
interface PresetSignal {
  nonce: number
  preset: VeridianSendingPolicyPreset | null
}
const NO_PRESET_SIGNAL: PresetSignal = { nonce: 0, preset: null }

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

// Nombre de senders (adresses d'envoi) d'une infra. ≥2 = le round-robin par
// classe de provider destinataire s'active automatiquement en contexte cold
// (backend veridian_sender_rotation.go) → on l'EXPOSE pour que l'admin sache que
// la capacité = rate/min × nombre d'adresses (et ne sous-dimensionne pas).
const senderCount = (integ: Integration & { email_provider: EmailProvider }): number =>
  integ.email_provider.senders?.length ?? 0

// Form field names plats : rate_<classe>, cap_<classe>, pixel_<classe>.
const rateField = (c: VeridianProviderClass) => `rate_${c}`
const capField = (c: VeridianProviderClass) => `cap_${c}`
const pixelField = (c: VeridianProviderClass) => `pixel_${c}`
const PER_RECIPIENT_FIELD = 'per_recipient_daily_cap'
const PER_SENDER_FIELD = 'per_sender_daily_cap'

// Le pixel effectif AFFICHÉ pour une classe : valeur configurée si présente,
// sinon le défaut tunnel (placeholder, pas persisté tant qu'on ne sauve pas).
function effectivePixel(
  configured: Record<VeridianProviderClass, boolean> | undefined,
  c: VeridianProviderClass
): boolean {
  if (configured && c in configured) return configured[c]
  return VERIDIAN_DEFAULT_OPEN_PIXEL[c]
}

// ── Boîte IMAP de retour (self-service) ───────────────────────────────────────
//
// Configure la boîte IMAP que Notifuse poll lui-même pour détecter (a) les NDR
// de bounce remontés par le relai Postfix cold (Lot 2) et (b) les réponses
// humaines des prospects qui stoppent la séquence (Lot 3). C'est la brique qui
// permet à un non-dev d'activer bounce-loop + stop-on-reply SANS curl ni script.
//
// Persistée comme une intégration de type "imap" (createIntegration /
// updateIntegration). Le password n'est jamais renvoyé en clair par l'API : à
// l'édition on le laisse vide pour ne pas le changer (le backend préserve
// encrypted_password).
const DEFAULT_IMAP_POLLING_SECONDS = 120

interface IMAPInboxCardProps {
  workspace: Workspace
  isOwner: boolean
  onWorkspaceUpdate: (workspace: Workspace) => void
}

function IMAPInboxCard({ workspace, isOwner, onWorkspaceUpdate }: IMAPInboxCardProps) {
  const { t } = useLingui()
  const [form] = Form.useForm()
  const [saving, setSaving] = useState(false)
  const { message } = App.useApp()

  // Une seule boîte IMAP de retour par workspace dans notre cas d'usage cold :
  // on prend la première intégration de type "imap" si elle existe.
  const imapIntegration = workspace.integrations?.find((i) => i.type === 'imap')
  const imap = imapIntegration?.imap_settings

  useEffect(() => {
    form.setFieldsValue({
      host: imap?.host,
      port: imap?.port ?? 993,
      username: imap?.username,
      // Password jamais pré-rempli (l'API ne renvoie que encrypted_password).
      password: '',
      use_tls: imap?.use_tls ?? true,
      folder: imap?.folder || 'INBOX',
      polling_interval_seconds: imap?.polling_interval_seconds || DEFAULT_IMAP_POLLING_SECONDS
    })
    // eslint-disable-next-line react-hooks/exhaustive-deps -- re-sync sur l'intégration
  }, [imapIntegration?.id, imap?.host, imap?.username])

  const handleSave = async (values: {
    host: string
    port: number
    username: string
    password?: string
    use_tls: boolean
    folder?: string
    polling_interval_seconds?: number
  }) => {
    setSaving(true)
    try {
      const imapSettings: IMAPSettings = {
        host: values.host.trim(),
        port: values.port,
        username: values.username.trim(),
        use_tls: values.use_tls,
        folder: values.folder?.trim() || 'INBOX',
        polling_interval_seconds: values.polling_interval_seconds || DEFAULT_IMAP_POLLING_SECONDS
      }
      // Password seulement si saisi (sinon on ne le change pas à l'édition).
      if (values.password && values.password.length > 0) {
        imapSettings.password = values.password
      }

      if (imapIntegration) {
        await workspaceService.updateIntegration({
          workspace_id: workspace.id,
          integration_id: imapIntegration.id,
          name: imapIntegration.name || 'Cold reply inbox',
          imap_settings: imapSettings
        })
      } else {
        await workspaceService.createIntegration({
          workspace_id: workspace.id,
          name: 'Cold reply inbox',
          type: 'imap',
          imap_settings: imapSettings
        })
      }

      const response = await workspaceService.get(workspace.id)
      onWorkspaceUpdate(response.workspace)
      form.setFieldValue('password', '')
      message.success(t`IMAP inbox saved`)
    } catch (error: unknown) {
      const errorMessage = (error as Error)?.message || t`Failed to save IMAP inbox`
      message.error(errorMessage)
    } finally {
      setSaving(false)
    }
  }

  const header = (
    <Space>
      <InboxOutlined />
      <Text strong>{t`Reply & bounce inbox (IMAP)`}</Text>
      {imapIntegration ? (
        <Tag color="green">{t`Configured`}</Tag>
      ) : (
        <Tag color="default">{t`Not configured`}</Tag>
      )}
    </Space>
  )

  const help = (
    <Paragraph type="secondary" style={{ fontSize: 12, marginBottom: 16 }}>
      {t`Notifuse polls this mailbox to detect bounce reports (failed deliveries reported back by your SMTP relay) and human replies from prospects. Detecting a reply automatically stops the cold sequence for that contact; detecting a bounce suppresses the address. Use the IMAP credentials of the address your campaigns send from (or its dedicated return mailbox).`}
    </Paragraph>
  )

  // ── Non-owner : lecture seule ───────────────────────────────────────────────
  if (!isOwner) {
    return (
      <Card size="small" title={header} className="!mb-6">
        {help}
        {imap ? (
          <Row gutter={[16, 8]}>
            <Col xs={24} sm={12}>
              <Text type="secondary" style={{ fontSize: 12 }}>
                {t`Host`}
              </Text>
              <div>
                <Text>{`${imap.host}:${imap.port}`}</Text>
              </div>
            </Col>
            <Col xs={24} sm={12}>
              <Text type="secondary" style={{ fontSize: 12 }}>
                {t`Username`}
              </Text>
              <div>
                <Text>{imap.username}</Text>
              </div>
            </Col>
            <Col xs={12} sm={8}>
              <Text type="secondary" style={{ fontSize: 12 }}>
                {t`Folder`}
              </Text>
              <div>
                <Text>{imap.folder || 'INBOX'}</Text>
              </div>
            </Col>
            <Col xs={12} sm={8}>
              <Text type="secondary" style={{ fontSize: 12 }}>
                {t`TLS`}
              </Text>
              <div>{imap.use_tls ? <Text>{t`On`}</Text> : <Text type="secondary">{t`Off`}</Text>}</div>
            </Col>
          </Row>
        ) : (
          <Text type="secondary">{t`No IMAP inbox configured yet.`}</Text>
        )}
      </Card>
    )
  }

  // ── Owner : formulaire éditable ─────────────────────────────────────────────
  return (
    <Card size="small" title={header} className="!mb-6">
      {help}
      <Form form={form} layout="vertical" onFinish={handleSave}>
        <Row gutter={16}>
          <Col xs={24} sm={14}>
            <Form.Item
              name="host"
              label={t`IMAP host`}
              rules={[{ required: true, message: t`Host is required` }]}
            >
              <Input placeholder="imap.example.com" />
            </Form.Item>
          </Col>
          <Col xs={12} sm={5}>
            <Form.Item
              name="port"
              label={t`Port`}
              rules={[{ required: true, message: t`Port is required` }]}
            >
              <InputNumber min={1} max={65535} placeholder="993" style={{ width: '100%' }} />
            </Form.Item>
          </Col>
          <Col xs={12} sm={5}>
            <Form.Item name="use_tls" label={t`TLS`} valuePropName="checked">
              <Switch checkedChildren={t`On`} unCheckedChildren={t`Off`} />
            </Form.Item>
          </Col>
        </Row>
        <Row gutter={16}>
          <Col xs={24} sm={12}>
            <Form.Item
              name="username"
              label={t`Username`}
              rules={[{ required: true, message: t`Username is required` }]}
            >
              <Input placeholder="user@example.com" autoComplete="off" />
            </Form.Item>
          </Col>
          <Col xs={24} sm={12}>
            <Form.Item
              name="password"
              label={t`Password`}
              tooltip={t`Leave empty to keep the current password unchanged.`}
            >
              <Input.Password
                placeholder={imapIntegration ? t`Unchanged` : t`Mailbox password`}
                autoComplete="new-password"
              />
            </Form.Item>
          </Col>
        </Row>
        <Row gutter={16}>
          <Col xs={12} sm={8}>
            <Form.Item name="folder" label={t`Folder`}>
              <Input placeholder="INBOX" />
            </Form.Item>
          </Col>
          <Col xs={12} sm={8}>
            <Form.Item
              name="polling_interval_seconds"
              label={t`Polling interval (seconds)`}
              tooltip={t`How often Notifuse checks the mailbox. Minimum 30s to avoid being throttled by the IMAP server.`}
            >
              <InputNumber min={30} step={30} style={{ width: '100%' }} />
            </Form.Item>
          </Col>
        </Row>
        <Button type="primary" htmlType="submit" loading={saving}>
          {imapIntegration ? t`Save IMAP inbox` : t`Connect IMAP inbox`}
        </Button>
      </Form>
    </Card>
  )
}

// ── Custom tracking domain PAR INFRA d'envoi ──────────────────────────────────
//
// En cold, un lien de tracking (pixel d'ouverture /t/, redirect de clic /r/) sur
// un domaine DIFFÉRENT du From est un signal anti-spam. La pratique standard
// (Lemlist / Instantly) est un custom tracking domain ALIGNÉ au domaine d'envoi :
// envoi depuis agences-veridian.fr → tracking sur track.agences-veridian.fr.
// Comme l'infra d'envoi EST le domaine d'envoi (l'intégration EmailProvider porte
// les senders + le relai SMTP), le tracking domain se règle AU NIVEAU INFRA.
//
// Persisté sur EmailProvider.veridian_tracking_domain via updateIntegration. On
// renvoie le provider COMPLET (senders + rate_limit conservés) pour ne rien
// perdre — l'API attend l'EmailProvider entier.
interface TrackingDomainCardProps {
  workspace: Workspace
  isOwner: boolean
  onWorkspaceUpdate: (workspace: Workspace) => void
}

function TrackingDomainCard({ workspace, isOwner, onWorkspaceUpdate }: TrackingDomainCardProps) {
  const { t } = useLingui()
  const [savingId, setSavingId] = useState<string | null>(null)
  const [drafts, setDrafts] = useState<Record<string, string>>({})
  const { message } = App.useApp()

  const emailIntegrations = (workspace.integrations || []).filter(
    (i): i is Integration & { email_provider: EmailProvider } =>
      i.type === 'email' && !!i.email_provider
  )

  const draftFor = (i: Integration & { email_provider: EmailProvider }): string =>
    i.id in drafts ? drafts[i.id] : i.email_provider.veridian_tracking_domain || ''

  const handleSave = async (integration: Integration & { email_provider: EmailProvider }) => {
    setSavingId(integration.id)
    try {
      const trackingDomain = draftFor(integration).trim()
      // On renvoie l'EmailProvider COMPLET + le tracking domain modifié (vide =
      // supprime l'override → fallback workspace/global). Les autres champs
      // (senders, rate_limit, settings provider) sont conservés tels quels.
      const provider: EmailProvider = {
        ...integration.email_provider,
        veridian_tracking_domain: trackingDomain || undefined
      }
      await workspaceService.updateIntegration({
        workspace_id: workspace.id,
        integration_id: integration.id,
        name: integration.name,
        provider
      })
      const response = await workspaceService.get(workspace.id)
      onWorkspaceUpdate(response.workspace)
      setDrafts((d) => {
        const next = { ...d }
        delete next[integration.id]
        return next
      })
      message.success(t`Tracking domain saved`)
    } catch (error: unknown) {
      const errorMessage = (error as Error)?.message || t`Failed to save tracking domain`
      message.error(errorMessage)
    } finally {
      setSavingId(null)
    }
  }

  const header = (
    <Space>
      <LinkOutlined />
      <Text strong>{t`Custom tracking domain (per sending infrastructure)`}</Text>
    </Space>
  )

  const help = (
    <Paragraph type="secondary" style={{ fontSize: 12, marginBottom: 16 }}>
      {t`Tracking links (open pixel, click redirects) point to Notifuse by default. For cold outreach, a tracking link on a domain different from your sending domain is an anti-spam signal. Set a custom tracking domain aligned with each sending domain (e.g. send from agences-veridian.fr → track on track.agences-veridian.fr) — it must be a CNAME/A record pointing to Notifuse. Leave empty to fall back to the workspace/global endpoint.`}
    </Paragraph>
  )

  if (emailIntegrations.length === 0) {
    return (
      <Card size="small" title={header} className="!mb-6">
        {help}
        <Text type="secondary">
          {t`No sending integration configured yet. Add an email provider in Settings → Integrations first.`}
        </Text>
      </Card>
    )
  }

  return (
    <Card size="small" title={header} className="!mb-6">
      {help}
      <Space direction="vertical" size="middle" style={{ width: '100%' }}>
        {emailIntegrations.map((integration) => {
          const senderDomain = integration.email_provider.senders?.[0]?.email?.split('@')[1]
          return (
            <Row key={integration.id} gutter={[16, 8]} align="bottom" wrap>
              <Col xs={24} sm={8}>
                <Text strong>{integration.name}</Text>
                <div>
                  <Text type="secondary" style={{ fontSize: 12 }}>
                    {senderDomain ? t`Sends from @${senderDomain}` : t`No sender domain`}
                  </Text>
                </div>
              </Col>
              <Col xs={24} sm={11}>
                <Input
                  value={draftFor(integration)}
                  placeholder={
                    senderDomain ? `track.${senderDomain}` : 'track.your-sending-domain.com'
                  }
                  disabled={!isOwner}
                  onChange={(e) =>
                    setDrafts((d) => ({ ...d, [integration.id]: e.target.value }))
                  }
                />
              </Col>
              <Col xs={24} sm={5}>
                {isOwner && (
                  <Button
                    type="primary"
                    loading={savingId === integration.id}
                    onClick={() => handleSave(integration)}
                    block
                  >
                    {t`Save`}
                  </Button>
                )}
              </Col>
            </Row>
          )
        })}
      </Space>
    </Card>
  )
}

// ── Limites (rates + caps) PAR INFRA d'envoi (R2) ─────────────────────────────
//
// Les débits/plafonds réglés en haut de section sont au niveau WORKSPACE
// (fallback). En warm-up, chaque INFRA (= intégration EmailProvider : son
// host/IP/relai SMTP + senders) porte SES PROPRES débits/plafonds, qui PRIMENT
// sur le workspace (cascade backend : broadcast → infra → workspace). Une IP
// fraîche tape Google à 1/min pendant que le reste du workspace envoie plus vite.
//
// Persisté sur EmailProvider.veridian_provider_class_rates / _daily_cap /
// _per_recipient_daily_cap via updateIntegration. On renvoie le provider COMPLET
// (senders + rate_limit + tracking_domain conservés) — l'API attend l'EmailProvider
// entier, écraser un champ casserait l'envoi.

interface InfraLimitsCardProps {
  workspace: Workspace
  isOwner: boolean
  onWorkspaceUpdate: (workspace: Workspace) => void
}

// Les 5 classes "principales" (historiques, classification par suffixe) affichées
// par défaut. Les 6 classes MX (Lot 4) sont repliées derrière un toggle : on les
// règle rarement à la main, et les afficher toutes ferait un mur de 22 champs par
// infra (lourd visuellement ET au rendu).
const INFRA_PRIMARY_CLASSES: VeridianProviderClass[] = [
  'google',
  'microsoft',
  'yahoo_aol',
  'freemail_fr',
  'corporate'
]

function InfraLimitsCard({ workspace, isOwner, onWorkspaceUpdate }: InfraLimitsCardProps) {
  const { t } = useLingui()
  const { message } = App.useApp()
  const [savingId, setSavingId] = useState<string | null>(null)
  // Affiche les 11 classes (dont les 6 MX) ou seulement les 5 principales.
  const [showAllClasses, setShowAllClasses] = useState(false)
  // Brouillons d'édition par intégration : rates/caps par classe + cap destinataire
  // + cap sender + liste de classes exclues + fenêtre d'envoi + jitter + anti-hash
  // DEPUIS cette infra. Les clés présentes (`in`) signalent un override édité ;
  // pour jitter/anti-hash on stocke `null` pour distinguer "édité à undefined
  // (héritage)" de "non touché".
  const [drafts, setDrafts] = useState<
    Record<
      string,
      {
        rates: Partial<Record<VeridianProviderClass, number>>
        caps: Partial<Record<VeridianProviderClass, number>>
        perRecipient?: number
        perSender?: number
        excluded?: VeridianProviderClass[]
        window?: VeridianSendingWindow | null
        jitterPct?: number | null
        antiHashEnabled?: boolean | null
        antiHashWindowHours?: number | null
      }
    >
  >({})

  const emailIntegrations = (workspace.integrations || []).filter(
    (i): i is Integration & { email_provider: EmailProvider } =>
      i.type === 'email' && !!i.email_provider
  )

  // Valeur courante d'un champ : brouillon si édité, sinon valeur persistée.
  const rateVal = (
    integ: Integration & { email_provider: EmailProvider },
    c: VeridianProviderClass
  ): number | undefined => {
    const d = drafts[integ.id]
    if (d && c in d.rates) return d.rates[c]
    return integ.email_provider.veridian_provider_class_rates?.[c]
  }
  const capVal = (
    integ: Integration & { email_provider: EmailProvider },
    c: VeridianProviderClass
  ): number | undefined => {
    const d = drafts[integ.id]
    if (d && c in d.caps) return d.caps[c]
    return integ.email_provider.veridian_provider_class_daily_cap?.[c]
  }
  const perRecipVal = (
    integ: Integration & { email_provider: EmailProvider }
  ): number | undefined => {
    const d = drafts[integ.id]
    if (d && 'perRecipient' in d) return d.perRecipient
    return integ.email_provider.veridian_per_recipient_daily_cap
  }
  const perSenderVal = (
    integ: Integration & { email_provider: EmailProvider }
  ): number | undefined => {
    const d = drafts[integ.id]
    if (d && 'perSender' in d) return d.perSender
    return integ.email_provider.veridian_per_sender_daily_cap
  }
  const excludedVal = (
    integ: Integration & { email_provider: EmailProvider }
  ): VeridianProviderClass[] => {
    const d = drafts[integ.id]
    if (d && d.excluded !== undefined) return d.excluded
    return integ.email_provider.veridian_excluded_provider_classes || []
  }
  // Fenêtre d'envoi de l'infra : brouillon (null = désactivée) sinon persistée.
  const windowVal = (
    integ: Integration & { email_provider: EmailProvider }
  ): VeridianSendingWindow | null => {
    const d = drafts[integ.id]
    if (d && 'window' in d) return d.window ?? null
    return integ.email_provider.veridian_sending_window ?? null
  }
  // Jitter de l'infra : tri-état (undefined = héritage). `null` en draft =
  // "édité à héritage" → on retourne undefined.
  const jitterVal = (
    integ: Integration & { email_provider: EmailProvider }
  ): number | undefined => {
    const d = drafts[integ.id]
    if (d && 'jitterPct' in d) return d.jitterPct ?? undefined
    return integ.email_provider.veridian_jitter_pct
  }
  const antiHashEnabledVal = (
    integ: Integration & { email_provider: EmailProvider }
  ): boolean | undefined => {
    const d = drafts[integ.id]
    if (d && 'antiHashEnabled' in d) return d.antiHashEnabled ?? undefined
    return integ.email_provider.veridian_anti_hash_enabled
  }
  const antiHashWindowVal = (
    integ: Integration & { email_provider: EmailProvider }
  ): number | undefined => {
    const d = drafts[integ.id]
    if (d && 'antiHashWindowHours' in d) return d.antiHashWindowHours ?? undefined
    return integ.email_provider.veridian_anti_hash_window_hours
  }

  const setRate = (id: string, c: VeridianProviderClass, v: number | null) =>
    setDrafts((prev) => {
      const cur = prev[id] || { rates: {}, caps: {} }
      return { ...prev, [id]: { ...cur, rates: { ...cur.rates, [c]: v ?? 0 } } }
    })
  const setCap = (id: string, c: VeridianProviderClass, v: number | null) =>
    setDrafts((prev) => {
      const cur = prev[id] || { rates: {}, caps: {} }
      return { ...prev, [id]: { ...cur, caps: { ...cur.caps, [c]: v ?? 0 } } }
    })
  const setPerRecip = (id: string, v: number | null) =>
    setDrafts((prev) => {
      const cur = prev[id] || { rates: {}, caps: {} }
      return { ...prev, [id]: { ...cur, perRecipient: v ?? 0 } }
    })
  const setPerSender = (id: string, v: number | null) =>
    setDrafts((prev) => {
      const cur = prev[id] || { rates: {}, caps: {} }
      return { ...prev, [id]: { ...cur, perSender: v ?? 0 } }
    })
  const setExcluded = (id: string, v: VeridianProviderClass[]) =>
    setDrafts((prev) => {
      const cur = prev[id] || { rates: {}, caps: {} }
      return { ...prev, [id]: { ...cur, excluded: v } }
    })
  const setWindow = (id: string, v: VeridianSendingWindow | null) =>
    setDrafts((prev) => {
      const cur = prev[id] || { rates: {}, caps: {} }
      return { ...prev, [id]: { ...cur, window: v } }
    })
  const setJitter = (id: string, v: number | undefined) =>
    setDrafts((prev) => {
      const cur = prev[id] || { rates: {}, caps: {} }
      // `null` en draft = édité à "héritage" (≠ non touché).
      return { ...prev, [id]: { ...cur, jitterPct: v === undefined ? null : v } }
    })
  const setAntiHashEnabled = (id: string, v: boolean | undefined) =>
    setDrafts((prev) => {
      const cur = prev[id] || { rates: {}, caps: {} }
      return { ...prev, [id]: { ...cur, antiHashEnabled: v === undefined ? null : v } }
    })
  const setAntiHashWindow = (id: string, v: number | undefined) =>
    setDrafts((prev) => {
      const cur = prev[id] || { rates: {}, caps: {} }
      return { ...prev, [id]: { ...cur, antiHashWindowHours: v === undefined ? null : v } }
    })

  const handleSave = async (integration: Integration & { email_provider: EmailProvider }) => {
    // Fenêtre infra invalide (end ≤ start) → on bloque avec un message explicite.
    if (!sendingWindowRangeValid(windowVal(integration))) {
      message.error(t`The closing time must be after the opening time.`)
      return
    }
    setSavingId(integration.id)
    try {
      // On part des valeurs persistées et on applique le brouillon, en NE
      // gardant que les valeurs strictement positives (0/vide = pas de limite
      // pour cette classe → on omet la clé, non-régression backend).
      const nextRates: Partial<Record<VeridianProviderClass, number>> = {}
      const nextCaps: Partial<Record<VeridianProviderClass, number>> = {}
      for (const c of VERIDIAN_PROVIDER_CLASSES) {
        const r = rateVal(integration, c)
        if (typeof r === 'number' && r > 0) nextRates[c] = r
        const cap = capVal(integration, c)
        if (typeof cap === 'number' && cap > 0) nextCaps[c] = Math.floor(cap)
      }
      const recip = perRecipVal(integration)
      const nextRecipient = typeof recip === 'number' && recip > 0 ? Math.floor(recip) : undefined
      const senderCap = perSenderVal(integration)
      const nextSender = typeof senderCap === 'number' && senderCap > 0 ? Math.floor(senderCap) : undefined

      // Classes exclues de l'envoi depuis cette infra. Liste vide → undefined
      // (pas de clé = aucune exclusion, non-régression).
      const excludedList = excludedVal(integration)
      const nextExcluded = excludedList.length > 0 ? excludedList : undefined

      // Fenêtre d'envoi infra : null/désactivée → undefined (héritage workspace).
      const nextWindow = windowVal(integration) ?? undefined

      // Jitter infra : tri-état. undefined = pas d'override (héritage). On
      // PRÉSERVE un 0 explicite (désactivation) — c'est le piège pointeur.
      const nextJitter = jitterVal(integration)
      // Anti-hash infra : tri-état idem (undefined = héritage, false = OFF explicite).
      const nextAntiHashEnabled = antiHashEnabledVal(integration)
      // Fenêtre anti-hash : entier > 0 sinon undefined (défaut/héritage 72h).
      const ahWin = antiHashWindowVal(integration)
      const nextAntiHashWindow =
        typeof ahWin === 'number' && ahWin > 0 ? Math.floor(ahWin) : undefined

      // Provider COMPLET (senders, rate_limit, tracking_domain conservés) + les
      // limites mises à jour. Map vide → undefined (pas de clé) pour rester en
      // non-régression.
      const provider: EmailProvider = {
        ...integration.email_provider,
        veridian_provider_class_rates:
          Object.keys(nextRates).length > 0
            ? (nextRates as Record<VeridianProviderClass, number>)
            : undefined,
        veridian_provider_class_daily_cap:
          Object.keys(nextCaps).length > 0
            ? (nextCaps as Record<VeridianProviderClass, number>)
            : undefined,
        veridian_per_recipient_daily_cap: nextRecipient,
        veridian_per_sender_daily_cap: nextSender,
        veridian_excluded_provider_classes: nextExcluded,
        veridian_sending_window: nextWindow,
        veridian_jitter_pct: nextJitter,
        veridian_anti_hash_enabled: nextAntiHashEnabled,
        veridian_anti_hash_window_hours: nextAntiHashWindow
      }

      await workspaceService.updateIntegration({
        workspace_id: workspace.id,
        integration_id: integration.id,
        name: integration.name,
        provider
      })
      const response = await workspaceService.get(workspace.id)
      onWorkspaceUpdate(response.workspace)
      setDrafts((d) => {
        const next = { ...d }
        delete next[integration.id]
        return next
      })
      message.success(t`Infra limits saved`)
    } catch (error: unknown) {
      const errorMessage = (error as Error)?.message || t`Failed to save infra limits`
      message.error(errorMessage)
    } finally {
      setSavingId(null)
    }
  }

  const header = (
    <Space>
      <TeamOutlined />
      <Text strong>{t`Per-infrastructure limits (warm-up)`}</Text>
    </Space>
  )

  const help = (
    <>
      <Paragraph type="secondary" style={{ fontSize: 12, marginBottom: 8 }}>
        {t`Rates and daily caps set above apply at the workspace level (fallback). Each sending infrastructure (an email integration: its IP / SMTP relay + senders) can carry its OWN limits that override the workspace — essential during IP warm-up, where a fresh IP must crawl while the rest of the workspace runs faster. Leave a field empty for no per-class limit on that infra (the workspace value, then the campaign value, still apply).`}
      </Paragraph>
      <Paragraph type="secondary" style={{ fontSize: 12, marginBottom: 16 }}>
        {t`In cold mode, if an infra has several sending addresses, Notifuse alternates them automatically per recipient provider (round-robin) — so the effective capacity is rate/min × number of addresses. Add or remove sending addresses in Settings → Integrations.`}
      </Paragraph>
    </>
  )

  if (emailIntegrations.length === 0) {
    return (
      <Card size="small" title={header} className="!mb-6">
        {help}
        <Text type="secondary">
          {t`No sending integration configured yet. Add an email provider in Settings → Integrations first.`}
        </Text>
      </Card>
    )
  }

  const visibleClasses = showAllClasses ? VERIDIAN_PROVIDER_CLASSES : INFRA_PRIMARY_CLASSES

  return (
    <Card size="small" title={header} className="!mb-6">
      {help}
      <Space style={{ marginBottom: 12 }}>
        <Switch
          checked={showAllClasses}
          onChange={setShowAllClasses}
          size="small"
          aria-label={t`Show all provider classes`}
        />
        <Text type="secondary" style={{ fontSize: 12 }}>
          {showAllClasses
            ? t`Showing all 11 provider classes (incl. MX-resolved)`
            : t`Showing the 5 main provider classes`}
        </Text>
      </Space>
      <Space direction="vertical" size="large" style={{ width: '100%' }}>
        {emailIntegrations.map((integration) => {
          const senderDomain = integration.email_provider.senders?.[0]?.email?.split('@')[1]
          return (
            <div key={integration.id}>
              <Space size="small" wrap style={{ marginBottom: 8 }}>
                <Text strong>{integration.name}</Text>
                <Text type="secondary" style={{ fontSize: 12 }}>
                  {senderDomain ? `@${senderDomain}` : ''}
                </Text>
                {/* Visibilité round-robin (ticket parité) : ≥2 senders = rotation
                    automatique par classe en cold ; 1 sender = pas de rotation. */}
                {senderCount(integration) > 1 ? (
                  <Tag color="green" icon={<TeamOutlined />}>
                    {t`${senderCount(integration)} senders · round-robin per class`}
                  </Tag>
                ) : (
                  <Tag color="default">{t`1 sender (no rotation)`}</Tag>
                )}
              </Space>

              <Row gutter={[12, 8]} align="bottom" style={{ marginBottom: 8 }}>
                <Col xs={24} sm={12}>
                  <Text type="secondary" style={{ fontSize: 12 }}>
                    {t`Per-recipient daily cap (this infra)`}
                  </Text>
                  <InputNumber
                    min={0}
                    step={1}
                    precision={0}
                    value={perRecipVal(integration)}
                    onChange={(v) => setPerRecip(integration.id, v)}
                    placeholder={t`Unlimited`}
                    disabled={!isOwner}
                    style={{ width: '100%', marginTop: 4 }}
                    aria-label={t`Per-recipient daily cap for ${integration.name}`}
                  />
                </Col>
                <Col xs={24} sm={12}>
                  <Text type="secondary" style={{ fontSize: 12 }}>
                    {t`Per-sender daily cap (warmup, this infra)`}
                  </Text>
                  <InputNumber
                    min={0}
                    step={1}
                    precision={0}
                    value={perSenderVal(integration)}
                    onChange={(v) => setPerSender(integration.id, v)}
                    placeholder={t`No cap`}
                    disabled={!isOwner}
                    style={{ width: '100%', marginTop: 4 }}
                    aria-label={t`Per-sender daily cap for ${integration.name}`}
                  />
                </Col>
              </Row>

              <Row gutter={[12, 8]} align="bottom" style={{ marginBottom: 8 }}>
                <Col xs={24}>
                  <Text type="secondary" style={{ fontSize: 12 }}>
                    {t`Excluded provider classes (this infra)`}
                  </Text>
                  <Select
                    mode="multiple"
                    allowClear
                    value={excludedVal(integration)}
                    onChange={(v) => setExcluded(integration.id, v as VeridianProviderClass[])}
                    options={VERIDIAN_PROVIDER_CLASSES.map((c) => ({
                      value: c,
                      label: classLabel(c)
                    }))}
                    placeholder={t`Send to all (none excluded)`}
                    disabled={!isOwner}
                    style={{ width: '100%', marginTop: 4 }}
                    aria-label={t`Excluded provider classes for ${integration.name}`}
                  />
                </Col>
              </Row>

              {/* Fenêtre d'envoi PAR INFRA (ticket 2026-06-16). Prime sur la
                  fenêtre du workspace pour cette infra ; désactivée = héritage. */}
              <div style={{ marginBottom: 8 }}>
                <Text type="secondary" style={{ fontSize: 12 }}>
                  {t`Sending window (this infra)`}
                </Text>
                <div>
                  <Text type="secondary" style={{ fontSize: 11 }}>
                    {t`Overrides the workspace window for this infra (e.g. a fresh IP kept to 10am–4pm). Disabled = inherit the workspace window.`}
                  </Text>
                </div>
                <div style={{ marginTop: 8 }}>
                  <SendingWindowEditor
                    value={windowVal(integration)}
                    onChange={(w) => setWindow(integration.id, w)}
                    disabled={!isOwner}
                    fallbackTimezone={workspace.settings.timezone || 'Europe/Paris'}
                    offHint={t`Inherit the workspace window`}
                  />
                </div>
              </div>

              {/* Jitter + anti-hash PAR INFRA (ticket 2026-06-16). Tri-état :
                  héritage workspace / override / désactivé explicite. */}
              <Row gutter={[12, 8]} align="top" style={{ marginBottom: 8 }}>
                <Col xs={24} sm={12}>
                  <Text type="secondary" style={{ fontSize: 12 }}>
                    {t`Timing jitter (this infra)`}
                  </Text>
                  <div style={{ marginTop: 4 }}>
                    <JitterControl
                      value={jitterVal(integration)}
                      onChange={(v) => setJitter(integration.id, v)}
                      disabled={!isOwner}
                      inheritHint={t`Inherit workspace`}
                      idSuffix={integration.name}
                    />
                  </div>
                </Col>
                <Col xs={24} sm={12}>
                  <Text type="secondary" style={{ fontSize: 12 }}>
                    {t`Anti-hash dedup (this infra)`}
                  </Text>
                  <div style={{ marginTop: 4 }}>
                    <AntiHashControl
                      enabled={antiHashEnabledVal(integration)}
                      windowHours={antiHashWindowVal(integration)}
                      onChangeEnabled={(v) => setAntiHashEnabled(integration.id, v)}
                      onChangeWindow={(v) => setAntiHashWindow(integration.id, v)}
                      disabled={!isOwner}
                      inheritLabel={t`Inherit`}
                      idSuffix={integration.name}
                    />
                  </div>
                </Col>
              </Row>

              {/* Grille rate/cap par classe. Compacte : libellé + 2 champs. */}
              <Row gutter={[8, 4]} style={{ fontSize: 11, opacity: 0.6, marginBottom: 4 }}>
                <Col xs={12} sm={12}>
                  <Text type="secondary" style={{ fontSize: 11 }}>{t`Provider class`}</Text>
                </Col>
                <Col xs={6} sm={6}>
                  <Text type="secondary" style={{ fontSize: 11 }}>{t`Rate /min`}</Text>
                </Col>
                <Col xs={6} sm={6}>
                  <Text type="secondary" style={{ fontSize: 11 }}>{t`Cap /day`}</Text>
                </Col>
              </Row>
              {visibleClasses.map((c) => (
                <Row key={c} gutter={[8, 4]} align="middle" style={{ marginBottom: 4 }}>
                  <Col xs={12} sm={12}>
                    <Text style={{ fontSize: 12 }}>{classLabel(c)}</Text>
                  </Col>
                  <Col xs={6} sm={6}>
                    <InputNumber
                      min={0}
                      step={0.5}
                      value={rateVal(integration, c)}
                      onChange={(v) => setRate(integration.id, c, v)}
                      placeholder="—"
                      disabled={!isOwner}
                      size="small"
                      style={{ width: '100%' }}
                      aria-label={`rate ${c} ${integration.name}`}
                    />
                  </Col>
                  <Col xs={6} sm={6}>
                    <InputNumber
                      min={0}
                      step={1}
                      precision={0}
                      value={capVal(integration, c)}
                      onChange={(v) => setCap(integration.id, c, v)}
                      placeholder="—"
                      disabled={!isOwner}
                      size="small"
                      style={{ width: '100%' }}
                      aria-label={`cap ${c} ${integration.name}`}
                    />
                  </Col>
                </Row>
              ))}

              {isOwner && (
                <Button
                  type="primary"
                  size="small"
                  loading={savingId === integration.id}
                  onClick={() => handleSave(integration)}
                  style={{ marginTop: 8 }}
                >
                  {t`Save ${integration.name} limits`}
                </Button>
              )}
            </div>
          )
        })}
      </Space>
    </Card>
  )
}

// ── Fenêtre d'envoi (horaires ouvrables) PAR WORKSPACE ────────────────────────
//
// En cold, marteler une boîte à 3h du matin ou le dimanche est un signal
// anti-spam (et inutile : personne ne lit). On cantonne l'envoi aux heures
// ouvrables (ex. lun-ven 9h-18h, Europe/Paris). Hors fenêtre, le worker
// re-planifie à la prochaine ouverture (gate veridianSendingWindowGate). Aucune
// fenêtre = envoi 24/7 (non-régression).
//
// Persistée dans les settings workspace `veridian_sending_window` via le MÊME
// POST /api/workspaces.update que le reste de la section. Shape miroir du Go :
// { days:[1..5], start_hour, start_minute, end_hour (exclusif), end_minute, timezone }.

// Jours de la semaine, convention Go time.Weekday (0=dimanche … 6=samedi).
// ⚠️ Libellés LITTÉRAUX (pas `t`...``) : un `t` passé en paramètre / utilisé hors
// composant React n'est PAS capté par l'extracteur statique Lingui → clé absente
// du catalogue → libellé VIDE en runtime (bug P0 vécu 2026-06-14). Les noms de
// jours sont fixes en français pour le tunnel FR ; littéral = zéro risque de vide.
const WEEKDAY_OPTIONS: { value: number; label: string }[] = [
  { value: 1, label: 'Lundi' },
  { value: 2, label: 'Mardi' },
  { value: 3, label: 'Mercredi' },
  { value: 4, label: 'Jeudi' },
  { value: 5, label: 'Vendredi' },
  { value: 6, label: 'Samedi' },
  { value: 0, label: 'Dimanche' }
]

// Timezones cold courantes. Littéraux (identifiants IANA, jamais traduits).
const TIMEZONE_OPTIONS: string[] = [
  'Europe/Paris',
  'Europe/London',
  'Europe/Berlin',
  'Europe/Madrid',
  'America/New_York',
  'America/Los_Angeles',
  'UTC'
]

// Créneaux horaires de 00:00 à 23:30 par pas de 30 min, pour les bornes de la
// fenêtre. Représentation en minutes depuis minuit (déterministe, sans dayjs).
// La borne de fin accepte 24:00 (1440 = fin de journée, exclusif côté backend).
function minutesToHHMM(total: number): string {
  const h = Math.floor(total / 60)
  const m = total % 60
  const pad = (n: number) => (n < 10 ? `0${n}` : `${n}`)
  return `${pad(h)}:${pad(m)}`
}
const START_SLOTS: { value: number; label: string }[] = Array.from({ length: 48 }, (_, i) => {
  const total = i * 30
  return { value: total, label: minutesToHHMM(total) }
})
const END_SLOTS: { value: number; label: string }[] = [
  ...START_SLOTS.slice(1), // 00:30 … 23:30
  { value: 1440, label: '24:00' }
]

// ── Éditeur de fenêtre d'envoi RÉUTILISABLE ───────────────────────────────────
//
// Corps éditable d'une fenêtre (toggle on/off + jours + horaires + timezone),
// extrait de SendingWindowCard pour être réutilisé PAR INFRA (InfraLimitsCard)
// sans dupliquer les constantes WEEKDAY_OPTIONS / START_SLOTS / END_SLOTS ni la
// validation end > start. Composant CONTRÔLÉ : il ne possède pas l'état, le
// parent passe `value` (la fenêtre ou null = désactivée) et reçoit `onChange`.
// Le parent décide quand sauver (ce composant ne fait que de l'édition locale).
interface SendingWindowEditorProps {
  value: VeridianSendingWindow | null
  onChange: (next: VeridianSendingWindow | null) => void
  disabled?: boolean
  // Timezone de repli quand la fenêtre n'en porte pas (workspace.settings.timezone).
  fallbackTimezone: string
  // Libellé du "off" : workspace = "Sending 24/7", infra = "Inherit workspace window".
  offHint: string
}

function SendingWindowEditor({
  value,
  onChange,
  disabled,
  fallbackTimezone,
  offHint
}: SendingWindowEditorProps) {
  const { t } = useLingui()

  const enabled = !!value
  const days = value?.days ?? [1, 2, 3, 4, 5]
  const startMin = value ? (value.start_hour ?? 0) * 60 + (value.start_minute ?? 0) : 9 * 60
  const endMin = value ? (value.end_hour ?? 0) * 60 + (value.end_minute ?? 0) : 18 * 60
  const timezone = value?.timezone || fallbackTimezone || 'Europe/Paris'
  const rangeInvalid = enabled && endMin <= startMin

  // Reconstruit la fenêtre Go à partir de bornes en minutes (end_hour exclusif).
  const buildWindow = (
    d: number[],
    sMin: number,
    eMin: number,
    tz: string
  ): VeridianSendingWindow => ({
    days: d,
    start_hour: Math.floor(sMin / 60),
    start_minute: sMin % 60,
    end_hour: Math.floor(eMin / 60),
    end_minute: eMin % 60,
    timezone: tz
  })

  return (
    <Space direction="vertical" size="middle" style={{ width: '100%' }}>
      <Space>
        <Switch
          checked={enabled}
          disabled={disabled}
          onChange={(on) =>
            onChange(on ? buildWindow(days, startMin, endMin, timezone) : null)
          }
          checkedChildren={t`On`}
          unCheckedChildren={t`Off`}
          aria-label={t`Enable sending window`}
        />
        <Text type="secondary" style={{ fontSize: 12 }}>
          {enabled ? t`Restrict to the window below` : offHint}
        </Text>
      </Space>

      {enabled && (
        <>
          <Row gutter={[16, 12]} align="bottom" wrap>
            <Col xs={24} md={12}>
              <Text type="secondary" style={{ fontSize: 12 }}>
                {t`Working days`}
              </Text>
              <Select
                mode="multiple"
                value={days}
                disabled={disabled}
                onChange={(d) => onChange(buildWindow(d, startMin, endMin, timezone))}
                options={WEEKDAY_OPTIONS}
                placeholder={t`All days`}
                style={{ width: '100%', marginTop: 4 }}
                aria-label={t`Working days`}
              />
            </Col>
            <Col xs={24} md={12}>
              <Text type="secondary" style={{ fontSize: 12 }}>
                {t`Timezone`}
              </Text>
              <Select
                value={timezone}
                disabled={disabled}
                onChange={(tz) => onChange(buildWindow(days, startMin, endMin, tz))}
                options={TIMEZONE_OPTIONS.map((tz) => ({ value: tz, label: tz }))}
                showSearch
                style={{ width: '100%', marginTop: 4 }}
                aria-label={t`Timezone`}
              />
            </Col>
          </Row>
          <Row gutter={[16, 12]} align="bottom" wrap>
            <Col xs={12} md={8}>
              <Text type="secondary" style={{ fontSize: 12 }}>
                {t`Opening time`}
              </Text>
              <Select
                value={startMin}
                disabled={disabled}
                onChange={(s) => onChange(buildWindow(days, s, endMin, timezone))}
                options={START_SLOTS}
                style={{ width: '100%', marginTop: 4 }}
                aria-label={t`Opening time`}
              />
            </Col>
            <Col xs={12} md={8}>
              <Text type="secondary" style={{ fontSize: 12 }}>
                {t`Closing time`}
              </Text>
              <Select
                value={endMin}
                disabled={disabled}
                onChange={(e) => onChange(buildWindow(days, startMin, e, timezone))}
                options={END_SLOTS}
                status={rangeInvalid ? 'error' : undefined}
                style={{ width: '100%', marginTop: 4 }}
                aria-label={t`Closing time`}
              />
            </Col>
          </Row>
          {rangeInvalid && (
            <Alert
              type="error"
              showIcon
              message={t`The closing time must be after the opening time.`}
            />
          )}
        </>
      )}
    </Space>
  )
}

// Validité d'une fenêtre (pour bloquer le Save) : pas de fenêtre = OK, sinon
// end > start exigé (en minutes depuis minuit).
function sendingWindowRangeValid(w: VeridianSendingWindow | null | undefined): boolean {
  if (!w) return true
  const startMin = (w.start_hour ?? 0) * 60 + (w.start_minute ?? 0)
  const endMin = (w.end_hour ?? 0) * 60 + (w.end_minute ?? 0)
  return endMin > startMin
}

// ── Contrôle JITTER tri-état RÉUTILISABLE (workspace + infra) ──────────────────
//
// Le jitter `veridian_jitter_pct` est un *float64 côté Go : sémantique TRI-ÉTAT.
//   undefined → pas d'override (défaut cold ±30 %, ou héritage workspace en infra)
//   0         → jitter DÉSACTIVÉ explicitement (opt-out — NE PAS collapser en
//               "non configuré", sinon on retombe sur le défaut cold ON)
//   > 0       → amplitude forcée
// Un simple InputNumber confond vide↔0 ; on découple donc un Switch "override"
// (= la valeur est-elle posée ?) d'un InputNumber d'amplitude. Off + Save → la
// valeur posée est `undefined` (le parent omet la clé) ; On + amplitude → la
// valeur exacte (y compris 0 = désactivation explicite).
interface JitterControlProps {
  value: number | undefined
  onChange: (next: number | undefined) => void
  disabled?: boolean
  // Texte du "off" : workspace = "Default cold ±30%", infra = "Inherit workspace".
  inheritHint: string
  idSuffix: string
}

function JitterControl({ value, onChange, disabled, inheritHint, idSuffix }: JitterControlProps) {
  const { t } = useLingui()
  const overridden = value !== undefined
  return (
    <Space size="small" wrap align="center">
      <Switch
        checked={overridden}
        disabled={disabled}
        size="small"
        // Activer l'override → part d'une amplitude par défaut 0.30. Désactiver →
        // undefined (héritage/défaut cold).
        onChange={(on) => onChange(on ? 0.3 : undefined)}
        aria-label={t`Override jitter ${idSuffix}`}
      />
      {overridden ? (
        <InputNumber
          min={0}
          max={0.9}
          step={0.05}
          value={value}
          disabled={disabled}
          onChange={(v) => onChange(v ?? 0)}
          size="small"
          style={{ width: 90 }}
          aria-label={t`Jitter amount ${idSuffix}`}
        />
      ) : (
        <Text type="secondary" style={{ fontSize: 12 }}>
          {inheritHint}
        </Text>
      )}
    </Space>
  )
}

// ── Contrôle ANTI-HASH tri-état RÉUTILISABLE (workspace + infra) ──────────────
//
// `veridian_anti_hash_enabled` est un *bool côté Go : TRI-ÉTAT.
//   undefined → défaut cold ON (ou héritage workspace en infra)
//   true      → forcé ON
//   false     → désactivé explicite
// Un Select 3-états (Inherit / On / Off) rend la sémantique explicite ; un Switch
// 2-états collapserait undefined et false. La fenêtre (heures) n'est pertinente
// que si non désactivé.
type AntiHashTriState = 'inherit' | 'on' | 'off'

function antiHashToState(value: boolean | undefined): AntiHashTriState {
  if (value === undefined) return 'inherit'
  return value ? 'on' : 'off'
}
function antiHashFromState(state: AntiHashTriState): boolean | undefined {
  if (state === 'inherit') return undefined
  return state === 'on'
}

interface AntiHashControlProps {
  enabled: boolean | undefined
  windowHours: number | undefined
  onChangeEnabled: (next: boolean | undefined) => void
  onChangeWindow: (next: number | undefined) => void
  disabled?: boolean
  // Libellé de l'option "inherit" : workspace = "Default (cold ON 72h)", infra =
  // "Inherit workspace".
  inheritLabel: string
  idSuffix: string
}

function AntiHashControl({
  enabled,
  windowHours,
  onChangeEnabled,
  onChangeWindow,
  disabled,
  inheritLabel,
  idSuffix
}: AntiHashControlProps) {
  const { t } = useLingui()
  const state = antiHashToState(enabled)
  return (
    <Space size="small" wrap align="center">
      <Select<AntiHashTriState>
        value={state}
        disabled={disabled}
        size="small"
        style={{ width: 130 }}
        onChange={(s) => onChangeEnabled(antiHashFromState(s))}
        options={[
          { value: 'inherit', label: inheritLabel },
          { value: 'on', label: t`Forced ON` },
          { value: 'off', label: t`Forced OFF` }
        ]}
        aria-label={t`Anti-hash mode ${idSuffix}`}
      />
      {state !== 'off' && (
        <InputNumber
          min={0}
          step={1}
          precision={0}
          value={windowHours}
          disabled={disabled}
          onChange={(v) => onChangeWindow(v ?? undefined)}
          placeholder="72"
          size="small"
          style={{ width: 80 }}
          aria-label={t`Anti-hash window hours ${idSuffix}`}
        />
      )}
      <Text type="secondary" style={{ fontSize: 12 }}>
        {t`hours`}
      </Text>
    </Space>
  )
}

interface SendingWindowCardProps {
  workspace: Workspace
  isOwner: boolean
  onWorkspaceUpdate: (workspace: Workspace) => void
  // Veridian fork — signal du preset de politique : quand `nonce` change
  // (incrémenté par le PresetCard parent), la carte pré-remplit sa fenêtre aux
  // valeurs du preset choisi dans son état LOCAL, sans sauver. L'owner relit puis
  // clique « Save sending window ». nonce 0 = aucun preset appliqué.
  presetSignal?: PresetSignal
}

function SendingWindowCard({
  workspace,
  isOwner,
  onWorkspaceUpdate,
  presetSignal = NO_PRESET_SIGNAL
}: SendingWindowCardProps) {
  const { t } = useLingui()
  const { message } = App.useApp()
  const [saving, setSaving] = useState(false)

  const existing = workspace.settings.veridian_sending_window
  // État local = la fenêtre éditée (null = désactivée → 24/7).
  const [draft, setDraft] = useState<VeridianSendingWindow | null>(existing ?? null)

  // Re-sync si le workspace change (sauvegarde aboutie, switch d'onglet…).
  useEffect(() => {
    setDraft(workspace.settings.veridian_sending_window ?? null)
    // eslint-disable-next-line react-hooks/exhaustive-deps -- re-sync sur la fenêtre persistée
  }, [workspace.id, JSON.stringify(workspace.settings.veridian_sending_window)])

  // Preset : applique la fenêtre du preset choisi à l'état LOCAL (sans sauver).
  // Déclenché à chaque incrément du nonce parent (> 0 = un clic preset).
  useEffect(() => {
    if (presetSignal.nonce <= 0) return
    const w = presetSignal.preset?.sendingWindow
    if (!w) return
    setDraft({
      ...w,
      timezone: w.timezone || workspace.settings.timezone || 'Europe/Paris'
    })
    // eslint-disable-next-line react-hooks/exhaustive-deps -- déclenché uniquement par le nonce
  }, [presetSignal.nonce])

  const rangeInvalid = !sendingWindowRangeValid(draft)

  const handleSave = async () => {
    if (rangeInvalid) {
      message.error(t`The closing time must be after the opening time.`)
      return
    }
    setSaving(true)
    try {
      // Désactivé (draft null) → undefined = pas de clé = 24/7 backend.
      await workspaceService.update({
        ...workspace,
        settings: {
          ...workspace.settings,
          veridian_sending_window: draft ?? undefined
        }
      })
      const response = await workspaceService.get(workspace.id)
      onWorkspaceUpdate(response.workspace)
      message.success(t`Sending window saved`)
    } catch (error: unknown) {
      const errorMessage = (error as Error)?.message || t`Failed to save sending window`
      message.error(errorMessage)
    } finally {
      setSaving(false)
    }
  }

  const header = (
    <Space>
      <ClockCircleOutlined />
      <Text strong>{t`Sending window (business hours)`}</Text>
      {existing ? (
        <Tag color="green">{t`Active`}</Tag>
      ) : (
        <Tag color="default">{t`24/7 (no window)`}</Tag>
      )}
    </Space>
  )

  const help = (
    <Paragraph type="secondary" style={{ fontSize: 12, marginBottom: 16 }}>
      {t`Restrict cold sending to business hours. Emails scheduled outside the window are automatically deferred to the next opening — never dropped. Sending at 3am or on weekends is an anti-spam signal and nobody reads it. Leave disabled to send 24/7.`}
    </Paragraph>
  )

  // Résumé lisible de la fenêtre persistée (lecture seule + entête owner). On
  // construit la string AVANT le rendu pour ne PAS mettre d'interpolation
  // complexe dans un `t`...`` (l'extracteur Lingui n'aime pas les ternaires
  // imbriqués) : ici c'est un littéral assemblé, zéro clé i18n à risque.
  const summaryDaysLabel =
    existing && existing.days && existing.days.length > 0
      ? existing.days
          .map((d) => WEEKDAY_OPTIONS.find((o) => o.value === d)?.label || String(d))
          .join(', ')
      : 'Every day'
  const summaryText = existing
    ? `${summaryDaysLabel} · ${minutesToHHMM(
        (existing.start_hour ?? 0) * 60 + (existing.start_minute ?? 0)
      )}–${minutesToHHMM(
        (existing.end_hour ?? 0) * 60 + (existing.end_minute ?? 0)
      )} · ${existing.timezone || workspace.settings.timezone || 'Europe/Paris'}`
    : ''
  const summary = existing ? (
    <Text type="secondary" style={{ fontSize: 12 }}>
      {summaryText}
    </Text>
  ) : (
    <Text type="secondary">{t`No sending window — emails go out 24/7.`}</Text>
  )

  // ── Non-owner : lecture seule ───────────────────────────────────────────────
  if (!isOwner) {
    return (
      <Card size="small" title={header} className="!mb-6">
        {help}
        {summary}
      </Card>
    )
  }

  // ── Owner : édition ─────────────────────────────────────────────────────────
  return (
    <Card size="small" title={header} className="!mb-6">
      {help}
      <Space direction="vertical" size="middle" style={{ width: '100%' }}>
        <SendingWindowEditor
          value={draft}
          onChange={setDraft}
          fallbackTimezone={workspace.settings.timezone || 'Europe/Paris'}
          offHint={t`Sending 24/7`}
        />
        <Button type="primary" loading={saving} onClick={handleSave} disabled={rangeInvalid}>
          {t`Save sending window`}
        </Button>
      </Space>
    </Card>
  )
}

// ── Exclusion de classes de providers (cold outbound) PAR WORKSPACE ───────────
//
// Levier DÉDIÉ pour ne PAS contacter une ou plusieurs classes de provider
// destinataire. Cas concret : sur une IP/un domaine fraîchement monté, on
// n'envoie PAS à microsoft/outlook (réputation Microsoft = la plus dure à warmer)
// le temps que l'IP mûrisse, puis on l'ouvre. Les contacts d'une classe exclue
// sont SKIPPÉS proprement par le worker (pas de SMTP ouvert, pas de bounce) ; le
// reste du broadcast part normalement.
//
// ⚠️ Distinct des rates/caps : un rate ou un cap à 0 signifie "PLEINE VITESSE /
// illimité" côté backend (opt-in), PAS une exclusion. Mettre rate microsoft = 0
// envoie microsoft SANS throttle — l'inverse du but. D'où ce levier séparé.
//
// Persisté dans les settings workspace `veridian_excluded_provider_classes` via le
// MÊME POST /api/workspaces.update que la fenêtre d'envoi. Niveau le plus général
// de la cascade (broadcast → infra → WORKSPACE) ; on peut affiner par infra dans
// la carte "Per-infrastructure limits".

interface ExcludedClassesCardProps {
  workspace: Workspace
  isOwner: boolean
  onWorkspaceUpdate: (workspace: Workspace) => void
  // Nombre de contacts par classe (breakdown R1), pour avertir "X contacts seront
  // ignorés". undefined si le breakdown n'est pas (encore) chargé.
  contactCount: (c: VeridianProviderClass) => number | undefined
  // Signal du preset : pré-remplit la liste d'exclusion aux valeurs du preset.
  presetSignal?: PresetSignal
}

function ExcludedClassesCard({
  workspace,
  isOwner,
  onWorkspaceUpdate,
  contactCount,
  presetSignal = NO_PRESET_SIGNAL
}: ExcludedClassesCardProps) {
  const { t } = useLingui()
  const { message } = App.useApp()
  const [saving, setSaving] = useState(false)

  const persisted = workspace.settings.veridian_excluded_provider_classes || []
  const [excluded, setExcluded] = useState<VeridianProviderClass[]>(persisted)

  // Re-sync sur le workspace (sauvegarde aboutie, switch d'onglet…).
  useEffect(() => {
    setExcluded(workspace.settings.veridian_excluded_provider_classes || [])
    // eslint-disable-next-line react-hooks/exhaustive-deps -- re-sync sur la liste persistée
  }, [workspace.id, JSON.stringify(workspace.settings.veridian_excluded_provider_classes)])

  // Preset : applique la liste d'exclusion du preset (état local, sans sauver).
  // Le preset peut poser [] (croisière) → on l'applique aussi (réactive Microsoft).
  useEffect(() => {
    if (presetSignal.nonce <= 0) return
    const ex = presetSignal.preset?.excludedClasses
    if (ex === undefined) return
    setExcluded(ex)
    // eslint-disable-next-line react-hooks/exhaustive-deps -- déclenché uniquement par le nonce
  }, [presetSignal.nonce])

  const handleSave = async () => {
    setSaving(true)
    try {
      // Liste vide → undefined (pas de clé = aucune exclusion, non-régression).
      const nextExcluded = excluded.length > 0 ? excluded : undefined
      await workspaceService.update({
        ...workspace,
        settings: {
          ...workspace.settings,
          veridian_excluded_provider_classes: nextExcluded
        }
      })
      const response = await workspaceService.get(workspace.id)
      onWorkspaceUpdate(response.workspace)
      message.success(t`Excluded provider classes saved`)
    } catch (error: unknown) {
      const errorMessage = (error as Error)?.message || t`Failed to save excluded provider classes`
      message.error(errorMessage)
    } finally {
      setSaving(false)
    }
  }

  const header = (
    <Space>
      <CloseCircleFilled />
      <Text strong>{t`Excluded provider classes (do not contact)`}</Text>
      {persisted.length > 0 ? (
        <Tag color="red">{t`${persisted.length} excluded`}</Tag>
      ) : (
        <Tag color="default">{t`None excluded`}</Tag>
      )}
    </Space>
  )

  const help = (
    <Paragraph type="secondary" style={{ fontSize: 12, marginBottom: 16 }}>
      {t`Pick mailbox provider classes you do NOT want to contact at all. Recipients in an excluded class are skipped cleanly — no email is sent, no bounce is produced — while the rest of the campaign goes out normally. Typical use: don't touch Microsoft/Outlook from a freshly warmed-up IP (Microsoft's reputation is the hardest to earn), then open it once the IP is mature. This is different from a rate or daily cap: setting a rate/cap to 0 means "unlimited", not "excluded".`}
    </Paragraph>
  )

  // Avertissement chiffré : combien de contacts seront ignorés pour chaque classe
  // exclue (à partir du breakdown R1). Aide à mesurer l'impact avant de sauver.
  const impactRows = excluded
    .map((c) => ({ c, n: contactCount(c) }))
    .filter((r): r is { c: VeridianProviderClass; n: number } => typeof r.n === 'number' && r.n > 0)

  const impactAlert =
    impactRows.length > 0 ? (
      <Alert
        type="warning"
        showIcon
        style={{ marginTop: 12 }}
        message={t`Contacts that will be skipped`}
        description={
          <Space direction="vertical" size={2} style={{ width: '100%' }}>
            {impactRows.map((r) => (
              <Text key={r.c} style={{ fontSize: 12 }}>
                {classLabel(r.c)}: {t`${r.n} contacts will be skipped`}
              </Text>
            ))}
          </Space>
        }
      />
    ) : null

  // ── Non-owner : lecture seule ───────────────────────────────────────────────
  if (!isOwner) {
    return (
      <Card size="small" title={header} className="!mb-6">
        {help}
        {persisted.length > 0 ? (
          <Space size="small" wrap>
            {persisted.map((c) => (
              <Tag key={c} color="red">
                {classLabel(c)}
              </Tag>
            ))}
          </Space>
        ) : (
          <Text type="secondary">{t`No provider class is excluded — all recipients are contacted.`}</Text>
        )}
      </Card>
    )
  }

  // ── Owner : édition ─────────────────────────────────────────────────────────
  return (
    <Card size="small" title={header} className="!mb-6">
      {help}
      <Space direction="vertical" size="middle" style={{ width: '100%' }}>
        <div>
          <Text type="secondary" style={{ fontSize: 12 }}>
            {t`Classes to never contact`}
          </Text>
          <Select
            mode="multiple"
            allowClear
            value={excluded}
            onChange={(v) => setExcluded(v as VeridianProviderClass[])}
            options={VERIDIAN_PROVIDER_CLASSES.map((c) => ({ value: c, label: classLabel(c) }))}
            placeholder={t`Send to all (none excluded)`}
            style={{ width: '100%', marginTop: 4 }}
            aria-label={t`Excluded provider classes`}
          />
        </div>
        {impactAlert}
        <Button type="primary" loading={saving} onClick={handleSave}>
          {t`Save excluded classes`}
        </Button>
      </Space>
    </Card>
  )
}

// ── Jitter temporel + anti-hash identique PAR WORKSPACE ───────────────────────
//
// Deux leviers d'anti-détection cold, persistables workspace (et par infra dans
// la carte ci-dessus). Tous deux ont une sémantique TRI-ÉTAT côté Go (pointeurs) :
//   - Jitter (*float64) : undefined = défaut cold ±30 %, 0 = désactivé, >0 = forcé.
//   - Anti-hash (*bool) : undefined = défaut cold ON, false = OFF, true = forcé ON.
// L'UI distingue donc "non configuré" de "0/false" via des contrôles dédiés
// (JitterControl / AntiHashControl), pour ne PAS retomber sur le défaut quand
// l'admin a explicitement choisi OFF.
//
// Persistés dans `veridian_jitter_pct` / `veridian_anti_hash_enabled` /
// `veridian_anti_hash_window_hours` via le MÊME POST /api/workspaces.update.

interface JitterAntiHashCardProps {
  workspace: Workspace
  isOwner: boolean
  onWorkspaceUpdate: (workspace: Workspace) => void
  presetSignal?: PresetSignal
}

function JitterAntiHashCard({
  workspace,
  isOwner,
  onWorkspaceUpdate,
  presetSignal = NO_PRESET_SIGNAL
}: JitterAntiHashCardProps) {
  const { t } = useLingui()
  const { message } = App.useApp()
  const [saving, setSaving] = useState(false)

  // États locaux tri-état : `undefined` = non configuré (défaut cold / héritage).
  const [jitter, setJitter] = useState<number | undefined>(
    workspace.settings.veridian_jitter_pct
  )
  const [antiHash, setAntiHash] = useState<boolean | undefined>(
    workspace.settings.veridian_anti_hash_enabled
  )
  const [antiHashWindow, setAntiHashWindow] = useState<number | undefined>(
    workspace.settings.veridian_anti_hash_window_hours
  )

  // Re-sync sur le workspace (sauvegarde aboutie, switch d'onglet…).
  useEffect(() => {
    setJitter(workspace.settings.veridian_jitter_pct)
    setAntiHash(workspace.settings.veridian_anti_hash_enabled)
    setAntiHashWindow(workspace.settings.veridian_anti_hash_window_hours)
    // eslint-disable-next-line react-hooks/exhaustive-deps -- re-sync sur les valeurs persistées
  }, [
    workspace.id,
    workspace.settings.veridian_jitter_pct,
    workspace.settings.veridian_anti_hash_enabled,
    workspace.settings.veridian_anti_hash_window_hours
  ])

  // Preset : applique les valeurs jitter/anti-hash du preset (état local, sans sauver).
  useEffect(() => {
    if (presetSignal.nonce <= 0) return
    const p = presetSignal.preset
    if (!p) return
    if (p.jitterPct !== undefined) setJitter(p.jitterPct)
    if (p.antiHashEnabled !== undefined) setAntiHash(p.antiHashEnabled)
    if (p.antiHashWindowHours !== undefined) setAntiHashWindow(p.antiHashWindowHours)
    // eslint-disable-next-line react-hooks/exhaustive-deps -- déclenché uniquement par le nonce
  }, [presetSignal.nonce])

  const handleSave = async () => {
    setSaving(true)
    try {
      // Tri-état préservé : on ENVOIE jitter=0 / anti_hash=false explicitement
      // (sémantique "désactivé"), undefined = pas de clé (défaut cold). Window
      // anti-hash : entier > 0 sinon undefined (défaut 72h).
      const nextWindow =
        typeof antiHashWindow === 'number' && antiHashWindow > 0
          ? Math.floor(antiHashWindow)
          : undefined
      await workspaceService.update({
        ...workspace,
        settings: {
          ...workspace.settings,
          veridian_jitter_pct: jitter,
          veridian_anti_hash_enabled: antiHash,
          veridian_anti_hash_window_hours: nextWindow
        }
      })
      const response = await workspaceService.get(workspace.id)
      onWorkspaceUpdate(response.workspace)
      message.success(t`Anti-detection settings saved`)
    } catch (error: unknown) {
      const errorMessage = (error as Error)?.message || t`Failed to save anti-detection settings`
      message.error(errorMessage)
    } finally {
      setSaving(false)
    }
  }

  const header = (
    <Space>
      <RocketOutlined />
      <Text strong>{t`Anti-detection (jitter & content dedup)`}</Text>
    </Space>
  )

  const help = (
    <Paragraph type="secondary" style={{ fontSize: 12, marginBottom: 16 }}>
      {t`Two cold-sending anti-detection levers. Timing jitter spreads the throttle's re-scheduling delay around its nominal value to break the metronomic rhythm that filters use to spot a machine. Content dedup (anti-hash) prevents two emails with an identical rendering (subject + body) from going out to the same provider class within a sliding window. Both default to ON for cold campaigns — leave them on default unless you have a reason to override per sensitive campaign.`}
    </Paragraph>
  )

  // ── Non-owner : lecture seule ───────────────────────────────────────────────
  if (!isOwner) {
    const jitterText =
      jitter === undefined
        ? t`Default ±30%`
        : jitter === 0
          ? t`Disabled`
          : t`±${Math.round(jitter * 100)}%`
    const antiHashText =
      antiHash === undefined ? t`Default (cold ON)` : antiHash ? t`On` : t`Off`
    return (
      <Card size="small" title={header} className="!mb-6">
        {help}
        <Row gutter={[16, 8]}>
          <Col xs={24} sm={12}>
            <Text type="secondary" style={{ fontSize: 12 }}>
              {t`Timing jitter`}
            </Text>
            <div>
              <Text>{jitterText}</Text>
            </div>
          </Col>
          <Col xs={24} sm={12}>
            <Text type="secondary" style={{ fontSize: 12 }}>
              {t`Content dedup (anti-hash)`}
            </Text>
            <div>
              <Text>
                {antiHashText}
                {antiHash !== false && (
                  <Text type="secondary">
                    {' · '}
                    {antiHashWindow && antiHashWindow > 0 ? antiHashWindow : 72}
                    {t`h window`}
                  </Text>
                )}
              </Text>
            </div>
          </Col>
        </Row>
      </Card>
    )
  }

  // ── Owner : édition ─────────────────────────────────────────────────────────
  return (
    <Card size="small" title={header} className="!mb-6">
      {help}
      <Row gutter={[24, 16]} align="top">
        <Col xs={24} md={12}>
          <Text strong>{t`Timing jitter`}</Text>
          <div>
            <Text type="secondary" style={{ fontSize: 12 }}>
              {t`Override the default ±30% jitter. Off = default cold ±30%. Set to 0 to disable jitter explicitly.`}
            </Text>
          </div>
          <div style={{ marginTop: 8 }}>
            <JitterControl
              value={jitter}
              onChange={setJitter}
              inheritHint={t`Default cold ±30%`}
              idSuffix="workspace"
            />
          </div>
        </Col>
        <Col xs={24} md={12}>
          <Text strong>{t`Content dedup (anti-hash)`}</Text>
          <div>
            <Text type="secondary" style={{ fontSize: 12 }}>
              {t`Prevents two identical emails toward the same provider within the window. Default cold = ON, 72h.`}
            </Text>
          </div>
          <div style={{ marginTop: 8 }}>
            <AntiHashControl
              enabled={antiHash}
              windowHours={antiHashWindow}
              onChangeEnabled={setAntiHash}
              onChangeWindow={setAntiHashWindow}
              inheritLabel={t`Default (cold ON)`}
              idSuffix="workspace"
            />
          </div>
        </Col>
      </Row>
      <Button type="primary" loading={saving} onClick={handleSave} style={{ marginTop: 16 }}>
        {t`Save anti-detection settings`}
      </Button>
    </Card>
  )
}

export function VeridianColdOutreachSettings({ workspace, onWorkspaceUpdate, isOwner }: Props) {
  const { t } = useLingui()
  const [saving, setSaving] = useState(false)
  const [touched, setTouched] = useState(false)
  // Signal du preset appliqué : incrémenté à chaque clic « Apply », propagé aux
  // cartes à état local (SendingWindow, ExcludedClasses, JitterAntiHash) qui
  // pré-remplissent leur slice sans sauver.
  const [presetSignal, setPresetSignal] = useState<PresetSignal>(NO_PRESET_SIGNAL)
  const [form] = Form.useForm()
  const { message } = App.useApp()

  const rates = workspace?.settings.veridian_provider_class_rates
  const dailyCaps = workspace?.settings.veridian_provider_class_daily_cap
  const perRecipientCap = workspace?.settings.veridian_per_recipient_daily_cap
  const perSenderCap = workspace?.settings.veridian_per_sender_daily_cap
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
    values[PER_SENDER_FIELD] = perSenderCap
    form.setFieldsValue(values)
    setTouched(false)
  }, [workspace, form, isOwner, rates, dailyCaps, perRecipientCap, perSenderCap, pixels])

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

      // Cap par sender émetteur (warmup IP) : entier ≥ 1. 0/vide = pas de plafond.
      const sender = values[PER_SENDER_FIELD]
      const nextSender =
        typeof sender === 'number' && sender > 0 ? Math.floor(sender) : undefined

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
          veridian_per_sender_daily_cap: nextSender,
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

  // Libellé + description d'un preset. DANS le composant → `t` extrait correctement.
  const presetLabel = (id: VeridianSendingPolicyPreset['id']): string => {
    switch (id) {
      case 'warmup':
        return t`Warm-up (cautious)`
      case 'cruise':
        return t`Cruise`
      case 'microsoft_prudence':
        return t`Microsoft prudence`
    }
  }
  const presetDescription = (id: VeridianSendingPolicyPreset['id']): string => {
    switch (id) {
      case 'warmup':
        return t`Start a new IP/domain: 1 email/day per provider class, low per-mailbox daily volume, business hours only, Microsoft excluded until the IP matures. Round-robin across addresses activates automatically with ≥2 senders.`
      case 'cruise':
        return t`Mature IP/domain: comfortable rates and daily caps, wide business hours (8am–8pm), Microsoft re-enabled at a moderate rate. Jitter and content dedup stay on.`
      case 'microsoft_prudence':
        return t`Same as Cruise but Microsoft/Outlook excluded — use when Microsoft reputation is degraded or never built, while still serving everyone else.`
    }
  }

  // Applique un PRESET de politique : pré-remplit le formulaire (rates, caps,
  // per-recipient, per-sender) + signale aux cartes à état local (fenêtre,
  // exclusion, jitter/anti-hash) de pré-remplir leur slice. NE SAUVE RIEN :
  // l'owner relit puis clique « Save changes » (et « Save » sur chaque carte
  // concernée). Le pixel N'EST PAS touché (on ne dégrade pas la réputation : le
  // défaut tunnel OFF google/microsoft reste).
  const applyPreset = (preset: VeridianSendingPolicyPreset) => {
    const values: Record<string, number> = {}
    for (const c of VERIDIAN_PROVIDER_CLASSES) {
      if (preset.rates) values[rateField(c)] = preset.rates[c]
      if (preset.classDailyCap) values[capField(c)] = preset.classDailyCap[c]
    }
    if (preset.perRecipientDailyCap !== undefined)
      values[PER_RECIPIENT_FIELD] = preset.perRecipientDailyCap
    if (preset.perSenderDailyCap !== undefined)
      values[PER_SENDER_FIELD] = preset.perSenderDailyCap
    form.setFieldsValue(values)
    setTouched(true)
    // Pousse les slices fenêtre / exclusion / jitter-antihash dans leurs cartes.
    setPresetSignal((s) => ({ nonce: s.nonce + 1, preset }))
    message.info(
      t`"${presetLabel(preset.id)}" preset applied below — review the values, then click "Save changes" (and the Save button on the Sending window / Excluded classes / Anti-detection cards).`
    )
  }

  // Infras d'envoi (email) ayant < 2 senders : le round-robin entre adresses ne
  // peut pas s'activer (il faut ≥ 2 boîtes). Sert l'avertissement du PresetCard.
  const emailIntegrations = (workspace?.integrations || []).filter(
    (i): i is Integration & { email_provider: EmailProvider } =>
      i.type === 'email' && !!i.email_provider
  )
  const infrasWithoutRoundRobin = emailIntegrations.filter(
    (i) => (i.email_provider.senders?.length ?? 0) < 2
  )

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

  // Encart PRESETS (owner) : un preset = un ensemble cohérent de valeurs (rates,
  // caps, fenêtre, jitter, anti-hash, exclusion) qui encode une intention métier
  // (warm-up / croisière / prudence Microsoft). Cliquer « Apply » pré-remplit les
  // champs SANS sauver — l'owner relit puis Save. Popconfirm car ça écrase la
  // config cold courante.
  const presetCard = (
    <Card size="small" className="!mb-6">
      <Space size="small" wrap style={{ marginBottom: 4 }}>
        <RocketOutlined style={{ color: '#1677ff' }} />
        <Text strong>{t`Sending policy presets`}</Text>
      </Space>
      <div style={{ marginBottom: 12 }}>
        <Text type="secondary" style={{ fontSize: 12 }}>
          {t`A preset is a named, coherent set of cold-sending values (rates, daily caps, sending window, jitter, content dedup and provider exclusions). Applying one fills the fields below as a starting point — review and adjust them, then click "Save changes" (and the Save button on the Sending window / Excluded classes / Anti-detection cards). Nothing is saved until you do.`}
        </Text>
      </div>
      <Space direction="vertical" size="middle" style={{ width: '100%' }}>
        {VERIDIAN_SENDING_POLICY_PRESETS.map((preset) => (
          <Row key={preset.id} gutter={[16, 8]} align="middle" wrap>
            <Col xs={24} md={18}>
              <Text strong>{presetLabel(preset.id)}</Text>
              <div>
                <Text type="secondary" style={{ fontSize: 12 }}>
                  {presetDescription(preset.id)}
                </Text>
              </div>
            </Col>
            <Col xs={24} md={6} style={{ textAlign: 'right' }}>
              <Popconfirm
                title={t`Apply this preset?`}
                description={t`This overwrites your current cold outreach values with the preset values. Nothing is saved until you click Save.`}
                okText={t`Apply`}
                cancelText={t`Cancel`}
                onConfirm={() => applyPreset(preset)}
              >
                <Button icon={<RocketOutlined />} aria-label={t`Apply ${presetLabel(preset.id)} preset`}>
                  {t`Apply`}
                </Button>
              </Popconfirm>
            </Col>
          </Row>
        ))}
      </Space>
      {infrasWithoutRoundRobin.length > 0 && (
        <Alert
          type="warning"
          showIcon
          className="!mt-3"
          message={t`Round-robin needs at least 2 sending addresses`}
          description={t`Some sending integrations have a single sender, so the round-robin across addresses cannot activate for them. Add at least 2 mailboxes per sending integration in Settings → Integrations to spread the volume.`}
        />
      )}
    </Card>
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

        {workspace && (
          <>
            <IMAPInboxCard
              workspace={workspace}
              isOwner={isOwner}
              onWorkspaceUpdate={onWorkspaceUpdate}
            />
            <TrackingDomainCard
              workspace={workspace}
              isOwner={isOwner}
              onWorkspaceUpdate={onWorkspaceUpdate}
            />
            <InfraLimitsCard
              workspace={workspace}
              isOwner={isOwner}
              onWorkspaceUpdate={onWorkspaceUpdate}
            />
            <SendingWindowCard
              workspace={workspace}
              isOwner={isOwner}
              onWorkspaceUpdate={onWorkspaceUpdate}
            />
            <ExcludedClassesCard
              workspace={workspace}
              isOwner={isOwner}
              onWorkspaceUpdate={onWorkspaceUpdate}
              contactCount={contactCount}
            />
            <JitterAntiHashCard
              workspace={workspace}
              isOwner={isOwner}
              onWorkspaceUpdate={onWorkspaceUpdate}
            />
            <Divider />
          </>
        )}

        <Card size="small" className="!mb-4">
          <Row gutter={[16, 8]} align="middle" wrap>
            <Col xs={24} sm={12}>
              <Text strong>{t`Per-recipient daily cap`}</Text>
              <div>
                <Text type="secondary" style={{ fontSize: 12 }}>
                  {t`Max emails toward a single address per day (anti-harassment).`}
                </Text>
              </div>
              {perRecipientCap && perRecipientCap > 0 ? (
                <Text>{t`${perRecipientCap} / day`}</Text>
              ) : (
                <Text type="secondary">{t`Unlimited`}</Text>
              )}
            </Col>
            <Col xs={24} sm={12}>
              <Text strong>{t`Per-sender daily cap (warmup)`}</Text>
              <div>
                <Text type="secondary" style={{ fontSize: 12 }}>
                  {t`Max emails sent FROM a single address per day (IP warmup).`}
                </Text>
              </div>
              {perSenderCap && perSenderCap > 0 ? (
                <Text>{t`${perSenderCap} / day`}</Text>
              ) : (
                <Text type="secondary">{t`No cap`}</Text>
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
      {presetCard}

      {workspace && (
        <>
          <IMAPInboxCard
            workspace={workspace}
            isOwner={isOwner}
            onWorkspaceUpdate={onWorkspaceUpdate}
          />
          <TrackingDomainCard
            workspace={workspace}
            isOwner={isOwner}
            onWorkspaceUpdate={onWorkspaceUpdate}
          />
          <InfraLimitsCard
            workspace={workspace}
            isOwner={isOwner}
            onWorkspaceUpdate={onWorkspaceUpdate}
          />
          <SendingWindowCard
            workspace={workspace}
            isOwner={isOwner}
            onWorkspaceUpdate={onWorkspaceUpdate}
            presetSignal={presetSignal}
          />
          <ExcludedClassesCard
            workspace={workspace}
            isOwner={isOwner}
            onWorkspaceUpdate={onWorkspaceUpdate}
            contactCount={contactCount}
            presetSignal={presetSignal}
          />
          <JitterAntiHashCard
            workspace={workspace}
            isOwner={isOwner}
            onWorkspaceUpdate={onWorkspaceUpdate}
            presetSignal={presetSignal}
          />
          <Divider />
        </>
      )}

      <Form form={form} layout="vertical" onFinish={handleSave} onValuesChange={() => setTouched(true)}>
        {/* Caps globaux — en tête, hors carte de classe. Destinataire (anti-
            harcèlement) + émetteur (warmup IP), deux dimensions indépendantes. */}
        <Card size="small" className="!mb-4">
          <Row gutter={[24, 12]} align="bottom" wrap>
            <Col xs={24} md={12}>
              <div style={{ marginBottom: 8 }}>
                <Text strong>{t`Per-recipient daily cap`}</Text>
                <div>
                  <Text type="secondary" style={{ fontSize: 12 }}>
                    {t`Max emails toward a single address per day — protects a contact from being harassed across campaigns. Leave empty (0) for unlimited.`}
                  </Text>
                </div>
              </div>
              <Form.Item
                name={PER_RECIPIENT_FIELD}
                label={t`Emails / recipient / day`}
                style={{ marginBottom: 0 }}
                tooltip={t`Hard daily limit per email address, all classes combined. Counted from message history since midnight. 0 or empty = unlimited.`}
              >
                <InputNumber min={0} step={1} precision={0} placeholder={t`Unlimited`} style={{ width: '100%' }} />
              </Form.Item>
            </Col>
            <Col xs={24} md={12}>
              <div style={{ marginBottom: 8 }}>
                <Text strong>{t`Per-sender daily cap (warmup)`}</Text>
                <div>
                  <Text type="secondary" style={{ fontSize: 12 }}>
                    {t`Max emails sent FROM a single sending address per day — the classic IP/domain warmup limit. Each mailbox ramps its own volume. Leave empty (0) for no sender limit.`}
                  </Text>
                </div>
              </div>
              <Form.Item
                name={PER_SENDER_FIELD}
                label={t`Emails / sender / day`}
                style={{ marginBottom: 0 }}
                tooltip={t`Hard daily limit per SENDING address (FROM), counted from message history since midnight (V53). Distinct from recipient caps: this caps how much each mailbox emits, for IP/domain warmup. 0 or empty = no sender cap. Can also be set per sending integration in the infra card above.`}
              >
                <InputNumber min={0} step={1} precision={0} placeholder={t`No cap`} style={{ width: '100%' }} />
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
