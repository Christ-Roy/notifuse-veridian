/**
 * Veridian : installation du connecteur MCP, contenu de la carte « Relier mon IA » (veridian_mon_ia.tsx, VER-203 : un seul
 * point d'entrée IA dans l'en-tête, l'ancien bouton « Brancher mon IA » n'existe plus). Installation par assistant, adresse MCP
 * à copier, commande Claude Code (CONTRAT-HUB §5.24.1) ; le lien vers le Hub (/dashboard/ia, révocation comprise) est celui de la carte.
 * Les liens viennent du Hub (voir lib/veridian_connect_ai), jamais en dur. Lecture impossible : rien ici, la carte garde son texte et
 * son lien, jamais de bouton mort. Mode veridian-managed seulement. Fiche 83.
 */

import { Button, Space, Typography, message } from 'antd'
import { CopyOutlined } from '@ant-design/icons'
import { useLingui } from '@lingui/react/macro'
import { useQuery } from '@tanstack/react-query'
import { veridianApi, type VeridianModeResponse } from '../services/api/veridian'
import { fetchInstallLinks, type InstallLinks } from '../lib/veridian_connect_ai'

async function copy(text: string, okMessage: string) {
  try {
    await navigator.clipboard.writeText(text)
    message.success(okMessage)
  } catch {
    message.warning(text)
  }
}

export function VeridianInstallLinks() {
  const { t } = useLingui()

  const { data: mode } = useQuery<VeridianModeResponse | null>({
    queryKey: ['veridian', 'mode'],
    queryFn: () => veridianApi.getMode().catch(() => null),
    staleTime: 10 * 60 * 1000,
    retry: false
  })
  const managed = mode?.mode === 'veridian-managed'
  const { data: links } = useQuery<InstallLinks | null>({
    queryKey: ['veridian', 'mcp-install-links'],
    queryFn: () => fetchInstallLinks(),
    enabled: managed,
    staleTime: 5 * 60 * 1000,
    retry: false
  })

  if (!managed || !links || links.assistants.length === 0) return null

  const address = links.url

  return (
    <div data-testid="veridian-connect-ai">
      <Space direction="vertical" size="middle" style={{ width: '100%' }}>
        <Typography.Text strong>{t`Connect it to your assistant`}</Typography.Text>
        {links.assistants.map((a) => (
          <div key={a.cle}>
            <Button type="primary" href={a.installation} target="_blank" rel="noopener noreferrer" block>
              {a.libelle}
            </Button>
            {a.etape && (
              <Typography.Text type="secondary" style={{ display: 'block', marginTop: 4 }}>
                {a.etape}
              </Typography.Text>
            )}
          </div>
        ))}

        <div>
          <Typography.Text type="secondary">{t`MCP address`}</Typography.Text>
          <Space.Compact style={{ width: '100%' }}>
            <Typography.Text code style={{ flex: 1, overflowWrap: 'anywhere' }}>
              {address}
            </Typography.Text>
            <Button icon={<CopyOutlined />} onClick={() => void copy(address, t`Address copied`)} />
          </Space.Compact>
        </div>

        {links.claudeCode && (
          <div>
            <Typography.Text type="secondary">{t`Claude Code`}</Typography.Text>
            <Space.Compact style={{ width: '100%' }}>
              <Typography.Text code style={{ flex: 1, overflowWrap: 'anywhere' }}>
                {links.claudeCode}
              </Typography.Text>
              <Button
                icon={<CopyOutlined />}
                onClick={() => void copy(links.claudeCode as string, t`Command copied`)}
              />
            </Space.Compact>
          </div>
        )}
      </Space>
    </div>
  )
}
