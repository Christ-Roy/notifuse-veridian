import React, { useState, useEffect } from 'react'
import { Card, Table, Alert, Tooltip } from 'antd'
import type { ColumnsType } from 'antd/es/table'
import { useLingui } from '@lingui/react/macro'
import {
  engagementByClassApi,
  VeridianClassEngagement
} from '../../services/api/veridian_engagement_by_class'
import {
  VERIDIAN_PROVIDER_CLASSES,
  VeridianProviderClass
} from '../../services/api/workspace'
import { Workspace } from '../../services/api/types'

// Veridian — tableau d'ENGAGEMENT (sent/delivered/bounced/opened/clicked) PAR
// CLASSE de provider destinataire, sur la fenêtre du dashboard. Permet de
// repérer une classe qui se dégrade (bounce rate Microsoft qui monte = signal
// d'arrêt AVANT de griller le domaine). Cf.
// todo/2026-06-16-kpi-engagement-par-classe-provider.md.
//
// ⚠️ La classification backend est par SUFFIXE de domaine (pas MX, zéro lookup
// sur un dashboard de masse) → les classes MX (ovh/ionos/…) tombent en
// `corporate` ici. Dégradation gracieuse assumée (l'enforcement réputation
// utilise bien le MX à l'envoi). Une note le dit dans le tooltip du titre.

interface VeridianEngagementByClassProps {
  workspace: Workspace
  timeRange?: [string, string]
}

// Libellés LITTÉRAUX (noms propres → pas de `t` : piège Lingui d'extraction de
// `t` paramétré hors composant, cf. memory feedback_lingui_t_param_hors_composant).
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

interface ClassRow extends VeridianClassEngagement {
  key: string
  class: VeridianProviderClass
}

const EMPTY: VeridianClassEngagement = {
  sent: 0,
  delivered: 0,
  bounced: 0,
  opened: 0,
  clicked: 0
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
          setError(err instanceof Error ? err.message : t`Failed to fetch engagement by provider class`)
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

  // Taux affiché modulo dénominateur 0 (— si rien envoyé).
  const rate = (num: number, denom: number): string => {
    if (denom === 0) return '—'
    const pct = (num / denom) * 100
    return pct === 0 || pct >= 10 ? `${Math.round(pct)}%` : `${pct.toFixed(1)}%`
  }

  // Une ligne par classe canonique, même hors data (compteurs à 0) pour une
  // grille stable. On masque les classes 100% vides pour ne pas noyer le signal.
  const rows: ClassRow[] = VERIDIAN_PROVIDER_CLASSES.map((c) => {
    const e = byClass[c] || EMPTY
    return { key: c, class: c, ...e }
  }).filter((r) => r.sent > 0 || r.delivered > 0 || r.bounced > 0 || r.opened > 0 || r.clicked > 0)

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
      sorter: (a, b) => a.sent - b.sent,
      defaultSortOrder: 'descend'
    },
    {
      title: t`Delivered`,
      key: 'delivered',
      align: 'right',
      render: (_: unknown, r: ClassRow) => (
        <Tooltip title={t`${r.delivered} delivered`}>{rate(r.delivered, r.sent)}</Tooltip>
      )
    },
    {
      title: t`Bounce`,
      key: 'bounced',
      align: 'right',
      render: (_: unknown, r: ClassRow) => {
        const pct = r.sent === 0 ? 0 : (r.bounced / r.sent) * 100
        // Rouge si le bounce dépasse 5% (seuil de vigilance réputation cold).
        const danger = pct >= 5
        return (
          <Tooltip title={t`${r.bounced} bounced`}>
            <span style={danger ? { color: '#ef4444', fontWeight: 600 } : undefined}>
              {rate(r.bounced, r.sent)}
            </span>
          </Tooltip>
        )
      }
    },
    {
      title: t`Open`,
      key: 'opened',
      align: 'right',
      render: (_: unknown, r: ClassRow) => (
        <Tooltip title={t`${r.opened} opened`}>{rate(r.opened, r.sent)}</Tooltip>
      )
    },
    {
      title: t`Click`,
      key: 'clicked',
      align: 'right',
      render: (_: unknown, r: ClassRow) => (
        <Tooltip title={t`${r.clicked} clicked`}>{rate(r.clicked, r.sent)}</Tooltip>
      )
    }
  ]

  return (
    <Card
      title={
        <Tooltip
          title={t`Recipients are grouped by their mailbox provider (Google, Microsoft, French ISPs…). Watch the bounce rate per class: a rising bounce rate on one provider is an early signal to slow down BEFORE the domain reputation is burned. Note: classes are derived by domain suffix here (not a live MX lookup), so custom domains hosted on Google/Microsoft/OVH fall under "Corporate"; the sending engine still uses the real MX to throttle.`}
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
