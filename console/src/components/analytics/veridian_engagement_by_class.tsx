import React, { useState, useEffect } from 'react'
import { Card, Table, Alert, Tooltip } from 'antd'
import type { ColumnsType } from 'antd/es/table'
import { useLingui } from '@lingui/react/macro'
import {
  engagementByClassApi,
  VeridianClassEngagement
} from '../../services/api/veridian_engagement_by_class'
import { VERIDIAN_PROVIDER_CLASSES } from '../../services/api/workspace'
import { Workspace } from '../../services/api/types'

// Veridian — tableau d'ENGAGEMENT PAR CLASSE de provider destinataire, sur la
// fenêtre du dashboard : envoyés, taux de rejet, taux de réponses humaines. Permet
// de repérer une classe qui se dégrade (rejets Microsoft qui montent = signal
// d'arrêt AVANT de griller le domaine).
//
// La classe vient de la colonne persistée message_history.veridian_provider_class
// (résolution MX faite à l'envoi), pas d'un découpage par suffixe de domaine.
// Pas de colonnes Livré / Open / Click : mails texte brut, aucun accusé de
// livraison renvoyé par le relais (le bandeau des métriques le dit).

interface VeridianEngagementByClassProps {
  workspace: Workspace
  timeRange?: [string, string]
}

// Messages sans classe persistée (antérieurs à la classification).
const UNCLASSIFIED = 'unclassified'

interface ClassRow extends VeridianClassEngagement {
  key: string
  class: string
}

const EMPTY: VeridianClassEngagement = { sent: 0, bounced: 0, replied_human: 0 }

// Taux affiché modulo dénominateur 0 (— si rien envoyé).
export const formatRate = (num: number, denom: number): string => {
  if (denom === 0) return '—'
  const pct = (num / denom) * 100
  return pct === 0 || pct >= 10 ? `${Math.round(pct)}%` : `${pct.toFixed(1)}%`
}

export const VeridianEngagementByClass: React.FC<VeridianEngagementByClassProps> = ({
  workspace,
  timeRange = ['2024-01-01', '2024-12-31']
}) => {
  const { t } = useLingui()
  const [byClass, setByClass] = useState<Record<string, VeridianClassEngagement>>({})
  const [loading, setLoading] = useState(false)
  const [error, setError] = useState<string | null>(null)

  useEffect(() => {
    let cancelled = false
    const fetchData = async () => {
      try {
        setLoading(true)
        setError(null)
        const resp = await engagementByClassApi.get({
          workspace_id: workspace.id,
          start: timeRange[0],
          end: timeRange[1]
        })
        if (!cancelled) setByClass(resp.by_class || {})
      } catch (err) {
        if (!cancelled) {
          console.error('Failed to fetch engagement by class:', err)
          setError(
            err instanceof Error ? err.message : t`Failed to fetch engagement by provider class`
          )
        }
      } finally {
        if (!cancelled) setLoading(false)
      }
    }
    fetchData()
    return () => {
      cancelled = true
    }
  }, [workspace.id, timeRange, t])

  const classLabel = (c: string): string => {
    switch (c) {
      case 'google':
        return t`Google (Gmail / Workspace)`
      case 'microsoft':
        return t`Microsoft (Outlook / Microsoft 365)`
      case 'yahoo_aol':
        return t`Yahoo / AOL`
      case 'freemail_fr':
        return t`French ISPs (Orange, SFR, Free…)`
      case 'corporate':
        return t`Corporate (host unknown)`
      case 'ovh':
        return t`OVH`
      case 'ionos':
        return t`IONOS / 1&1`
      case 'apple_icloud':
        return t`Apple iCloud`
      case 'security_gateway':
        return t`Anti-spam gateway (Vade, Mailinblack, Proofpoint…)`
      case 'other_hoster':
        return t`Other hosters (Infomaniak, Gandi, Zoho…)`
      case 'infomaniak':
        return t`Infomaniak`
      case 'gandi':
        return t`Gandi`
      case 'hostinger':
        return t`Hostinger / Titan`
      case 'o2switch':
        return t`o2switch`
      case 'lws':
        return t`LWS`
      case 'scaleway':
        return t`Scaleway / Online`
      case 'website_builder':
        return t`Website builders`
      case 'corporate_selfhost':
        return t`Corporate self-hosted`
      default:
        return t`Unclassified`
    }
  }

  // Une ligne par classe ayant de l'activité (les classes 100 % vides sont
  // masquées pour ne pas noyer le signal), triées par envois décroissants.
  const rows: ClassRow[] = [...VERIDIAN_PROVIDER_CLASSES, UNCLASSIFIED]
    .map((c) => ({ key: c, class: c, ...(byClass[c] || EMPTY) }))
    .filter((r) => r.sent > 0 || r.bounced > 0 || r.replied_human > 0)
    .sort((a, b) => b.sent - a.sent)

  const columns: ColumnsType<ClassRow> = [
    {
      title: t`Provider class`,
      dataIndex: 'class',
      key: 'class',
      render: (_: unknown, r: ClassRow) => classLabel(r.class)
    },
    {
      title: t`Sent`,
      dataIndex: 'sent',
      key: 'sent',
      align: 'right',
      render: (_: unknown, r: ClassRow) => r.sent.toLocaleString()
    },
    {
      title: t`Bounce`,
      key: 'bounced',
      align: 'right',
      render: (_: unknown, r: ClassRow) => {
        const pct = r.sent === 0 ? 0 : (r.bounced / r.sent) * 100
        // Rouge si le rejet dépasse 5% (seuil de vigilance réputation cold).
        const danger = pct >= 5
        return (
          <Tooltip title={t`${r.bounced} bounced`}>
            <span style={danger ? { color: '#ef4444', fontWeight: 600 } : undefined}>
              {formatRate(r.bounced, r.sent)}
            </span>
          </Tooltip>
        )
      }
    },
    {
      title: t`Human replies`,
      key: 'replied_human',
      align: 'right',
      render: (_: unknown, r: ClassRow) => (
        <Tooltip title={t`${r.replied_human} human replies`}>
          <span>{formatRate(r.replied_human, r.sent)}</span>
        </Tooltip>
      )
    }
  ]

  return (
    <Card
      title={
        <Tooltip
          title={t`Recipients are grouped by their mailbox provider class, resolved at send time (OVH, IONOS, anti-spam gateways, Google, Microsoft…). Watch the bounce rate per class: a rising rate on one class is an early signal to slow down BEFORE the domain reputation is burned. Sends are counted on their send date, bounces on their bounce date, human replies on their reply date.`}
        >
          <span>{t`Engagement by provider class`}</span>
        </Tooltip>
      }
      className="mt-8"
    >
      {error && (
        <Alert message={t`Error`} description={error} type="error" showIcon style={{ marginBottom: 16 }} />
      )}
      <Table<ClassRow>
        columns={columns}
        dataSource={rows}
        loading={loading}
        pagination={false}
        size="small"
        locale={{ emptyText: t`No sends in this period yet.` }}
      />
    </Card>
  )
}
