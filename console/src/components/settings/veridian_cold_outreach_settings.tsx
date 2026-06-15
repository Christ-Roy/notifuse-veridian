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
import { App, Alert, Button, Card, Col, Divider, Form, Input, InputNumber, Row, Select, Space, Switch, Tag, Tooltip, Typography } from 'antd'
import { CheckCircleFilled, ClockCircleOutlined, CloseCircleFilled, InfoCircleOutlined, InboxOutlined, LinkOutlined, TeamOutlined } from '@ant-design/icons'
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

interface SendingWindowCardProps {
  workspace: Workspace
  isOwner: boolean
  onWorkspaceUpdate: (workspace: Workspace) => void
}

function SendingWindowCard({ workspace, isOwner, onWorkspaceUpdate }: SendingWindowCardProps) {
  const { t } = useLingui()
  const { message } = App.useApp()
  const [saving, setSaving] = useState(false)

  const existing = workspace.settings.veridian_sending_window
  const initialEnabled = !!existing
  const [enabled, setEnabled] = useState(initialEnabled)
  const [days, setDays] = useState<number[]>(existing?.days ?? [1, 2, 3, 4, 5])
  const [startMin, setStartMin] = useState<number>(
    existing ? (existing.start_hour ?? 0) * 60 + (existing.start_minute ?? 0) : 9 * 60
  )
  const [endMin, setEndMin] = useState<number>(
    existing ? (existing.end_hour ?? 0) * 60 + (existing.end_minute ?? 0) : 18 * 60
  )
  const [timezone, setTimezone] = useState<string>(
    existing?.timezone || workspace.settings.timezone || 'Europe/Paris'
  )

  // Re-sync si le workspace change (sauvegarde aboutie, switch d'onglet…).
  useEffect(() => {
    const w = workspace.settings.veridian_sending_window
    setEnabled(!!w)
    setDays(w?.days ?? [1, 2, 3, 4, 5])
    setStartMin(w ? (w.start_hour ?? 0) * 60 + (w.start_minute ?? 0) : 9 * 60)
    setEndMin(w ? (w.end_hour ?? 0) * 60 + (w.end_minute ?? 0) : 18 * 60)
    setTimezone(w?.timezone || workspace.settings.timezone || 'Europe/Paris')
    // eslint-disable-next-line react-hooks/exhaustive-deps -- re-sync sur la fenêtre persistée
  }, [workspace.id, JSON.stringify(workspace.settings.veridian_sending_window)])

  const rangeInvalid = endMin <= startMin

  const handleSave = async () => {
    if (enabled && rangeInvalid) {
      message.error(t`The closing time must be after the opening time.`)
      return
    }
    setSaving(true)
    try {
      // Désactivé → on retire la fenêtre (undefined = pas de clé = 24/7 backend).
      // Activé → on construit le shape Go (end_hour exclusif ; 24:00 = end_hour 24).
      const window: VeridianSendingWindow | undefined = enabled
        ? {
            days,
            start_hour: Math.floor(startMin / 60),
            start_minute: startMin % 60,
            end_hour: Math.floor(endMin / 60),
            end_minute: endMin % 60,
            timezone
          }
        : undefined

      await workspaceService.update({
        ...workspace,
        settings: {
          ...workspace.settings,
          veridian_sending_window: window
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
        <Space>
          <Switch
            checked={enabled}
            onChange={setEnabled}
            checkedChildren={t`On`}
            unCheckedChildren={t`Off`}
            aria-label={t`Enable sending window`}
          />
          <Text type="secondary" style={{ fontSize: 12 }}>
            {enabled ? t`Restrict to the window below` : t`Sending 24/7`}
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
                  onChange={setDays}
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
                  onChange={setTimezone}
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
                  onChange={setStartMin}
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
                  onChange={setEndMin}
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

        <Button type="primary" loading={saving} onClick={handleSave} disabled={enabled && rangeInvalid}>
          {t`Save sending window`}
        </Button>
      </Space>
    </Card>
  )
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
            <SendingWindowCard
              workspace={workspace}
              isOwner={isOwner}
              onWorkspaceUpdate={onWorkspaceUpdate}
            />
            <Divider />
          </>
        )}

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
          <SendingWindowCard
            workspace={workspace}
            isOwner={isOwner}
            onWorkspaceUpdate={onWorkspaceUpdate}
          />
          <Divider />
        </>
      )}

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
