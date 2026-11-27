import { useQuery } from '@tanstack/react-query'
import { Alert, Table, Typography } from 'antd'
import type { TableColumnType } from 'antd'
import { useLingui } from '@lingui/react/macro'
import { veridianLiensApi, type LienClique } from '../../services/api/veridian_liens'

const { Text } = Typography

interface VeridianLiensRapportProps {
  workspaceId: string
  broadcastId: string
}

// Détail des clics par lien d'un envoi (reprise amont 35.0) : clics totaux et cliqueurs uniques par URL.
// Les clics sont comptés à partir de la mise en service ; un envoi antérieur n'a pas de détail.
export function VeridianLiensRapport({ workspaceId, broadcastId }: VeridianLiensRapportProps) {
  const { t } = useLingui()

  const { data, isLoading, isError } = useQuery({
    queryKey: ['veridian-liens', workspaceId, broadcastId],
    queryFn: () => veridianLiensApi.rapport(workspaceId, broadcastId),
    enabled: !!workspaceId && !!broadcastId
  })

  const columns: TableColumnType<LienClique>[] = [
    {
      title: t`Link`,
      dataIndex: 'url',
      key: 'url',
      render: (url: string) => (
        <Text className="break-all" code>
          {url}
        </Text>
      )
    },
    {
      title: t`Total clicks`,
      dataIndex: 'clics',
      key: 'clics',
      align: 'right',
      width: 140
    },
    {
      title: t`Unique clickers`,
      dataIndex: 'cliqueurs',
      key: 'cliqueurs',
      align: 'right',
      width: 160
    }
  ]

  if (isError) {
    return <Alert type="error" showIcon message={t`Could not load the link clicks`} />
  }

  return (
    <div className="mt-4">
      <Text strong>{t`Clicks by link`}</Text>
      <Table<LienClique>
        className="mt-2"
        size="small"
        columns={columns}
        dataSource={data?.lignes ?? []}
        rowKey="url"
        loading={isLoading}
        pagination={false}
        locale={{ emptyText: t`No link clicked yet` }}
      />
    </div>
  )
}
