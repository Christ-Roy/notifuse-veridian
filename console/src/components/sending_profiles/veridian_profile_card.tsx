import { Alert, Button, Card, Popconfirm, Progress, Space, Tag, Tooltip, Typography } from 'antd'
import { useLingui } from '@lingui/react/macro'
import { i18n } from '@lingui/core'

import type { EmailProfileOverview } from '../../services/api/veridian_email_profiles'
import { formatNumber, useProfileLabels } from './veridian_profile_labels'
import {
  canAddToRotation,
  canRemoveFromRotation,
  excludedClassesOf,
  limitingFactor,
  profileBlockers,
  reputationIssues,
  todayProgress
} from './veridian_profile_rules'

const { Text } = Typography

export type ProfileAction = 'test' | 'pause' | 'resume' | 'rotation' | 'edit' | 'delete'

interface Props {
  profile: EmailProfileOverview
  // Tous les profils du workspace : la règle « dernier profil de la rotation » en dépend.
  all: EmailProfileOverview[]
  isOwner: boolean
  // Action en cours sur CE profil (désactive les boutons).
  busy?: ProfileAction | null
  onAction: (action: ProfileAction, profile: EmailProfileOverview) => void
  now?: Date
}

function formatVerified(iso: string | null): string {
  if (!iso) return ''
  const date = new Date(iso)
  if (Number.isNaN(date.getTime())) return ''
  return new Intl.DateTimeFormat(i18n.locale || 'en', {
    day: '2-digit',
    month: '2-digit',
    hour: '2-digit',
    minute: '2-digit',
    hourCycle: 'h23'
  }).format(date)
}

export function VeridianProfileCard({ profile, all, isOwner, busy, onAction, now }: Props) {
  const { t } = useLingui()
  const labels = useProfileLabels()
  const plan = profile.plan
  // Un profil « non affecté » (créé, ni en rotation ni transactionnel) est un profil commercial hors
  // rotation : aucune porte commerciale ne s'applique tant qu'il n'y est pas (plan.applicable = false).
  const commercial = profile.usage !== 'transactional'
  const gated = commercial && plan.applicable
  const blockers = profileBlockers(profile, now)
  const reputation = reputationIssues(plan)
  const excluded = excludedClassesOf(plan)
  const progress = todayProgress(plan)
  const factor = limitingFactor(plan)
  const sentLabel = formatNumber(progress.sent)
  const capLabel = progress.cap === null ? '' : formatNumber(progress.cap)
  const reservedExtra = progress.reserved > progress.sent ? progress.reserved - progress.sent : 0
  const verifiedAt = formatVerified(profile.verified_at)
  const rotationAdd = canAddToRotation(profile)
  const rotationRemove = canRemoveFromRotation(profile, all)
  const inbox = profile.return_inbox
  const disabled = !isOwner || !!busy
  const canPause = commercial && profile.in_rotation

  const rotationBlockedText = (() => {
    if (profile.in_rotation) {
      return rotationRemove.allowed ? '' : t`The rotation must keep at least one profile`
    }
    if (rotationAdd.allowed) return ''
    return rotationAdd.reason === 'transactional'
      ? t`Reserved for transactional: change its usage in Edit`
      : t`Send a successful test before adding it to the rotation`
  })()
  const rotationDisabled = disabled || rotationBlockedText !== ''

  return (
    <Card
      size="small"
      data-testid={`profile-card-${profile.integration_id}`}
      style={{ marginBottom: 12 }}
      title={
        <Space size={8} wrap>
          <Text strong>{profile.name}</Text>
          <Tag>{labels.typeLabel(profile.type)}</Tag>
          {profile.verified ? (
            <Tag color="green">{t`Verified ${verifiedAt}`}</Tag>
          ) : (
            <Tag color="orange">{t`Not verified`}</Tag>
          )}
          {profile.paused && <Tag color="red">{t`Paused`}</Tag>}
          {commercial && profile.in_rotation && <Tag color="blue">{t`In rotation`}</Tag>}
          {commercial && !profile.in_rotation && <Tag>{t`Out of rotation`}</Tag>}
        </Space>
      }
    >
      <div style={{ marginBottom: 8 }}>
        <Text type="secondary">{t`Senders`}: </Text>
        {profile.senders.length === 0 ? (
          <Text type="secondary">{t`none`}</Text>
        ) : (
          profile.senders.map((sender, index) => (
            <span key={sender.email}>
              {index > 0 && ', '}
              {sender.name ? `${sender.name} <${sender.email}>` : sender.email}
            </span>
          ))
        )}
      </div>

      {gated ? (
        <div data-testid="today-block" style={{ marginBottom: 8 }}>
          <Text strong>{t`Today`}</Text>
          <div>
            {progress.cap === null ? (
              <Text>{t`${sentLabel} sent, no daily cap`}</Text>
            ) : (
              <Text>{t`${sentLabel} / ${capLabel} sent`}</Text>
            )}
            {reservedExtra > 0 && (
              <Text type="secondary"> ({t`${reservedExtra} reserved, in flight`})</Text>
            )}
          </div>
          {progress.percent !== null && (
            <Progress
              percent={progress.percent}
              size="small"
              showInfo={false}
              status={progress.percent >= 100 ? 'exception' : 'normal'}
            />
          )}
          <div data-testid="limiting-factor">
            <Text type="secondary">{t`Limited by`}: </Text>
            <Text>{labels.factorText(factor)}</Text>
          </div>
          {blockers.map((blocker) => (
            <div key={blocker.kind}>
              <Tag color={blocker.kind === 'window_closed' ? 'gold' : 'orange'}>
                {labels.blockerText(blocker)}
              </Tag>
            </div>
          ))}
        </div>
      ) : (
        <div data-testid="today-block" style={{ marginBottom: 8 }}>
          <Text strong>{t`Today`}</Text>
          <div>
            {commercial ? (
              <Text>{t`${sentLabel} sent. Out of the rotation: no cap or rule applies until it joins.`}</Text>
            ) : (
              <Text>{t`${sentLabel} sent. No commercial limit: a transactional mail always goes out.`}</Text>
            )}
          </div>
        </div>
      )}

      {gated && (
        <div data-testid="reputation-block" style={{ marginBottom: 8 }}>
          <Text strong>{t`Reputation by recipient provider`}</Text>
          {reputation.length === 0 ? (
            <div>
              <Text type="secondary">{t`No provider slowed or stopped`}</Text>
            </div>
          ) : (
            reputation.map((issue) => (
              <div key={issue.class}>
                <Tag color={issue.stopped ? 'red' : 'gold'}>{labels.reputationText(issue)}</Tag>
              </div>
            ))
          )}
          {excluded.length > 0 && (
            <div data-testid="excluded-classes">
              <Text type="secondary">{t`Excluded classes`}: </Text>
              <Text>{excluded.map((cls) => labels.classLabel(cls)).join(', ')}</Text>
            </div>
          )}
        </div>
      )}

      <div data-testid="inbox-block" style={{ marginBottom: 8 }}>
        <Text strong>{t`Return inbox`}</Text>
        {inbox ? (
          <div>
            <Text>{inbox.address}</Text>
            <Text type="secondary">
              {' '}
              ({inbox.host}
              {inbox.folder ? `, ${inbox.folder}` : ''}) {t`Replies and bounces`}
            </Text>
          </div>
        ) : (
          <div>
            <Text type="secondary">{t`No linked inbox`}</Text>
          </div>
        )}
      </div>

      {isOwner && (
        <Space wrap size={[8, 8]}>
          <Button size="small" disabled={!!busy} loading={busy === 'test'} onClick={() => onAction('test', profile)}>
            {t`Test`}
          </Button>
          {/* Un profil transactionnel n a pas de pause : le serveur la refuse (400) */}
          {commercial &&
            (profile.paused ? (
            <Button size="small" disabled={!!busy} loading={busy === 'resume'} onClick={() => onAction('resume', profile)}>
              {t`Resume`}
            </Button>
          ) : (
            <Popconfirm
              title={t`Pause this profile?`}
              description={t`The rotation switches to the other profiles. Queued messages are kept.`}
              okText={t`Pause`}
              cancelText={t`Cancel`}
              onConfirm={() => onAction('pause', profile)}
              disabled={!canPause}
            >
              <Button size="small" disabled={!!busy || !canPause} loading={busy === 'pause'}>
                {t`Pause`}
              </Button>
            </Popconfirm>
          ))}
          {commercial && (
            <Tooltip title={rotationBlockedText}>
              <span>
                <Button
                  size="small"
                  disabled={rotationDisabled}
                  loading={busy === 'rotation'}
                  onClick={() => onAction('rotation', profile)}
                >
                  {profile.in_rotation ? t`Remove from rotation` : t`Add to rotation`}
                </Button>
              </span>
            </Tooltip>
          )}
          <Button size="small" disabled={!!busy} onClick={() => onAction('edit', profile)}>
            {t`Edit`}
          </Button>
          <Popconfirm
            title={t`Delete this profile?`}
            description={t`Credentials are erased. Refused while messages are still queued.`}
            okText={t`Delete`}
            okButtonProps={{ danger: true }}
            cancelText={t`Cancel`}
            onConfirm={() => onAction('delete', profile)}
          >
            <Button size="small" danger disabled={!!busy} loading={busy === 'delete'}>
              {t`Delete`}
            </Button>
          </Popconfirm>
        </Space>
      )}
      {!isOwner && <Alert type="info" showIcon message={t`Only the workspace owner can change profiles`} />}
    </Card>
  )
}
