import { useState, type ReactNode } from 'react'
import { Button, Checkbox, Input, InputNumber, Radio, Select, Space, Switch, Tag, Typography } from 'antd'
import { useLingui } from '@lingui/react/macro'

import type { EmailProvider, WorkspaceSettings } from '../../services/api/types'
import {
  VERIDIAN_PROVIDER_CLASSES,
  type VeridianProviderClass,
  type VeridianSendingWindow
} from '../../services/api/workspace'
import { useProfileLabels } from './veridian_profile_labels'
import {
  clearProfileSetting,
  seedClassTable,
  settingSource,
  type InheritableSetting,
  type SettingSource
} from './veridian_profile_inheritance'

const { Text } = Typography

const PRIMARY_CLASSES: VeridianProviderClass[] = ['google', 'microsoft', 'yahoo_aol', 'freemail_fr', 'corporate']
// Lundi en premier, comme l'agenda français.
const WEEK_ORDER = [1, 2, 3, 4, 5, 6, 0]
const DEFAULT_WARMUP_SCHEDULE = [20, 40, 80, 150, 300]

interface Props {
  value: EmailProvider
  onChange: (next: EmailProvider) => void
  workspaceSettings: Partial<WorkspaceSettings>
  gmailMaxDailyCap?: number
}

function SourceTag({ source }: { source: SettingSource }) {
  const { t } = useLingui()
  if (source === 'workspace') return <Tag color="blue">{t`Inherited from the workspace`}</Tag>
  if (source === 'default') return <Tag>{t`Default`}</Tag>
  return <Tag color="green">{t`Set on this profile`}</Tag>
}

interface RowProps {
  title: string
  help?: string
  source?: SettingSource
  onReset?: () => void
  children: ReactNode
}

function SettingRow({ title, help, source, onReset, children }: RowProps) {
  const { t } = useLingui()
  return (
    <div style={{ marginBottom: 16 }} data-testid={`setting-${title}`}>
      <Space size={8} wrap>
        <Text strong>{title}</Text>
        {source && <SourceTag source={source} />}
        {source === 'profile' && onReset && (
          <Button type="link" size="small" onClick={onReset} style={{ padding: 0 }}>
            {t`Use the inherited value`}
          </Button>
        )}
      </Space>
      {help && (
        <div>
          <Text type="secondary" style={{ fontSize: 12 }}>
            {help}
          </Text>
        </div>
      )}
      <div style={{ marginTop: 6 }}>{children}</div>
    </div>
  )
}

function inheritedHint(
  source: SettingSource,
  text: string | null
): string | undefined {
  return source === 'workspace' && text ? text : undefined
}

export function VeridianProfileAdvanced({ value, onChange, workspaceSettings, gmailMaxDailyCap }: Props) {
  const { t } = useLingui()
  const labels = useProfileLabels()
  const [allClasses, setAllClasses] = useState(false)
  const src = (setting: InheritableSetting) => settingSource(setting, value, workspaceSettings)
  const patch = (changes: Partial<EmailProvider>) => onChange({ ...value, ...changes })
  const reset = (setting: InheritableSetting) => onChange(clearProfileSetting(value, setting))
  const classes = allClasses ? VERIDIAN_PROVIDER_CLASSES : PRIMARY_CLASSES

  const rateSource = src('class_rates')
  const capSource = src('class_caps')
  const windowSource = src('sending_window')
  const windowValue = value.veridian_sending_window
  const tz = workspaceSettings.timezone || 'UTC'
  const warmupOn = !!value.veridian_warmup_started_at && (value.veridian_warmup_schedule?.length ?? 0) > 0

  const setClassValue = (
    key: 'veridian_provider_class_rates' | 'veridian_provider_class_daily_cap',
    cls: VeridianProviderClass,
    next: number | null
  ) => {
    const table = { ...(value[key] ?? {}) } as Record<string, number>
    if (next === null) delete table[cls]
    else table[cls] = next
    patch({ [key]: table } as Partial<EmailProvider>)
  }

  const setWindow = (next: VeridianSendingWindow | undefined) => {
    if (!next) {
      reset('sending_window')
      return
    }
    patch({ veridian_sending_window: next })
  }

  const percent = (ratio: number | undefined) => (ratio === undefined ? undefined : Math.round(ratio * 1000) / 10)

  const profileTable = (
    kind: 'rates' | 'caps',
    source: SettingSource
  ) => {
    const key = kind === 'rates' ? 'veridian_provider_class_rates' : 'veridian_provider_class_daily_cap'
    const own = (value[key] ?? {}) as Record<string, number>
    const inherited = (workspaceSettings[key] ?? {}) as Record<string, number>
    const setting: InheritableSetting = kind === 'rates' ? 'class_rates' : 'class_caps'
    const shown = source === 'workspace' ? inherited : own
    return (
      <div>
        {source === 'workspace' && (
          <Button
            size="small"
            style={{ marginBottom: 8 }}
            onClick={() => onChange(seedClassTable(setting as 'class_rates' | 'class_caps', value, workspaceSettings))}
          >
            {t`Customize for this profile (copies the workspace values)`}
          </Button>
        )}
        <Space direction="vertical" size={4}>
          {classes.map((cls) => (
            <Space key={cls} size={8}>
              <span style={{ display: 'inline-block', width: 170 }}>{labels.classLabel(cls)}</span>
              <InputNumber
                size="small"
                min={0}
                step={kind === 'rates' ? 0.1 : 1}
                precision={kind === 'rates' ? 2 : 0}
                disabled={source === 'workspace'}
                value={shown[cls]}
                placeholder={kind === 'rates' ? t`per minute` : t`per day`}
                onChange={(next) => setClassValue(key, cls, next === null ? null : Number(next))}
                aria-label={`${kind}-${cls}`}
              />
            </Space>
          ))}
        </Space>
        <div>
          <Button type="link" size="small" style={{ padding: 0 }} onClick={() => setAllClasses(!allClasses)}>
            {allClasses ? t`Show the main providers only` : t`Show all provider classes`}
          </Button>
        </div>
      </div>
    )
  }

  return (
    <div data-testid="profile-advanced">
      <SettingRow
        title={t`Profile daily cap`}
        help={t`Hard ceiling of messages per day for this profile, all senders and providers together. 0 means no profile cap.`}
      >
        <InputNumber
          min={0}
          max={gmailMaxDailyCap}
          precision={0}
          value={value.veridian_profile_daily_cap ?? 0}
          onChange={(next) => patch({ veridian_profile_daily_cap: Number(next ?? 0) })}
          aria-label="profile-daily-cap"
        />
      </SettingRow>

      <SettingRow
        title={t`Warmup`}
        help={t`The cap climbs step by step from the start date. While active it replaces the per provider caps.`}
      >
        <Switch
          checked={warmupOn}
          onChange={(on) => {
            if (on) {
              patch({
                veridian_warmup_started_at: `${new Date().toISOString().slice(0, 10)}T00:00:00Z`,
                veridian_warmup_schedule: DEFAULT_WARMUP_SCHEDULE,
                veridian_warmup_step_days: value.veridian_warmup_step_days || 1
              })
            } else {
              const next = { ...value }
              delete next.veridian_warmup_started_at
              delete next.veridian_warmup_schedule
              delete next.veridian_warmup_step_days
              onChange(next)
            }
          }}
          aria-label="warmup-switch"
        />
        {warmupOn && (
          <Space direction="vertical" size={6} style={{ marginTop: 8, display: 'flex' }}>
            <Space wrap>
              <Text>{t`Start date`}</Text>
              <Input
                type="date"
                style={{ width: 160 }}
                value={(value.veridian_warmup_started_at ?? '').slice(0, 10)}
                onChange={(event) =>
                  event.target.value &&
                  patch({ veridian_warmup_started_at: `${event.target.value}T00:00:00Z` })
                }
                aria-label="warmup-start"
              />
              <Text>{t`Days per step`}</Text>
              <InputNumber
                min={1}
                precision={0}
                value={value.veridian_warmup_step_days || 1}
                onChange={(next) => patch({ veridian_warmup_step_days: Number(next ?? 1) })}
                aria-label="warmup-step-days"
              />
            </Space>
            <Space wrap>
              <Text>{t`Daily caps by step`}</Text>
              <Input
                style={{ width: 260 }}
                defaultValue={(value.veridian_warmup_schedule ?? []).join(', ')}
                onBlur={(event) => {
                  const schedule = event.target.value
                    .split(/[,\s]+/)
                    .map((part) => Number(part))
                    .filter((n) => Number.isInteger(n) && n > 0)
                  if (schedule.length > 0) patch({ veridian_warmup_schedule: schedule })
                }}
                aria-label="warmup-schedule"
              />
            </Space>
          </Space>
        )}
      </SettingRow>

      <SettingRow
        title={t`Sending window`}
        source={windowSource}
        onReset={() => reset('sending_window')}
        help={inheritedHint(
          windowSource,
          workspaceSettings.veridian_sending_window
            ? `${workspaceSettings.veridian_sending_window.start_hour}h-${workspaceSettings.veridian_sending_window.end_hour}h (${workspaceSettings.veridian_sending_window.timezone || tz})`
            : null
        )}
      >
        <Switch
          checked={!!windowValue}
          onChange={(on) =>
            setWindow(on ? { days: [1, 2, 3, 4, 5], start_hour: 9, end_hour: 18, timezone: tz } : undefined)
          }
          aria-label="window-switch"
        />
        {windowValue && (
          <Space direction="vertical" size={6} style={{ marginTop: 8, display: 'flex' }}>
            <Checkbox.Group
              value={windowValue.days ?? []}
              onChange={(days) => setWindow({ ...windowValue, days: days as number[] })}
              options={WEEK_ORDER.map((day) => ({ label: labels.weekdayLabel(day), value: day }))}
            />
            <Space wrap>
              <Text>{t`From`}</Text>
              <InputNumber
                min={0}
                max={23}
                precision={0}
                value={windowValue.start_hour}
                onChange={(next) => setWindow({ ...windowValue, start_hour: Number(next ?? 0) })}
                aria-label="window-start"
              />
              <Text>{t`to`}</Text>
              <InputNumber
                min={1}
                max={24}
                precision={0}
                value={windowValue.end_hour}
                onChange={(next) => setWindow({ ...windowValue, end_hour: Number(next ?? 24) })}
                aria-label="window-end"
              />
              <Input
                style={{ width: 180 }}
                value={windowValue.timezone ?? tz}
                onChange={(event) => setWindow({ ...windowValue, timezone: event.target.value })}
                aria-label="window-timezone"
              />
            </Space>
          </Space>
        )}
      </SettingRow>

      <SettingRow
        title={t`Rate per provider (messages per minute)`}
        source={rateSource}
        onReset={() => reset('class_rates')}
        help={t`The table of this profile replaces the workspace table as a whole. 0 means unthrottled.`}
      >
        {profileTable('rates', rateSource)}
      </SettingRow>

      <SettingRow
        title={t`Daily cap per provider`}
        source={capSource}
        onReset={() => reset('class_caps')}
        help={t`The table of this profile replaces the workspace table as a whole. 0 means no cap.`}
      >
        {profileTable('caps', capSource)}
      </SettingRow>

      <SettingRow
        title={t`Daily cap per recipient`}
        source={src('per_recipient_cap')}
        onReset={() => reset('per_recipient_cap')}
        help={inheritedHint(
          src('per_recipient_cap'),
          String(workspaceSettings.veridian_per_recipient_daily_cap ?? '')
        )}
      >
        <InputNumber
          min={0}
          precision={0}
          value={
            src('per_recipient_cap') === 'workspace'
              ? workspaceSettings.veridian_per_recipient_daily_cap
              : (value.veridian_per_recipient_daily_cap ?? 0)
          }
          onChange={(next) => patch({ veridian_per_recipient_daily_cap: Number(next ?? 0) })}
          aria-label="per-recipient-cap"
        />
      </SettingRow>

      <SettingRow
        title={t`Daily cap per sending address`}
        source={src('per_sender_cap')}
        onReset={() => reset('per_sender_cap')}
      >
        <InputNumber
          min={0}
          precision={0}
          value={
            src('per_sender_cap') === 'workspace'
              ? workspaceSettings.veridian_per_sender_daily_cap
              : (value.veridian_per_sender_daily_cap ?? 0)
          }
          onChange={(next) => patch({ veridian_per_sender_daily_cap: Number(next ?? 0) })}
          aria-label="per-sender-cap"
        />
      </SettingRow>

      <SettingRow
        title={t`Excluded provider classes`}
        source={src('excluded_classes')}
        onReset={() => reset('excluded_classes')}
        help={t`Contacts of an excluded class are skipped by this profile.`}
      >
        <Select
          mode="multiple"
          style={{ minWidth: 320 }}
          value={
            src('excluded_classes') === 'workspace'
              ? (workspaceSettings.veridian_excluded_provider_classes ?? [])
              : (value.veridian_excluded_provider_classes ?? [])
          }
          options={VERIDIAN_PROVIDER_CLASSES.map((cls) => ({ value: cls, label: labels.classLabel(cls) }))}
          onChange={(next) => patch({ veridian_excluded_provider_classes: next as VeridianProviderClass[] })}
          aria-label="excluded-classes"
        />
      </SettingRow>

      <SettingRow
        title={t`Jitter (random spread of the pacing)`}
        source={src('jitter')}
        onReset={() => reset('jitter')}
        help={t`Percent. 0 turns the spread off. Empty uses the default of 30%.`}
      >
        <InputNumber
          min={0}
          max={90}
          precision={0}
          addonAfter="%"
          value={
            src('jitter') === 'workspace'
              ? percent(workspaceSettings.veridian_jitter_pct)
              : percent(value.veridian_jitter_pct)
          }
          onChange={(next) => {
            if (next === null) reset('jitter')
            else patch({ veridian_jitter_pct: Number(next) / 100 })
          }}
          aria-label="jitter"
        />
      </SettingRow>

      <SettingRow
        title={t`Identical content guard`}
        source={src('anti_hash')}
        onReset={() => reset('anti_hash')}
        help={t`Logs two messages with the same rendered content sent to the same provider within the window.`}
      >
        <Radio.Group
          value={
            src('anti_hash') === 'workspace'
              ? workspaceSettings.veridian_anti_hash_enabled
              : value.veridian_anti_hash_enabled
          }
          onChange={(event) => patch({ veridian_anti_hash_enabled: event.target.value as boolean })}
        >
          <Radio value={true}>{t`On`}</Radio>
          <Radio value={false}>{t`Off`}</Radio>
        </Radio.Group>
        <Space style={{ marginLeft: 12 }}>
          <Text type="secondary">{t`Window (hours)`}</Text>
          <InputNumber
            min={1}
            precision={0}
            value={
              src('anti_hash_window') === 'workspace'
                ? workspaceSettings.veridian_anti_hash_window_hours
                : value.veridian_anti_hash_window_hours
            }
            placeholder="72"
            onChange={(next) => {
              if (next === null) reset('anti_hash_window')
              else patch({ veridian_anti_hash_window_hours: Number(next) })
            }}
            aria-label="anti-hash-window"
          />
        </Space>
      </SettingRow>

      <SettingRow
        title={t`Reputation circuit breaker threshold`}
        help={t`Share of hard bounces over 7 days above which sending from this domain is frozen. Between 1% and 15%, default 3%.`}
      >
        <InputNumber
          min={1}
          max={15}
          precision={1}
          addonAfter="%"
          value={percent(value.veridian_hard_bounce_freeze_threshold)}
          placeholder="3"
          onChange={(next) => {
            const nextProvider = { ...value }
            if (next === null) delete nextProvider.veridian_hard_bounce_freeze_threshold
            else nextProvider.veridian_hard_bounce_freeze_threshold = Number(next) / 100
            onChange(nextProvider)
          }}
          aria-label="freeze-threshold"
        />
      </SettingRow>

      <SettingRow
        title={t`SMTP technical brake (messages/minute)`}
        help={t`Safety pacing of the transport, not the capacity of the profile. The daily cap and the rules above set the capacity.`}
      >
        <InputNumber
          min={1}
          precision={0}
          value={value.rate_limit_per_minute}
          onChange={(next) => patch({ rate_limit_per_minute: Number(next ?? 1) })}
          aria-label="technical-brake"
        />
      </SettingRow>
    </div>
  )
}
