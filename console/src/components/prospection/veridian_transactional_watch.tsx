import React from 'react'
import { Alert, Card, Space, Tag, Typography } from 'antd'
import { useLingui } from '@lingui/react/macro'

import type { TransactionalWatch } from '../../services/api/veridian_email_profiles'
import { useWatchTexts } from './veridian_watch_texts'

const { Text } = Typography

// Surveillance du profil transactionnel (lot 5) : volume et réputation, MESURÉS et jamais
// bloquants. Une boucle côté application cliente ou une réputation qui se dégrade se voit
// ici ; rien n'est retardé, mis en pause ou refusé pour autant.

const levelColor = (level: string): string =>
  level === 'ok' ? 'green' : level === 'watch' ? 'orange' : level === 'alert' ? 'red' : 'default'

// Bandeau du tableau de bord commercial : silencieux quand tout va bien.
export const VeridianTransactionalWatchBanner: React.FC<{
  watch: TransactionalWatch | null | undefined
  onOpen?: () => void
}> = ({ watch, onOpen }) => {
  const { t } = useLingui()
  const texts = useWatchTexts()
  if (!watch || watch.level === 'ok') return null
  const type = watch.level === 'alert' ? 'error' : watch.level === 'watch' ? 'warning' : 'info'
  return (
    <Alert
      data-testid="transactional-watch-banner"
      type={type}
      showIcon
      style={{ marginBottom: 16 }}
      message={
        <span>
          {t`Transactional profile`}: {texts.levelLabel(watch.level)}
        </span>
      }
      description={
        <Space direction="vertical" size={0}>
          {watch.level === 'unknown' && <span>{t`The measure could not be made. Nothing is blocked.`}</span>}
          {watch.alerts.map((a) => (
            <span key={a.code}>{texts.alertText(a, watch)}</span>
          ))}
          {onOpen && (
            <a onClick={onOpen} role="button">
              {t`See the transactional dashboard`}
            </a>
          )}
        </Space>
      }
    />
  )
}

// Carte complète, dans la vue transactionnelle.
export const VeridianTransactionalWatchCard: React.FC<{ watch: TransactionalWatch | null | undefined }> = ({ watch }) => {
  const { t } = useLingui()
  const texts = useWatchTexts()
  if (!watch) return null
  return (
    <Card title={t`Volume and reputation watch`} style={{ marginBottom: 24 }} data-testid="transactional-watch-card">
      <Space direction="vertical" size={4}>
        <div>
          <Tag color={levelColor(watch.level)}>{texts.levelLabel(watch.level)}</Tag>
          <Text type="secondary">{watch.profile_name}</Text>
        </div>
        {watch.alerts.length === 0 && watch.level === 'ok' && (
          <Text>{t`No unusual volume and no reputation signal over the last 7 days.`}</Text>
        )}
        {watch.level === 'unknown' && <Text type="warning">{t`The measure could not be made. Nothing is blocked.`}</Text>}
        {watch.alerts.map((a) => (
          <Text key={a.code} type={a.level === 'alert' ? 'danger' : 'warning'}>
            {texts.alertText(a, watch)}
          </Text>
        ))}
        <Text type="secondary" style={{ fontSize: 12 }}>
          {t`This watch only informs: a transactional email is never held back, delayed or refused because of it.`}
        </Text>
      </Space>
    </Card>
  )
}
