import { useState } from 'react'
import { useQuery } from '@tanstack/react-query'
import { Alert, App, Button, Card, Empty, Input, Popconfirm, Skeleton, Space, Tag, Typography } from 'antd'
import { CheckCircleOutlined, DeleteOutlined, PlusOutlined, ReloadOutlined, SafetyCertificateOutlined } from '@ant-design/icons'
import { useLingui } from '@lingui/react/macro'

import dayjs from '../../lib/dayjs'
import { ApiError } from '../../services/api/client'
import {
  checkOf,
  sendingDomainsService,
  spfInclude,
  type DomainCheck,
  type DomainRecord,
  type SendingDomain
} from '../../services/api/veridian_sending_domains'

const { Paragraph, Text } = Typography

interface Props {
  workspaceId: string
}

// Réglages > « Sending domains » (VER-166, fiche 97) : déclarer son domaine, recopier les enregistrements DNS, vérifier. Une fois DKIM
// vérifié, chaque mail dont l'expéditeur est sur ce domaine part signé à son nom. Cartes plutôt qu'un tableau : les valeurs DKIM sont
// longues, rien ne doit déborder sur un téléphone.
export function VeridianSendingDomains({ workspaceId }: Props) {
  const { t } = useLingui()
  const { message } = App.useApp()
  const [draft, setDraft] = useState('')
  const [pending, setPending] = useState('')

  const query = useQuery({
    queryKey: ['veridian-sending-domains', workspaceId],
    queryFn: () => sendingDomainsService.list(workspaceId),
    retry: false
  })
  const domains = query.data?.domains ?? []

  const errorText = (err: unknown): string => {
    if (err instanceof ApiError) {
      if (err.status === 403) return t`You need the right to edit this workspace's settings.`
      if (err.status === 409) return t`This domain is already declared in this workspace.`
      if (err.status === 400) return t`This is not a valid domain name (example: yourcompany.com).`
    }
    return t`Something went wrong. Try again in a moment.`
  }

  const run = async (key: string, fn: () => Promise<unknown>, done?: string) => {
    setPending(key)
    try {
      await fn()
      if (done) message.success(done)
      await query.refetch()
    } catch (err) {
      message.error(errorText(err))
    } finally {
      setPending('')
    }
  }

  const add = () => {
    const name = draft.trim()
    if (!name) return
    void run('add', async () => {
      await sendingDomainsService.create(workspaceId, name)
      setDraft('')
    }, t`Domain added. Publish the records below, then click Verify.`)
  }

  return (
    <div>
      <h2 style={{ marginBottom: 8 }}>{t`Sending domains`}</h2>
      <Paragraph type="secondary">
        {t`Send from your own domain. Add it here, publish the DNS records at your DNS host, then verify: every email whose sender is on this domain will be signed in your name (DKIM), which is what Gmail and Outlook check first.`}
      </Paragraph>

      <Space.Compact style={{ width: '100%', maxWidth: 520 }}>
        <Input
          aria-label={t`Domain name`}
          placeholder={t`yourcompany.com`}
          value={draft}
          onChange={(e) => setDraft(e.target.value)}
          onPressEnter={add}
        />
        <Button type="primary" icon={<PlusOutlined />} loading={pending === 'add'} onClick={add} disabled={!draft.trim()}>
          {t`Add domain`}
        </Button>
      </Space.Compact>

      <div style={{ marginTop: 16 }}>
        {query.isLoading ? (
          <div role="status" aria-label={t`Loading`}>
            <Skeleton active paragraph={{ rows: 3 }} />
          </div>
        ) : query.isError ? (
          <Alert
            type="error"
            showIcon
            role="alert"
            message={errorText(query.error)}
            action={
              <Button size="small" icon={<ReloadOutlined />} onClick={() => query.refetch()}>
                {t`Try again`}
              </Button>
            }
          />
        ) : domains.length === 0 ? (
          <Empty description={t`No sending domain yet.`} />
        ) : (
          <ul style={{ listStyle: 'none', padding: 0, margin: 0, display: 'grid', gap: 16 }}>
            {domains.map((d) => (
              <li key={d.id}>
                <DomainCard
                  domain={d}
                  busy={pending === d.id}
                  onVerify={() => run(d.id, () => sendingDomainsService.verify(workspaceId, d.id))}
                  onRemove={() => run(d.id, () => sendingDomainsService.remove(workspaceId, d.id), t`Domain removed.`)}
                />
              </li>
            ))}
          </ul>
        )}
      </div>
    </div>
  )
}

function DomainCard({ domain, busy, onVerify, onRemove }: { domain: SendingDomain; busy: boolean; onVerify: () => void; onRemove: () => void }) {
  const { t } = useLingui()
  const verified = domain.status === 'verified'
  return (
    <Card
      size="small"
      title={
        <Space wrap>
          <SafetyCertificateOutlined />
          <Text strong>{domain.domain}</Text>
          {verified ? (
            <Tag color="success" icon={<CheckCircleOutlined />}>
              {t`Verified: emails are signed`}
            </Tag>
          ) : domain.lost_at ? (
            <Tag color="error">{t`Signing stopped`}</Tag>
          ) : (
            <Tag color="warning">{t`Waiting for DNS`}</Tag>
          )}
          {domain.complete && <Tag color="success">{t`DKIM, SPF and DMARC in place`}</Tag>}
        </Space>
      }
      extra={
        <Space wrap>
          <Button type="primary" icon={<ReloadOutlined />} loading={busy} onClick={onVerify}>
            {t`Verify`}
          </Button>
          <Popconfirm
            title={t`Remove this domain?`}
            description={t`Its key is deleted: emails from this domain will no longer be signed by us.`}
            okText={t`Remove`}
            cancelText={t`Cancel`}
            onConfirm={onRemove}
          >
            <Button danger icon={<DeleteOutlined />} disabled={busy} aria-label={t`Remove`} />
          </Popconfirm>
        </Space>
      }
    >
      {!verified && domain.lost_at && (
        <Alert
          type="error"
          showIcon
          role="alert"
          style={{ marginBottom: 12 }}
          message={t`Signing stopped on ${dayjs(domain.lost_at).format('LLL')}`}
          description={
            domain.lost_reason === 'mismatch'
              ? t`The DKIM record of this domain no longer carries our key. Emails from this domain now leave without our signature. Publish the DKIM value below again, then click Verify.`
              : t`The DKIM record of this domain was removed. Emails from this domain now leave without our signature. Publish the DKIM record below again, then click Verify.`
          }
        />
      )}
      <Paragraph type="secondary" style={{ marginBottom: 12 }}>
        {domain.last_checked_at
          ? t`Last checked ${dayjs(domain.last_checked_at).fromNow()}.`
          : t`Not checked yet: publish the records, then click Verify (DNS changes can take a few minutes).`}
      </Paragraph>
      <div style={{ display: 'grid', gap: 12 }}>
        {(domain.records ?? []).map((r) => (
          <RecordBlock key={r.kind} record={r} check={checkOf(domain, r.kind)} />
        ))}
      </div>
    </Card>
  )
}

function RecordBlock({ record, check }: { record: DomainRecord; check?: DomainCheck }) {
  const { t } = useLingui()
  const kindLabel = record.kind === 'dkim' ? t`DKIM signature` : record.kind === 'spf' ? t`SPF (authorized senders)` : t`DMARC policy`
  const verdictLabel = (c: DomainCheck): string => {
    switch (c.verdict) {
      case 'pass':
        return t`In place`
      case 'inherited':
        return t`Inherited from the parent domain`
      case 'missing':
        return t`Not found`
      case 'mismatch':
        return t`Different from the expected value`
      case 'multiple':
        return t`Several SPF records (invalid)`
      case 'temperror':
        return t`DNS unreachable, try again`
      default:
        return c.verdict
    }
  }
  // Conseils et motifs traduits ici, dans la langue du compte (le serveur ne rend que des codes de verdict exploitables).
  const include = record.kind === 'spf' ? spfInclude(record.value) : ''
  const shortName = record.name.split('.').slice(0, 2).join('.')
  const note =
    record.kind === 'dkim'
      ? t`Signs your emails. Publish it as is; some DNS hosts want the name without the domain (${shortName}).`
      : record.kind === 'spf'
        ? t`Allows our relays to send for you. A domain has only one SPF record: if one already exists, add "include:${include}" to it, before the "all".`
        : t`Recommended. Start with p=none, then move to p=quarantine once the reports are clean. If one already exists, keep it.`
  const verdictDetail = (c: DomainCheck): string => {
    switch (c.verdict) {
      case 'missing':
        return record.kind === 'dkim'
          ? t`No TXT record at this name yet.`
          : record.kind === 'spf'
            ? t`This domain has no SPF record.`
            : t`No DMARC policy: recommended, without it Gmail and Yahoo filter more strictly.`
      case 'mismatch':
        return record.kind === 'dkim'
          ? t`A TXT record exists but does not carry our public key: copy the whole value again.`
          : t`The SPF record does not authorize our relays: add include:${include}.`
      case 'multiple':
        return t`Several SPF records: receivers ignore all of them. Merge them into one.`
      case 'temperror':
        return t`DNS unreachable, nothing concluded: try again in a moment.`
      default:
        return ''
    }
  }
  return (
    <div style={{ border: '1px solid rgba(0,0,0,0.08)', borderRadius: 8, padding: 12, minWidth: 0 }}>
      <Space wrap style={{ marginBottom: 8 }}>
        <Text strong>{kindLabel}</Text>
        <Tag>{record.required ? t`Required` : t`Recommended`}</Tag>
        {check ? <Tag color={check.ok ? 'success' : check.verdict === 'temperror' ? 'default' : 'error'}>{verdictLabel(check)}</Tag> : <Tag>{t`Not checked`}</Tag>}
      </Space>
      {check && !check.ok && (
        <Paragraph type={check.verdict === 'temperror' ? 'secondary' : 'danger'} style={{ marginBottom: 8 }}>
          {verdictDetail(check)}
        </Paragraph>
      )}
      <div style={{ display: 'grid', gridTemplateColumns: 'minmax(70px, max-content) minmax(0, 1fr)', gap: '4px 12px', alignItems: 'start' }}>
        <Text type="secondary">{t`Type`}</Text>
        <Text code>{record.type}</Text>
        <Text type="secondary">{t`Name`}</Text>
        <Text code copyable={{ text: record.name }} style={{ wordBreak: 'break-all' }}>
          {record.name}
        </Text>
        <Text type="secondary">{t`Value`}</Text>
        <Text code copyable={{ text: record.value }} style={{ wordBreak: 'break-all' }}>
          {record.value}
        </Text>
      </div>
      <Paragraph type="secondary" style={{ marginTop: 8, marginBottom: 0, fontSize: 13 }}>
        {note}
      </Paragraph>
    </div>
  )
}
