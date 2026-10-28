import React from 'react'
import { Alert, Button, Descriptions, Drawer, Spin, Statistic, Typography } from 'antd'
import { useQuery } from '@tanstack/react-query'
import { useLingui } from '@lingui/react/macro'

import dayjs from '../../lib/dayjs'
import { templatesApi } from '../../services/api/template'
import type { AutomationNodeStats } from '../../services/api/automation'
import type { ProspectionStage } from '../../services/api/veridian_prospection'
import type { Workspace } from '../../services/api/types'
import { CreateTemplateDrawer } from '../templates/CreateTemplateDrawer'
import { formatNumber, formatPercent } from '../sending_profiles/veridian_profile_labels'
import { replyRatePercent } from './veridian_flow_labels'

const { Text } = Typography

interface Props {
  open: boolean
  onClose: () => void
  kind: 'email' | 'delay'
  workspace: Workspace
  // Droit d'écriture sur les modèles (même règle que la page Modèles) : sinon Modifier est désactivé.
  canEdit: boolean
  // Email
  templateId?: string
  stageLabel?: string
  stage?: ProspectionStage
  nodeStats?: AutomationNodeStats
  // Délai
  delay?: { duration: number; unit: string }
}

// Tiroir d'un nœud du flux. Email : le modèle (nom, objet, version, date), ses compteurs sur ce
// nœud, un aperçu rendu dans un iframe SANS script (sandbox vide) et le texte brut, avec
// « Modifier » qui ouvre l'éditeur de modèle existant. Délai : la durée et l'étape d'arrivée.
export const VeridianFlowNodeDrawer: React.FC<Props> = ({
  open,
  onClose,
  kind,
  workspace,
  canEdit,
  templateId,
  stageLabel,
  stage,
  nodeStats,
  delay
}) => {
  const { t } = useLingui()
  const timezone = workspace.settings?.timezone || 'Europe/Paris'

  const templateQuery = useQuery({
    queryKey: ['templates', workspace.id, 'flow-node', templateId],
    queryFn: () => templatesApi.get({ workspace_id: workspace.id, id: templateId as string }),
    enabled: open && kind === 'email' && !!templateId
  })
  const template = templateQuery.data?.template

  if (kind === 'delay') {
    const unit = delay?.unit ?? ''
    const duration = delay?.duration ?? 0
    return (
      <Drawer open={open} onClose={onClose} title={t`Wait`} width="min(420px, 100vw)">
        <Descriptions column={1} size="small" bordered>
          <Descriptions.Item label={t`Duration`}>{duration > 0 ? `${duration} ${unit}` : t`Not set`}</Descriptions.Item>
          <Descriptions.Item label={t`Reached at`}>{stageLabel ?? '-'}</Descriptions.Item>
        </Descriptions>
      </Drawer>
    )
  }

  const rate = replyRatePercent(stage?.replies_human ?? 0, stage?.sent ?? 0)
  const replies = stage?.replies_human ?? 0
  const title = template ? (stageLabel ? `${stageLabel} · ${template.name}` : template.name) : t`Email`

  return (
    <Drawer
      open={open}
      onClose={onClose}
      title={title}
      width="min(520px, 100vw)"
      destroyOnClose
      extra={
        template ? (
          <CreateTemplateDrawer
            workspace={workspace}
            template={template}
            buttonContent={t`Edit template`}
            buttonProps={{ type: 'primary', disabled: !canEdit }}
          />
        ) : (
          <Button type="primary" disabled>
            {t`Edit template`}
          </Button>
        )
      }
    >
      {templateQuery.isLoading && <Spin />}
      {templateQuery.isError && <Alert type="error" showIcon message={t`Unable to load the template`} />}
      {template && (
        <>
          <Descriptions column={1} size="small" bordered style={{ marginBottom: 16 }}>
            <Descriptions.Item label={t`Template`}>{template.name}</Descriptions.Item>
            <Descriptions.Item label={t`Subject`}>{template.email?.subject || '-'}</Descriptions.Item>
            <Descriptions.Item label={t`Version`}>{template.version}</Descriptions.Item>
            <Descriptions.Item label={t`Last edited`}>
              {dayjs(template.updated_at).tz(timezone).format('LL - HH:mm')}
            </Descriptions.Item>
          </Descriptions>

          <div style={{ display: 'flex', flexWrap: 'wrap', gap: 16, marginBottom: 16 }}>
            <Statistic title={t`Queued`} value={formatNumber(nodeStats?.queued ?? 0)} valueStyle={{ fontSize: 16 }} />
            <Statistic title={t`Sent`} value={formatNumber(nodeStats?.completed ?? 0)} valueStyle={{ fontSize: 16 }} />
            <Statistic title={t`Failed`} value={formatNumber(nodeStats?.failed ?? 0)} valueStyle={{ fontSize: 16 }} />
            <Statistic title={t`Human replies`} value={formatNumber(replies)} valueStyle={{ fontSize: 16 }} />
            <Statistic
              title={t`Reply rate`}
              value={rate === null ? '-' : formatPercent(rate / 100)}
              valueStyle={{ fontSize: 16 }}
            />
          </div>

          <Text strong>{t`Preview`}</Text>
          {template.email?.compiled_preview ? (
            <iframe
              title={t`Preview of ${template.name}`}
              sandbox=""
              srcDoc={template.email.compiled_preview}
              style={{ width: '100%', height: 420, border: '1px solid #e5e7eb', borderRadius: 6, marginTop: 8, background: '#fff' }}
            />
          ) : (
            <Alert type="info" showIcon message={t`No preview yet: open the template to render it.`} style={{ marginTop: 8 }} />
          )}

          {template.email?.text && (
            <>
              <Text strong style={{ display: 'block', marginTop: 16 }}>
                {t`Plain text`}
              </Text>
              <pre style={{ whiteSpace: 'pre-wrap', wordBreak: 'break-word', background: '#f9fafb', padding: 12, borderRadius: 6, fontSize: 13 }}>
                {template.email.text}
              </pre>
            </>
          )}
        </>
      )}
    </Drawer>
  )
}
