import React, { useState, useEffect, useMemo, useCallback } from 'react'
import { useLingui } from '@lingui/react/macro'
import { Drawer, Typography, Skeleton, Alert, Tabs, Tag, Space, Descriptions, Segmented, Select, Button, List } from 'antd'
import { SafetyCertificateOutlined, ReloadOutlined } from '@ant-design/icons'
import type { Template, MjmlCompileError, Workspace } from '../../services/api/types'
import { templatesApi } from '../../services/api/template'
import type { CompileTemplateRequest } from '../../services/api/template'
import type { EmailBlock } from '../email_builder/types'
import { Highlight, themes } from 'prism-react-renderer'
import type { MessageHistory } from '../../services/api/messages_history'
import { SUPPORTED_LANGUAGES } from '../../lib/languages'
import { deliverabilityApi } from '../../services/api/deliverability'
import type {
  VeridianDeliverabilityResult,
  VeridianDeliverabilityMode
} from '../../services/api/deliverability'
import {
  VERIDIAN_PROVIDER_CLASSES,
  type VeridianProviderClass
} from '../../services/api/workspace'

const { Text } = Typography

interface TemplatePreviewDrawerProps {
  record: Template
  workspace: Workspace
  templateData?: Record<string, unknown>
  messageHistory?: MessageHistory
  children: React.ReactNode
}

const TemplatePreviewDrawer: React.FC<TemplatePreviewDrawerProps> = ({
  record,
  workspace,
  templateData,
  messageHistory,
  children
}) => {
  const { t } = useLingui()
  const [previewHtml, setPreviewHtml] = useState<string | null>(null)
  const [previewMjml, setPreviewMjml] = useState<string | null>(null)
  const [isLoading, setIsLoading] = useState<boolean>(false)
  const [error, setError] = useState<string | null>(null)
  const [mjmlError, setMjmlError] = useState<MjmlCompileError | null>(null)
  const [isOpen, setIsOpen] = useState<boolean>(false)
  const [activeTabKey, setActiveTabKey] = useState<string>('1') // State for active tab
  const [renderedSubject, setRenderedSubject] = useState<string | null>(null)
  const [renderedSubjectPreview, setRenderedSubjectPreview] = useState<string | null>(null)
  const [selectedLanguage, setSelectedLanguage] = useState<string | null>(null)
  // Effective template data returned by the compile endpoint (includes the workspace
  // object the server injects). Displayed in the Template Data tab so it matches the render.
  const [effectiveTestData, setEffectiveTestData] = useState<Record<string, unknown> | null>(null)

  const availableLanguages = useMemo(() => {
    if (messageHistory) return []
    const defaultLang = workspace.settings?.default_language || 'en'
    const langs: { label: string; value: string }[] = [
      { label: SUPPORTED_LANGUAGES[defaultLang] || defaultLang, value: defaultLang }
    ]
    if (record.translations) {
      for (const [code, translation] of Object.entries(record.translations)) {
        if (
          code !== defaultLang &&
          translation.email &&
          (translation.email.visual_editor_tree || translation.email.mjml_source)
        ) {
          langs.push({ label: SUPPORTED_LANGUAGES[code] || code, value: code })
        }
      }
    }
    return langs
  }, [record.translations, workspace.settings?.default_language, messageHistory])

  const showLanguageSelector = availableLanguages.length > 1
  const effectiveLanguage = selectedLanguage || workspace.settings?.default_language || 'en'

  const effectiveEmail = useMemo(() => {
    const defaultLang = workspace.settings?.default_language || 'en'
    if (effectiveLanguage === defaultLang) return record.email
    return record.translations?.[effectiveLanguage]?.email || record.email
  }, [effectiveLanguage, record.email, record.translations, workspace.settings?.default_language])

  const fetchPreview = async () => {
    const isCodeMode = effectiveEmail?.editor_mode === 'code'

    if (!workspace.id || (!isCodeMode && !effectiveEmail?.visual_editor_tree) || (isCodeMode && !effectiveEmail?.mjml_source)) {
      setError(t`Missing workspace ID or template data.`)
      setMjmlError(null)
      setPreviewMjml(null)
      setPreviewHtml(null)
      return
    }

    setIsLoading(true)
    setError(null)
    setMjmlError(null)
    setPreviewHtml(null)
    setPreviewMjml(null)
    setRenderedSubject(null)
    setRenderedSubjectPreview(null)
    setEffectiveTestData(null)
    setActiveTabKey('1') // Reset to HTML tab on new fetch

    try {
      // Build compile request based on editor mode.
      // Subject and subject_preview are sent so the server can render them with
      // the same Liquid engine used at send time, keeping preview and send in sync.
      const req: Partial<CompileTemplateRequest> = {
        workspace_id: workspace.id,
        message_id: 'preview',
        subject: effectiveEmail?.subject,
        subject_preview: effectiveEmail?.subject_preview,
        test_data: templateData || record.test_data || {},
        tracking_settings: {
          enable_tracking: workspace.settings?.email_tracking_enabled || false,
          endpoint: workspace.settings?.custom_endpoint_url || undefined,
          workspace_id: workspace.id,
          message_id: 'preview'
        }
      }

      if (isCodeMode) {
        // Code mode: use mjml_source directly
        req.mjml_source = effectiveEmail!.mjml_source
      } else {
        // Visual mode: parse visual_editor_tree
        let treeObject: EmailBlock | null = null
        if (effectiveEmail?.visual_editor_tree && typeof effectiveEmail.visual_editor_tree === 'string') {
          try {
            treeObject = JSON.parse(effectiveEmail.visual_editor_tree)
          } catch (parseError) {
            console.error('Failed to parse visual_editor_tree:', parseError)
            setError(t`Invalid template structure data.`)
            setMjmlError(null)
            setPreviewMjml(null)
            setIsLoading(false)
            return
          }
        } else if (effectiveEmail?.visual_editor_tree) {
          treeObject = effectiveEmail.visual_editor_tree as unknown as EmailBlock
        }

        if (!treeObject) {
          setError(t`Template structure data is missing or invalid.`)
          setMjmlError(null)
          setPreviewMjml(null)
          setIsLoading(false)
          return
        }

        req.visual_editor_tree = treeObject
      }

      // console.log('Compile Request:', req)
      const response = await templatesApi.compile(req as CompileTemplateRequest)
      // console.log('Compile Response:', response)

      // Server returns rendered subject/subject_preview on both success and
      // MJML-error paths, so update them either way before branching.
      setRenderedSubject(response.subject ?? null)
      setRenderedSubjectPreview(response.subject_preview ?? null)
      // The server echoes back the effective template data (with the injected
      // workspace object) so the Template Data tab matches what was rendered.
      setEffectiveTestData(response.test_data ?? null)

      if (response.error) {
        setMjmlError(response.error)
        setPreviewMjml(response.mjml)
        setError(null)
        setPreviewHtml(null)
      } else {
        setPreviewHtml(response.html)
        setPreviewMjml(response.mjml)
        setError(null)
        setMjmlError(null)
      }
    } catch (err) {
      console.error('Compile Error:', err)
      const error = err as { response?: { data?: { error?: string } }; message?: string }
      const errorMsg =
        error.response?.data?.error || error.message || t`Failed to compile template preview.`
      setError(errorMsg)
      setMjmlError(null)
      setPreviewMjml(null)
    } finally {
      setIsLoading(false)
    }
  }

  useEffect(() => {
    if (isOpen && workspace.id) {
      fetchPreview()
    } else if (!isOpen) {
      // Reset state when drawer closes to avoid showing stale data briefly on reopen
      setPreviewHtml(null)
      setPreviewMjml(null)
      setError(null)
      setMjmlError(null)
      setIsLoading(false)
      setActiveTabKey('1')
      setRenderedSubject(null)
      setRenderedSubjectPreview(null)
      setEffectiveTestData(null)
      setSelectedLanguage(null)
    }
  // eslint-disable-next-line react-hooks/exhaustive-deps -- fetchPreview is stable
  }, [isOpen, record.id, record.version, workspace.id, effectiveLanguage])

  const items = []

  if (previewHtml) {
    items.push({
      key: '1',
      label: t`HTML Preview`,
      children: (
        <iframe
          srcDoc={previewHtml}
          className="w-full h-full border-0"
          style={{ height: '600px', width: '100%' }}
          title={t`HTML Preview of ${record.name}`}
          sandbox=""
        />
      )
    })
  }

  if (previewMjml) {
    items.push({
      key: '2',
      label: t`MJML Source`,
      children: <MJMLPreview previewMjml={previewMjml} />
    })
  }

  // Add Template Data tab regardless of preview status. Prefer the effective data the
  // server rendered with (includes the injected workspace object); fall back to the
  // local data before the first compile resolves.
  const testData = effectiveTestData || templateData || record.test_data || {}
  items.push({
    key: '3',
    label: t`Template Data`,
    children: <JsonDataViewer data={testData} />
  })

  const emailProvider = workspace.integrations?.find(
    (i) =>
      i.id ===
      (record.category === 'marketing'
        ? workspace.settings?.marketing_email_provider_id
        : workspace.settings?.transactional_email_provider_id)
  )?.email_provider

  const defaultSender = emailProvider?.senders.find((s) => s.is_default)
  const templateSender = emailProvider?.senders.find((s) => s.id === record.email?.sender_id)

  // Veridian — onglet "Délivrabilité" : linte le HTML RENDU (spam score) avant
  // envoi. Disponible dès qu'un HTML est compilé. from_domain dérivé du sender
  // (alignement tracking) pour la règle TRACKING_DOMAIN_MISMATCH.
  const fromEmail = templateSender?.email || defaultSender?.email
  const fromDomain = fromEmail?.split('@')[1]
  if (previewHtml) {
    items.push({
      key: '4',
      label: t`Deliverability`,
      children: (
        <DeliverabilityPanel
          workspaceId={workspace.id}
          html={previewHtml}
          subject={renderedSubject ?? effectiveEmail?.subject ?? ''}
          fromDomain={fromDomain}
        />
      )
    })
  }

  const drawerContent = (
    <div>
      {/* Header details */}
      <Descriptions bordered={false} size="small" column={1} className="mb-4">
        <Descriptions.Item label={t`From`}>
          {messageHistory?.channel_options?.from_name ? (
            <>
              <Text>
                {messageHistory.channel_options.from_name} &lt;
                {templateSender?.email || defaultSender?.email || t`no email`}&gt;
              </Text>
              {(templateSender || defaultSender) && (
                <Text type="secondary" className="text-xs pl-2">
                  {t`(original: ${templateSender?.name || defaultSender?.name})`}
                </Text>
              )}
            </>
          ) : (
            <>
              {templateSender ? (
                <>
                  <Text>
                    {templateSender.name}
                    <Text> &lt;{templateSender.email}&gt;</Text>
                  </Text>
                </>
              ) : (
                <>
                  {defaultSender ? (
                    <Text>
                      {defaultSender.name}
                      <Text> &lt;{defaultSender.email}&gt;</Text>
                    </Text>
                  ) : (
                    <Text>{t`No default sender configured`}</Text>
                  )}
                </>
              )}
            </>
          )}
        </Descriptions.Item>

        {(record.email?.reply_to || messageHistory?.channel_options?.reply_to) && (
          <Descriptions.Item label={t`Reply to`}>
            {messageHistory?.channel_options?.reply_to ? (
              <>
                <Text>{messageHistory.channel_options.reply_to}</Text>
                {record.email?.reply_to && (
                  <Text type="secondary" className="text-xs pl-2">
                    {t`(original: ${record.email.reply_to})`}
                  </Text>
                )}
              </>
            ) : (
              <Text>{record.email?.reply_to || t`Not set`}</Text>
            )}
          </Descriptions.Item>
        )}

        <Descriptions.Item label={t`Subject`}>
          <Text>{renderedSubject ?? effectiveEmail?.subject}</Text>
        </Descriptions.Item>

        {(renderedSubjectPreview || effectiveEmail?.subject_preview) && (
          <Descriptions.Item label={t`Subject preview`}>
            <Text>{renderedSubjectPreview ?? effectiveEmail?.subject_preview}</Text>
          </Descriptions.Item>
        )}

        {/* Channel Options Display - CC and BCC */}
        {messageHistory?.channel_options?.cc && messageHistory.channel_options.cc.length > 0 && (
          <Descriptions.Item label={t`CC`}>
            <Space size={[0, 4]} wrap>
              {messageHistory.channel_options.cc.map((email, idx) => (
                <Tag bordered={false} key={idx} color="blue" className="text-xs">
                  {email}
                </Tag>
              ))}
            </Space>
          </Descriptions.Item>
        )}

        {messageHistory?.channel_options?.bcc && messageHistory.channel_options.bcc.length > 0 && (
          <Descriptions.Item label={t`BCC`}>
            <Space size={[0, 4]} wrap>
              {messageHistory.channel_options.bcc.map((email, idx) => (
                <Tag bordered={false} key={idx} color="purple" className="text-xs">
                  {email}
                </Tag>
              ))}
            </Space>
          </Descriptions.Item>
        )}
        {showLanguageSelector && (
          <Descriptions.Item label={t`Language`}>
            <Segmented
              size="small"
              value={effectiveLanguage}
              onChange={(value) => setSelectedLanguage(value as string)}
              options={availableLanguages}
            />
          </Descriptions.Item>
        )}
      </Descriptions>
      {/* Main content area */}
      <div className="flex flex-col mt-4">
        {isLoading && (
          <div className="p-4 flex-grow">
            <Skeleton active paragraph={{ rows: 8 }} />
          </div>
        )}
        {!isLoading &&
          error &&
          !mjmlError && ( // General error (not MJML compilation error)
            <div className="p-4">
              <Alert message={t`Error loading preview`} description={error} type="error" showIcon />
            </div>
          )}
        {!isLoading && mjmlError && (
          // MJML Compilation Error
          <div className="p-4 overflow-auto flex-grow flex flex-col">
            <Alert
              message={t`MJML Compilation Error: ${mjmlError.message}`}
              type="error"
              showIcon
              description={
                mjmlError.details && mjmlError.details.length > 0 ? (
                  <ul className="list-disc list-inside mt-2 text-xs">
                    {mjmlError.details.map((detail, index) => (
                      <li key={index}>
                        {t`Line ${detail.line} (${detail.tagName}): ${detail.message}`}
                      </li>
                    ))}
                  </ul>
                ) : (
                  t`No specific details provided.`
                )
              }
              className="mb-4 flex-shrink-0" // Prevent alert from growing too large
            />
          </div>
        )}
        {!isLoading &&
          items.length > 0 && ( // Success case
            <Tabs
              activeKey={activeTabKey} // Control active tab
              onChange={setActiveTabKey} // Update state on tab change (onChange is preferred over onTabClick for controlled Tabs)
              className="flex flex-col flex-grow"
              items={items}
              destroyOnHidden={false}
            />
          )}
        {!isLoading &&
          !error &&
          !mjmlError &&
          !previewHtml &&
          !previewMjml &&
          items.length === 0 && ( // Neither success nor error, initial or no data state
            <div className="flex items-center justify-center flex-grow text-gray-500">
              {t`No preview available or template is empty.`}
            </div>
          )}
      </div>
    </div>
  )

  return (
    <>
      <div onClick={() => setIsOpen(true)}>{children}</div>
      <Drawer
        title={`${record.name}`}
        placement="right"
        width={650}
        open={isOpen}
        onClose={() => setIsOpen(false)}
        destroyOnClose={true}
        maskClosable={true}
        mask={true}
        keyboard={true}
        forceRender={false}
      >
        {drawerContent}
      </Drawer>
    </>
  )
}

// ── Panneau Délivrabilité (Veridian) ─────────────────────────────────────────
//
// Garde-fou AVANT envoi : l'admin voit le risque spam de son template (linter Go
// natif, instantané). Score 0-10 (>5 = risque), règles déclenchées avec message
// d'aide. Un sélecteur de classe de provider permet de voir le mode strict
// (Google/Microsoft, draconien) vs lenient (petits providers, tolérant).
//
// Endpoint POST /api/veridian/templates.deliverabilityScore. On lint le HTML
// COMPILÉ (rendu final) : c'est ce que verra le destinataire, et c'est là qu'on
// détecte les variables Liquid/spintax qui fuitent non résolues.

// Couleur du badge selon le score (façon SpamAssassin) : vert <3, orange 3-5,
// rouge >5 (RiskThreshold backend = 5).
function deliverabilityScoreColor(score: number): string {
  if (score < 3) return '#52c41a' // vert
  if (score <= 5) return '#faad14' // orange
  return '#ff4d4f' // rouge
}

// Libellé lisible d'une classe de provider. ⚠️ LITTÉRAUX (pas `t`...``) : noms
// propres de providers qui ne se traduisent pas, et un `t` passé hors composant
// casse l'extracteur statique Lingui (clé absente → libellé VIDE runtime, bug P0
// vécu 2026-06-14). Aligné sur classLabel() de veridian_cold_outreach_settings.tsx.
const DELIVERABILITY_CLASS_LABELS: Record<VeridianProviderClass, string> = {
  google: 'Google (Gmail / Workspace) — strict',
  microsoft: 'Microsoft (Outlook / Microsoft 365) — strict',
  yahoo_aol: 'Yahoo / AOL',
  freemail_fr: 'French ISPs (Orange, SFR, Free…)',
  corporate: 'Corporate (unknown by suffix)',
  ovh: 'OVH',
  ionos: 'IONOS / 1&1',
  apple_icloud: 'Apple iCloud — strict',
  security_gateway: 'Anti-spam gateway (Vade, Mailinblack…)',
  other_hoster: 'Other hosters (Infomaniak, Gandi, Zoho…)',
  corporate_selfhost: 'Corporate self-hosted'
}

interface DeliverabilityPanelProps {
  workspaceId: string
  html: string
  subject: string
  fromDomain?: string
}

export const DeliverabilityPanel: React.FC<DeliverabilityPanelProps> = ({
  workspaceId,
  html,
  subject,
  fromDomain
}) => {
  const { t } = useLingui()
  // Classe destinataire choisie ('' = mode "default" neutre, pas de classe).
  const [providerClass, setProviderClass] = useState<VeridianProviderClass | ''>('')
  const [result, setResult] = useState<VeridianDeliverabilityResult | null>(null)
  const [loading, setLoading] = useState(false)
  const [error, setError] = useState<string | null>(null)

  const runScore = useCallback(async () => {
    setLoading(true)
    setError(null)
    try {
      const res = await deliverabilityApi.score({
        workspace_id: workspaceId,
        subject,
        body: html,
        is_html: true,
        from_domain: fromDomain,
        provider_class: providerClass || undefined,
        // Pas de classe = profil neutre explicite.
        mode: providerClass ? undefined : ('default' as VeridianDeliverabilityMode)
      })
      setResult(res)
    } catch (err: unknown) {
      setError((err as Error)?.message || 'Failed to score deliverability')
      setResult(null)
    } finally {
      setLoading(false)
    }
  }, [workspaceId, subject, html, fromDomain, providerClass])

  // Lint au montage et à chaque changement de classe / de HTML rendu.
  useEffect(() => {
    runScore()
  }, [runScore])

  const classOptions = [
    { value: '', label: t`Neutral profile (no recipient class)` },
    ...VERIDIAN_PROVIDER_CLASSES.map((c) => ({
      value: c,
      label: DELIVERABILITY_CLASS_LABELS[c]
    }))
  ]

  return (
    <div className="p-2">
      <Space direction="vertical" size="middle" style={{ width: '100%' }}>
        <Alert
          type="info"
          showIcon
          icon={<SafetyCertificateOutlined />}
          message={t`Deliverability check (spam score)`}
          description={
            <Text type="secondary" style={{ fontSize: 12 }}>
              {t`Native lint of the rendered HTML before sending. Score 0-10 (above 5 = high spam risk). Pick a recipient provider class to see the strict profile (Google/Microsoft penalize links, tracking and HTML heavily) vs the lenient one (smaller providers tolerate light HTML).`}
            </Text>
          }
        />

        <Space wrap>
          <Text type="secondary" style={{ fontSize: 12 }}>
            {t`Simulate for`}
          </Text>
          <Select
            value={providerClass}
            onChange={(v) => setProviderClass(v as VeridianProviderClass | '')}
            options={classOptions}
            style={{ minWidth: 320 }}
            aria-label={t`Recipient provider class`}
          />
          <Button
            icon={<ReloadOutlined />}
            size="small"
            onClick={runScore}
            loading={loading}
            aria-label={t`Re-run deliverability score`}
          >
            {t`Re-check`}
          </Button>
        </Space>

        {loading && <Skeleton active paragraph={{ rows: 3 }} />}

        {!loading && error && (
          <Alert type="error" showIcon message={t`Could not score the template`} description={error} />
        )}

        {!loading && !error && result && (
          <>
            <Space align="center" size="large" wrap>
              <div
                style={{
                  width: 64,
                  height: 64,
                  borderRadius: 8,
                  display: 'flex',
                  alignItems: 'center',
                  justifyContent: 'center',
                  background: deliverabilityScoreColor(result.score),
                  color: '#fff',
                  fontSize: 22,
                  fontWeight: 700
                }}
                aria-label={t`Deliverability score`}
              >
                {result.score}
              </div>
              <div>
                <div>
                  {result.is_risky ? (
                    <Tag color="red">{t`High spam risk`}</Tag>
                  ) : (
                    <Tag color="green">{t`Looks good`}</Tag>
                  )}
                  <Tag>{t`mode: ${result.mode}`}</Tag>
                </div>
                <Text type="secondary" style={{ fontSize: 12 }}>
                  {result.summary}
                </Text>
              </div>
            </Space>

            {result.rules.length === 0 ? (
              <Alert type="success" showIcon message={t`No spam rule triggered. Clean template.`} />
            ) : (
              <List
                size="small"
                header={
                  <Text strong>{t`Triggered rules (${result.rules.length})`}</Text>
                }
                dataSource={result.rules}
                renderItem={(rule) => (
                  <List.Item>
                    <Space direction="vertical" size={2} style={{ width: '100%' }}>
                      <Space size="small" wrap>
                        <Tag color={rule.weight < 0 ? 'green' : rule.weight >= 2 ? 'red' : 'orange'}>
                          {rule.weight > 0 ? `+${rule.weight}` : `${rule.weight}`}
                        </Tag>
                        <Text code style={{ fontSize: 12 }}>
                          {rule.name}
                        </Text>
                      </Space>
                      <Text type="secondary" style={{ fontSize: 12 }}>
                        {rule.message}
                      </Text>
                    </Space>
                  </List.Item>
                )}
              />
            )}
          </>
        )}
      </Space>
    </div>
  )
}

const JsonDataViewer = ({ data }: { data: Record<string, unknown> }) => {
  const prettyJson = JSON.stringify(data, null, 2)

  return (
    <div className="rounded" style={{ maxWidth: '100%' }}>
      <Highlight theme={themes.github} code={prettyJson} language="json">
        {({ className, style, tokens, getLineProps, getTokenProps }) => (
          <pre
            className={className}
            style={{
              ...style,
              margin: '0',
              borderRadius: '4px',
              padding: '10px',
              fontSize: '12px',
              wordWrap: 'break-word',
              whiteSpace: 'pre-wrap',
              wordBreak: 'normal'
            }}
          >
            {tokens.map((line, i) => (
              <div key={i} {...getLineProps({ line })}>
                <span
                  style={{
                    display: 'inline-block',
                    width: '2em',
                    userSelect: 'none',
                    opacity: 0.3
                  }}
                >
                  {i + 1}
                </span>
                {line.map((token, key) => (
                  <span key={key} {...getTokenProps({ token })} />
                ))}
              </div>
            ))}
          </pre>
        )}
      </Highlight>
    </div>
  )
}

const MJMLPreview = ({ previewMjml }: { previewMjml: string }) => {
  return (
    <div className="overflow-auto">
      <Highlight theme={themes.github} code={previewMjml} language="xml">
        {({ className, style, tokens, getLineProps, getTokenProps }) => (
          <pre
            className={className}
            style={{
              ...style,
              fontSize: '12px',
              margin: 0,
              padding: '10px',
              wordWrap: 'break-word',
              whiteSpace: 'pre-wrap',
              wordBreak: 'normal'
            }}
          >
            {tokens.map((line, i) => (
              <div key={i} {...getLineProps({ line })}>
                <span
                  style={{
                    display: 'inline-block',
                    width: '2em',
                    userSelect: 'none',
                    opacity: 0.3
                  }}
                >
                  {i + 1}
                </span>
                {line.map((token, key) => (
                  <span key={key} {...getTokenProps({ token })} />
                ))}
              </div>
            ))}
          </pre>
        )}
      </Highlight>
    </div>
  )
}

export default TemplatePreviewDrawer
