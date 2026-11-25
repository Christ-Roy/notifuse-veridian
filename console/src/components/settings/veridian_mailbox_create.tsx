import { useEffect, useMemo, useState } from 'react'
import { Alert, Button, Input, InputNumber, Modal, Radio, Select, Space, Typography } from 'antd'
import { useLingui } from '@lingui/react/macro'

import type { Integration } from '../../services/api/workspace'
import {
  mailboxAccessService,
  type MailboxCreateResult
} from '../../services/api/veridian_mailbox_access'
import { useMailboxLabels } from './veridian_mailbox_labels'
import { ValueRow } from './veridian_mailbox_parts'
import { DEFAULT_DAILY_CAP, defaultSender, emailProfiles } from './veridian_mailbox_rules'

const { Paragraph, Text } = Typography

interface Props {
  open: boolean
  onClose: () => void
  workspaceId: string
  integrations: Integration[] | undefined
  isOwner: boolean
  /** Appelé dès que l'accès existe (l'écran relit la liste). */
  onCreated: () => void
  /** « Régler mon application » : le mot de passe est passé UNE fois, en mémoire seulement. */
  onSetup: (result: MailboxCreateResult) => void
}

export function VeridianMailboxCreate({ open, onClose, workspaceId, integrations, isOwner, onCreated, onSetup }: Props) {
  const { t } = useLingui()
  const labels = useMailboxLabels()
  const profiles = useMemo(() => emailProfiles(integrations), [integrations])

  const [profileId, setProfileId] = useState('')
  const [address, setAddress] = useState('')
  const [label, setLabel] = useState('')
  const [canSend, setCanSend] = useState(true)
  const [cap, setCap] = useState<number | null>(DEFAULT_DAILY_CAP)
  const [forWorkspace, setForWorkspace] = useState(false)
  const [saving, setSaving] = useState(false)
  const [error, setError] = useState('')
  const [created, setCreated] = useState<MailboxCreateResult | null>(null)

  const profile = profiles.find((p) => p.id === profileId)
  const senders = profile?.email_provider?.senders ?? []

  // À l'ouverture : formulaire vierge, premier profil et son expéditeur par défaut déjà choisis.
  useEffect(() => {
    if (!open) return
    const first = profiles[0]
    setProfileId(first?.id ?? '')
    setAddress(defaultSender(first))
    setLabel('')
    setCanSend(true)
    setCap(DEFAULT_DAILY_CAP)
    setForWorkspace(false)
    setError('')
    setCreated(null)
    setSaving(false)
    // eslint-disable-next-line react-hooks/exhaustive-deps -- seulement à l'ouverture
  }, [open])

  const chooseProfile = (id: string) => {
    setProfileId(id)
    setAddress(defaultSender(profiles.find((p) => p.id === id)))
  }

  const submit = async () => {
    setError('')
    if (!profileId || !address) {
      setError(t`Choose a sending profile and an address.`)
      return
    }
    if (!label.trim()) {
      setError(t`Give this access a name, for example Phone or CRM.`)
      return
    }
    if (canSend && (!cap || cap < 1)) {
      setError(t`Set a daily sending limit of at least 1.`)
      return
    }
    setSaving(true)
    try {
      const result = await mailboxAccessService.create({
        workspace_id: workspaceId,
        profile_id: profileId,
        address,
        label: label.trim(),
        can_send: canSend,
        daily_cap: cap ?? DEFAULT_DAILY_CAP,
        shared: forWorkspace
      })
      setCreated(result)
      onCreated()
    } catch (err) {
      setError(labels.errorOf(err))
    } finally {
      setSaving(false)
    }
  }

  // Fermer = oublier le mot de passe (il n'existe plus nulle part côté console).
  const close = () => {
    setCreated(null)
    onClose()
  }

  return (
    <Modal
      open={open}
      onCancel={close}
      maskClosable={false}
      destroyOnHidden
      width="min(520px, calc(100vw - 32px))"
      title={created ? t`Access created` : t`Create an access`}
      footer={
        created ? (
          <Space wrap>
            <Button onClick={close}>{t`Done`}</Button>
            <Button type="primary" onClick={() => onSetup(created)}>
              {t`Set up my app`}
            </Button>
          </Space>
        ) : (
          <Space wrap>
            <Button onClick={close}>{t`Cancel`}</Button>
            <Button type="primary" loading={saving} disabled={profiles.length === 0} onClick={submit}>
              {t`Create`}
            </Button>
          </Space>
        )
      }
    >
      {created ? (
        <div>
          <Alert
            type="warning"
            showIcon
            role="alert"
            message={t`This password is shown only once.`}
            description={t`Copy it now and keep it in your password manager. It cannot be shown again: if you lose it, create a new access.`}
            style={{ marginBottom: 12 }}
          />
          <ValueRow label={t`Username`} value={created.username} />
          <ValueRow label={t`Password`} value={created.password} secret />
        </div>
      ) : profiles.length === 0 ? (
        <Alert
          type="info"
          showIcon
          message={t`No sending profile yet. Add one in Sending profiles first, then come back here.`}
        />
      ) : (
        <Space direction="vertical" size={16} style={{ width: '100%' }}>
          {error && <Alert type="error" showIcon role="alert" message={error} />}
          <div>
            <label htmlFor="mbx-create-profile">{t`Sending profile`}</label>
            <Select
              id="mbx-create-profile"
              style={{ width: '100%' }}
              value={profileId || undefined}
              onChange={chooseProfile}
              options={profiles.map((p) => ({ value: p.id, label: p.name }))}
            />
          </div>
          <div>
            <label htmlFor="mbx-create-address">{t`Address`}</label>
            <Select
              id="mbx-create-address"
              style={{ width: '100%' }}
              value={address || undefined}
              onChange={setAddress}
              options={senders.map((s) => ({ value: s.email, label: s.email }))}
            />
          </div>
          <div>
            <label htmlFor="mbx-create-label">{t`Name`}</label>
            <Input
              id="mbx-create-label"
              value={label}
              maxLength={60}
              placeholder={t`Phone, CRM...`}
              autoComplete="off"
              onChange={(e) => setLabel(e.target.value)}
            />
          </div>
          <div>
            <div id="mbx-create-rights">{t`What can it do?`}</div>
            <Radio.Group
              aria-labelledby="mbx-create-rights"
              value={canSend ? 'send' : 'read'}
              onChange={(e) => setCanSend(e.target.value === 'send')}
            >
              <Space direction="vertical">
                <Radio value="send">{t`Read and send`}</Radio>
                <Radio value="read">{t`Read only`}</Radio>
              </Space>
            </Radio.Group>
          </div>
          {canSend && (
            <div>
              <label htmlFor="mbx-create-cap">{t`Sending limit per day`}</label>
              <InputNumber
                id="mbx-create-cap"
                min={1}
                precision={0}
                value={cap}
                onChange={(v) => setCap(typeof v === 'number' ? v : null)}
                style={{ width: '100%' }}
              />
            </div>
          )}
          <div>
            <div id="mbx-create-who">{t`Who is it for?`}</div>
            <Radio.Group
              aria-labelledby="mbx-create-who"
              value={forWorkspace ? 'workspace' : 'me'}
              onChange={(e) => setForWorkspace(e.target.value === 'workspace')}
            >
              <Space direction="vertical">
                <Radio value="me">{t`Me (closes when I leave the workspace)`}</Radio>
                <Radio value="workspace" disabled={!isOwner}>
                  {t`The workspace (shared mailbox or tool)`}
                </Radio>
              </Space>
            </Radio.Group>
            {!isOwner && (
              <Paragraph type="secondary" style={{ margin: '4px 0 0' }}>
                {t`Only the workspace owner can create a shared access: it would outlive you.`}
              </Paragraph>
            )}
            {isOwner && forWorkspace && (
              <Text type="secondary">{t`A shared access stays open when people leave.`}</Text>
            )}
          </div>
        </Space>
      )}
    </Modal>
  )
}
