import { useEffect, useState } from 'react'
import {
  Alert,
  Button,
  Card,
  Checkbox,
  Drawer,
  Form,
  Input,
  InputNumber,
  Radio,
  Result,
  Space,
  Spin,
  Switch,
  Tag,
  Typography
} from 'antd'
import { useLingui } from '@lingui/react/macro'

import {
  emailProfilesCreateService,
  type CreateEmailProfileRequest
} from '../../services/api/veridian_email_profiles'
import {
  GMAIL_PERSONAL_DEFAULT_DAILY_CAP,
  gmailAccountTypeMaxDailyCap,
  type GmailAccountType
} from '../settings/veridian_email_profiles'
import { setRotation, setUsage, testProfile } from './veridian_profile_ops'

const { Text, Paragraph, Link } = Typography

export const GMAIL_APP_PASSWORDS_URL = 'https://myaccount.google.com/apppasswords'
export const GMAIL_TWO_STEP_URL = 'https://myaccount.google.com/signinoptions/two-step-verification'

type Choice = 'smtp_imap' | 'gmail_app_password'
type Stage = 'choose' | 'form' | 'testing' | 'result'

interface FormValues {
  name?: string
  sender_email: string
  sender_name?: string
  // Gmail
  app_password?: string
  account_type?: GmailAccountType
  daily_cap?: number
  // SMTP
  smtp_host?: string
  smtp_port?: number
  smtp_tls?: boolean
  smtp_user?: string
  smtp_password?: string
  link_inbox?: boolean
  imap_host?: string
  imap_port?: number
  imap_tls?: boolean
  imap_user?: string
  imap_same_password?: boolean
  imap_password?: string
}

interface Props {
  open: boolean
  workspaceId: string
  // Adresse du propriétaire : destinataire du test de transport.
  ownerEmail: string
  onClose: () => void
  // Appelé quand le profil existe (créé, avec ou sans test réussi) : la page se rafraîchit.
  onChanged: () => void
}

export function VeridianProfileWizard({ open, workspaceId, ownerEmail, onClose, onChanged }: Props) {
  const { t } = useLingui()
  const [form] = Form.useForm<FormValues>()
  const [stage, setStage] = useState<Stage>('choose')
  const [choice, setChoice] = useState<Choice | null>(null)
  const [submitting, setSubmitting] = useState(false)
  const [error, setError] = useState<string | null>(null)
  const [created, setCreated] = useState<{ id: string } | null>(null)
  const [testOk, setTestOk] = useState<boolean | null>(null)
  const [testError, setTestError] = useState<string | null>(null)
  const [applying, setApplying] = useState(false)

  const linkInbox = Form.useWatch('link_inbox', form)
  const sameImapPassword = Form.useWatch('imap_same_password', form)
  const accountType = Form.useWatch('account_type', form) ?? 'personal'
  const smtpHost = Form.useWatch('smtp_host', form)
  const smtpUser = Form.useWatch('smtp_user', form)

  // L'IMAP de retour se préremplit sur le même hôte et le même identifiant tant
  // que l'utilisateur ne l'a pas modifié lui-même.
  useEffect(() => {
    if (choice !== 'smtp_imap') return
    if (!form.isFieldTouched('imap_host')) form.setFieldValue('imap_host', smtpHost)
    if (!form.isFieldTouched('imap_user')) form.setFieldValue('imap_user', smtpUser)
  }, [choice, smtpHost, smtpUser, form])

  const reset = () => {
    form.resetFields()
    setStage('choose')
    setChoice(null)
    setSubmitting(false)
    setError(null)
    setCreated(null)
    setTestOk(null)
    setTestError(null)
    setApplying(false)
  }

  const close = () => {
    reset()
    onClose()
  }

  const pick = (next: Choice) => {
    setChoice(next)
    setError(null)
    form.resetFields()
    if (next === 'gmail_app_password') {
      form.setFieldsValue({ account_type: 'personal', daily_cap: GMAIL_PERSONAL_DEFAULT_DAILY_CAP })
    } else {
      form.setFieldsValue({
        smtp_port: 587,
        smtp_tls: true,
        link_inbox: true,
        imap_port: 993,
        imap_tls: true,
        imap_same_password: true
      })
    }
    setStage('form')
  }

  const buildRequest = (values: FormValues): CreateEmailProfileRequest => {
    const senderEmail = values.sender_email.trim()
    const base = {
      workspace_id: workspaceId,
      name: (values.name || senderEmail).trim(),
      sender_email: senderEmail,
      sender_name: (values.sender_name || '').trim()
    }
    if (choice === 'gmail_app_password') {
      return {
        ...base,
        type: 'gmail_app_password',
        app_password: (values.app_password || '').replace(/\s/g, ''),
        gmail_account_type: values.account_type ?? 'personal',
        profile_daily_cap: values.daily_cap ?? GMAIL_PERSONAL_DEFAULT_DAILY_CAP
      }
    }
    const request: CreateEmailProfileRequest = {
      ...base,
      type: 'smtp_imap',
      smtp: {
        host: (values.smtp_host || '').trim(),
        port: values.smtp_port ?? 587,
        use_tls: values.smtp_tls ?? true,
        username: (values.smtp_user || '').trim(),
        password: values.smtp_password || ''
      }
    }
    if (values.link_inbox) {
      request.imap = {
        host: (values.imap_host || '').trim(),
        port: values.imap_port ?? 993,
        use_tls: values.imap_tls ?? true,
        username: (values.imap_user || '').trim(),
        password: values.imap_same_password ? values.smtp_password || '' : values.imap_password || ''
      }
    }
    return request
  }

  const submit = async (values: FormValues) => {
    setSubmitting(true)
    setError(null)
    try {
      const response = await emailProfilesCreateService.create(buildRequest(values))
      // Le secret a fait son aller simple : on le retire du formulaire avant tout autre affichage.
      form.resetFields()
      setCreated({ id: response.integration_id })
      onChanged()
      setStage('testing')
      try {
        const test = await testProfile(workspaceId, response.integration_id, ownerEmail)
        setTestOk(!!test.success)
        setTestError(test.success ? null : test.error || null)
      } catch (err) {
        setTestOk(false)
        setTestError(err instanceof Error ? err.message : null)
      }
      onChanged()
      setStage('result')
    } catch (err) {
      setError(err instanceof Error ? err.message : t`Could not create the profile`)
    } finally {
      setSubmitting(false)
    }
  }

  const apply = async (target: 'rotation' | 'transactional' | 'later') => {
    if (!created) return
    if (target === 'later') {
      close()
      return
    }
    setApplying(true)
    setError(null)
    try {
      if (target === 'rotation') await setRotation(workspaceId, created.id, true)
      else await setUsage(workspaceId, created.id, 'transactional')
      onChanged()
      close()
    } catch (err) {
      setError(err instanceof Error ? err.message : t`Could not apply the choice`)
      setApplying(false)
    }
  }

  const gmailMax = gmailAccountTypeMaxDailyCap(accountType)

  const chooser = (
    <Space direction="vertical" size={12} style={{ width: '100%' }}>
      <Card
        hoverable
        size="small"
        data-testid="choice-smtp-imap"
        onClick={() => pick('smtp_imap')}
        title={t`SMTP + IMAP`}
      >
        <Text>{t`Any mail server: host, port, login and password. Optional return inbox over IMAP.`}</Text>
      </Card>
      <Card
        hoverable
        size="small"
        data-testid="choice-gmail-app-password"
        onClick={() => pick('gmail_app_password')}
        title={t`Gmail with an app password`}
      >
        <Text>{t`Your Gmail address and a 16-character app password. SMTP and IMAP are set up together.`}</Text>
      </Card>
      <Card
        size="small"
        data-testid="choice-gmail-oauth"
        style={{ opacity: 0.55, cursor: 'not-allowed' }}
        title={
          <Space>
            {t`Gmail with OAuth`}
            <Tag>{t`Soon`}</Tag>
          </Space>
        }
        aria-disabled="true"
      >
        <Text type="secondary">{t`Sign in with Google, without any password. Not available yet.`}</Text>
      </Card>
    </Space>
  )

  const gmailForm = (
    <>
      <Alert
        type="info"
        showIcon
        style={{ marginBottom: 16 }}
        message={t`Create a Gmail app password in 3 steps`}
        description={
          <ol style={{ paddingLeft: 18, margin: '8px 0 0' }}>
            <li>
              {t`Turn on 2-step verification on the Google account.`}{' '}
              <Link href={GMAIL_TWO_STEP_URL} target="_blank" rel="noopener noreferrer">
                {t`Open 2-step verification`}
              </Link>
            </li>
            <li>{t`Open the app passwords page and create a password named “Notifuse”.`}</li>
            <li>{t`Copy the 16 characters and paste them below. Your usual Gmail password does not work.`}</li>
          </ol>
        }
      />
      <Button
        type="primary"
        href={GMAIL_APP_PASSWORDS_URL}
        target="_blank"
        rel="noopener noreferrer"
        style={{ marginBottom: 16 }}
      >
        {t`Open Google app passwords`}
      </Button>
      <Form.Item
        name="sender_email"
        label={t`Gmail address`}
        rules={[{ required: true, type: 'email', message: t`Enter a valid address` }]}
      >
        <Input autoComplete="off" placeholder="prenom.nom@gmail.com" />
      </Form.Item>
      <Form.Item name="sender_name" label={t`Display name`}>
        <Input autoComplete="off" />
      </Form.Item>
      <Form.Item
        name="app_password"
        label={t`App password (16 characters)`}
        rules={[
          { required: true, message: t`Paste the app password` },
          {
            validator: (_, value?: string) =>
              !value || value.replace(/\s/g, '').length === 16
                ? Promise.resolve()
                : Promise.reject(new Error(t`The app password has exactly 16 characters`))
          }
        ]}
      >
        <Input.Password autoComplete="new-password" placeholder="abcd efgh ijkl mnop" />
      </Form.Item>
      <Form.Item name="account_type" label={t`Account type`}>
        <Radio.Group>
          <Radio value="personal">{t`Personal Gmail`}</Radio>
          <Radio value="workspace">{t`Google Workspace`}</Radio>
        </Radio.Group>
      </Form.Item>
      <Form.Item
        name="daily_cap"
        label={t`Daily cap (messages per day)`}
        extra={t`30 per day to start. Raise it gradually, the maximum is ${gmailMax}.`}
      >
        <InputNumber min={1} max={gmailMax} precision={0} style={{ width: 160 }} />
      </Form.Item>
      <Alert
        type="warning"
        showIcon
        message={t`IMAP must be enabled in Gmail (Settings, Forwarding and POP/IMAP) for replies and bounces to be read.`}
      />
    </>
  )

  const smtpForm = (
    <>
      <Form.Item name="name" label={t`Profile name`}>
        <Input autoComplete="off" />
      </Form.Item>
      <Form.Item
        name="sender_email"
        label={t`Sender address`}
        rules={[{ required: true, type: 'email', message: t`Enter a valid address` }]}
      >
        <Input autoComplete="off" />
      </Form.Item>
      <Form.Item name="sender_name" label={t`Display name`}>
        <Input autoComplete="off" />
      </Form.Item>
      <Text strong>{t`Outgoing server (SMTP)`}</Text>
      <Space align="start" wrap style={{ width: '100%', marginTop: 8 }}>
        <Form.Item name="smtp_host" label={t`Host`} rules={[{ required: true, message: t`Required` }]}>
          <Input autoComplete="off" style={{ width: 240 }} />
        </Form.Item>
        <Form.Item name="smtp_port" label={t`Port`} rules={[{ required: true, message: t`Required` }]}>
          <InputNumber min={1} max={65535} precision={0} />
        </Form.Item>
        <Form.Item name="smtp_tls" label={t`TLS`} valuePropName="checked">
          <Switch />
        </Form.Item>
      </Space>
      <Form.Item name="smtp_user" label={t`Login`} rules={[{ required: true, message: t`Required` }]}>
        <Input autoComplete="off" />
      </Form.Item>
      <Form.Item
        name="smtp_password"
        label={t`Password`}
        rules={[{ required: true, message: t`Required` }]}
      >
        <Input.Password autoComplete="new-password" />
      </Form.Item>
      <Form.Item name="link_inbox" valuePropName="checked">
        <Checkbox>{t`Link a return inbox (IMAP) to read replies and bounces`}</Checkbox>
      </Form.Item>
      {linkInbox && (
        <div data-testid="imap-block">
          <Space align="start" wrap style={{ width: '100%' }}>
            <Form.Item
              name="imap_host"
              label={t`IMAP host`}
              rules={[{ required: true, message: t`Required` }]}
            >
              <Input autoComplete="off" style={{ width: 240 }} />
            </Form.Item>
            <Form.Item name="imap_port" label={t`Port`}>
              <InputNumber min={1} max={65535} precision={0} />
            </Form.Item>
            <Form.Item name="imap_tls" label={t`TLS`} valuePropName="checked">
              <Switch />
            </Form.Item>
          </Space>
          <Form.Item
            name="imap_user"
            label={t`IMAP login`}
            rules={[{ required: true, message: t`Required` }]}
          >
            <Input autoComplete="off" />
          </Form.Item>
          <Form.Item name="imap_same_password" valuePropName="checked">
            <Checkbox>{t`Same password as SMTP`}</Checkbox>
          </Form.Item>
          {!sameImapPassword && (
            <Form.Item
              name="imap_password"
              label={t`IMAP password`}
              rules={[{ required: true, message: t`Required` }]}
            >
              <Input.Password autoComplete="new-password" />
            </Form.Item>
          )}
        </div>
      )}
    </>
  )

  const body = (() => {
    if (stage === 'choose') return chooser
    if (stage === 'testing') {
      return (
        <div style={{ textAlign: 'center', padding: 32 }}>
          <Spin />
          <Paragraph style={{ marginTop: 16 }}>
            {t`Profile created. Sending a test message to ${ownerEmail}...`}
          </Paragraph>
        </div>
      )
    }
    if (stage === 'result') {
      return testOk ? (
        <Result
          status="success"
          title={t`Transport verified`}
          subTitle={t`A test message was sent to ${ownerEmail}. Where should this profile be used?`}
          extra={
            <Space direction="vertical" style={{ width: '100%' }}>
              <Button type="primary" block loading={applying} onClick={() => apply('rotation')}>
                {t`Add to the commercial rotation`}
              </Button>
              <Button block loading={applying} onClick={() => apply('transactional')}>
                {t`Reserve for transactional mail`}
              </Button>
              <Button block disabled={applying} onClick={() => apply('later')}>
                {t`Decide later`}
              </Button>
            </Space>
          }
        />
      ) : (
        <Result
          status="warning"
          title={t`Profile created, but the test failed`}
          subTitle={testError || t`The server refused the test message.`}
          extra={
            <Button type="primary" onClick={close}>
              {t`Close, then edit or retry the test from the card`}
            </Button>
          }
        />
      )
    }
    return (
      <Form
        form={form}
        layout="vertical"
        onFinish={submit}
        requiredMark={false}
        autoComplete="off"
        disabled={submitting}
      >
        {choice === 'gmail_app_password' ? gmailForm : smtpForm}
        {error && <Alert type="error" showIcon message={error} style={{ margin: '12px 0' }} />}
        <Space style={{ marginTop: 16 }}>
          <Button onClick={() => setStage('choose')} disabled={submitting}>
            {t`Back`}
          </Button>
          <Button type="primary" htmlType="submit" loading={submitting}>
            {t`Create and test`}
          </Button>
        </Space>
      </Form>
    )
  })()

  return (
    <Drawer
      open={open}
      onClose={close}
      width={560}
      destroyOnClose
      title={t`Add a sending profile`}
      maskClosable={!submitting && stage !== 'testing'}
    >
      {stage === 'result' && error && <Alert type="error" showIcon message={error} style={{ marginBottom: 12 }} />}
      {body}
    </Drawer>
  )
}
