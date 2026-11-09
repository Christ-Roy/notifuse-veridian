/**
 * Veridian — « Brancher mon IA » : un point d'entrée visible dans l'en-tête de la console.
 * Les liens viennent du Hub (voir lib/veridian_connect_ai). Si le Hub ne répond pas, le modal montre l'adresse MCP
 * à copier et un message : jamais de bouton mort.
 */

import { useState } from 'react'
import { Button, Modal, Space, Typography, message } from 'antd'
import { ApiOutlined, CopyOutlined } from '@ant-design/icons'
import { useLingui } from '@lingui/react/macro'
import { useQuery } from '@tanstack/react-query'
import { veridianApi, type VeridianModeResponse } from '../services/api/veridian'
import {
  VERIDIAN_MCP_FALLBACK_URL,
  VERIDIAN_MCP_REVOKE_URL,
  fetchInstallLinks,
  type InstallLinks
} from '../lib/veridian_connect_ai'

async function copy(text: string, okMessage: string) {
  try {
    await navigator.clipboard.writeText(text)
    message.success(okMessage)
  } catch {
    message.warning(text)
  }
}

export function VeridianConnectAI({ compact = false }: { compact?: boolean }) {
  const { t } = useLingui()
  const [open, setOpen] = useState(false)

  const { data: mode } = useQuery<VeridianModeResponse | null>({
    queryKey: ['veridian', 'mode'],
    queryFn: () => veridianApi.getMode().catch(() => null),
    staleTime: 10 * 60 * 1000,
    retry: false
  })
  const { data: links, isFetched } = useQuery<InstallLinks | null>({
    queryKey: ['veridian', 'mcp-install-links'],
    queryFn: () => fetchInstallLinks(),
    enabled: open,
    staleTime: 5 * 60 * 1000,
    retry: false
  })

  if (!mode || mode.mode !== 'veridian-managed') return null

  const address = links?.url ?? VERIDIAN_MCP_FALLBACK_URL
  const unavailable = isFetched && (!links || links.assistants.length === 0)

  return (
    <>
      <Button
        color="default"
        variant="filled"
        icon={<ApiOutlined />}
        onClick={() => setOpen(true)}
        data-testid="veridian-connect-ai"
        aria-label={t`Brancher mon IA`}
      >
        {compact ? null : t`Brancher mon IA`}
      </Button>
      <Modal
        open={open}
        onCancel={() => setOpen(false)}
        footer={null}
        title={t`Brancher mon IA`}
        destroyOnHidden
      >
        <Space direction="vertical" size="middle" style={{ width: '100%' }}>
          <Typography.Paragraph style={{ marginBottom: 0 }}>
            {links?.phrase ??
              t`Une seule connexion Veridian : votre IA accède à Mail et à vos autres apps Veridian.`}
          </Typography.Paragraph>

          {links?.assistants.map((a) => (
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

          {unavailable && (
            <Typography.Text type="warning" data-testid="veridian-connect-ai-fallback">
              {t`Les boutons d'installation sont momentanément indisponibles. Ajoutez cette adresse comme serveur MCP dans votre assistant :`}
            </Typography.Text>
          )}

          <div>
            <Typography.Text type="secondary">{t`Adresse MCP`}</Typography.Text>
            <Space.Compact style={{ width: '100%' }}>
              <Typography.Text code style={{ flex: 1, overflowWrap: 'anywhere' }}>
                {address}
              </Typography.Text>
              <Button icon={<CopyOutlined />} onClick={() => void copy(address, t`Adresse copiée`)} />
            </Space.Compact>
          </div>

          {links?.claudeCode && (
            <div>
              <Typography.Text type="secondary">{t`Claude Code`}</Typography.Text>
              <Space.Compact style={{ width: '100%' }}>
                <Typography.Text code style={{ flex: 1, overflowWrap: 'anywhere' }}>
                  {links.claudeCode}
                </Typography.Text>
                <Button
                  icon={<CopyOutlined />}
                  onClick={() => void copy(links.claudeCode as string, t`Commande copiée`)}
                />
              </Space.Compact>
            </div>
          )}

          <Typography.Link href={VERIDIAN_MCP_REVOKE_URL} target="_blank" rel="noopener noreferrer">
            {t`Gérer ou révoquer les accès IA`}
          </Typography.Link>
        </Space>
      </Modal>
    </>
  )
}
