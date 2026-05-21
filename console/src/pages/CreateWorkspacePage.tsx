import { useEffect, useState } from 'react'
import { Form, Input, Button, Tooltip, App, Result, Spin } from 'antd'
import { useNavigate } from '@tanstack/react-router'
import { InfoCircleOutlined, ArrowLeftOutlined, LoginOutlined } from '@ant-design/icons'
import { workspaceService } from '../services/api/workspace'
import { ApiError } from '../services/api/client'
import { veridianApi, type VeridianModeResponse } from '../services/api/veridian'
import { useAuth } from '../contexts/AuthContext'
import { MainLayout, MainLayoutSidebar } from '../layouts/MainLayout'
import { getBrowserTimezone } from '../lib/timezoneNormalizer'
import { getBrowserLanguage } from '../lib/languages'
import { useLingui } from '@lingui/react/macro'

export function CreateWorkspacePage() {
  const { t } = useLingui()
  const navigate = useNavigate()
  const [loading, setLoading] = useState(false)
  const [form] = Form.useForm()
  const { refreshWorkspaces } = useAuth()
  const { message } = App.useApp()

  // === Veridian patch === Detection mode managed pour switch UI.
  // En mode "veridian-managed", l'utilisateur n'a pas le droit de creer un
  // workspace (provisionne par le Hub). On affiche un ecran qui le renvoie
  // vers /console/signin (magic link) ou vers app.veridian.site pour souscrire.
  const [veridianMode, setVeridianMode] = useState<VeridianModeResponse | null>(null)
  const [modeLoading, setModeLoading] = useState(true)

  useEffect(() => {
    veridianApi
      .getMode()
      .then((res) => setVeridianMode(res))
      .catch(() => setVeridianMode({ mode: 'self-hosted', signin_url: '/console/signin' }))
      .finally(() => setModeLoading(false))
  }, [])

  // Generate workspace ID from name (alphanumeric only, max 20 chars)
  const generateWorkspaceId = (name: string) => {
    if (!name) return ''
    // remove spaces and remove non-alphanumeric characters
    return name
      .toLowerCase()
      .replace(/[^a-z0-9]/g, '')
      .substring(0, 20)
  }

  // Update generated ID when name changes
  const handleNameChange = (e: React.ChangeEvent<HTMLInputElement>) => {
    const name = e.target.value
    const id = generateWorkspaceId(name)
    form.setFieldsValue({ id })
  }

  const onFinish = async (values: { name: string; id: string; website_url?: string }) => {
    try {
      setLoading(true)
      let logoUrl = null
      let coverUrl = null

      // If website URL is provided, detect favicon and cover image
      if (values.website_url) {
        try {
          const faviconResponse = await workspaceService.detectFavicon(values.website_url)
          logoUrl = faviconResponse.iconUrl
          coverUrl = faviconResponse.coverUrl || null
        } catch (error) {
          console.error('Error detecting website assets:', error)
          // Don't fail the whole process if detection fails
        }
      }

      // Get user's timezone (normalized to canonical IANA name)
      const timezone = getBrowserTimezone()
      const detectedLang = getBrowserLanguage()

      // Create workspace with API
      await workspaceService.create({
        id: generateWorkspaceId(values.id),
        name: values.name,
        settings: {
          website_url: values.website_url || '',
          logo_url: logoUrl,
          cover_url: coverUrl,
          timezone: timezone,
          email_tracking_enabled: true,
          default_language: detectedLang,
          languages: [detectedLang]
        }
      })

      await refreshWorkspaces()

      // Navigate to the new workspace
      message.success(t`Workspace "${values.name}" created successfully!`)
      // wait for the refreshWorkspaces to propagate the new workspaces list to the root layout
      window.setTimeout(() => {
        navigate({
          to: '/console/workspace/$workspaceId',
          params: { workspaceId: values.id }
        })
      }, 100)
    } catch (error) {
      console.error('Error creating workspace:', error)
      if (error instanceof ApiError && error.status === 403 && error.message.includes('workspace limit')) {
        // === Veridian patch === pivot pricing 2026-05-21 : pas de copy
        // "Upgrade your plan" visible. La limite workspace côté backend
        // reste un garde-fou (root-only par défaut), mais le wording est
        // neutre. Robert tranchera en session calme si on ajoute un CTA
        // contextuel "Contactez Veridian" — pour l'instant message neutre.
        message.error(t`Unable to create workspace. Please contact your administrator.`)
      } else {
        message.error(error instanceof Error ? error.message : t`Failed to create workspace`)
      }
      setLoading(false)
    }
  }

  const handleBackToDashboard = () => {
    navigate({ to: '/console' })
  }

  // === Veridian patch === Spinner court le temps du fetch /api/veridian/mode.
  if (modeLoading) {
    return (
      <MainLayout>
        <div style={{ display: 'flex', justifyContent: 'center', alignItems: 'center', minHeight: '60vh' }}>
          <Spin size="large" />
        </div>
      </MainLayout>
    )
  }

  // === Veridian patch === Mode "veridian-managed" : workspace creation
  // pilotee uniquement par le Hub via HMAC. La console UI ne propose plus
  // de formulaire mais redirige vers /console/signin (magic link) ou vers
  // le Hub pour souscrire.
  if (veridianMode?.mode === 'veridian-managed') {
    return (
      <MainLayout>
        <div style={{ maxWidth: 640, margin: '60px auto', padding: '0 24px' }}>
          <Result
            icon={<LoginOutlined style={{ color: '#1677ff' }} />}
            title={t`Workspace creation disabled`}
            subTitle={
              <div style={{ marginTop: 12 }}>
                <p>
                  {t`Your Notifuse workspace is provisioned automatically by Veridian when you subscribe at`}{' '}
                  <a href={veridianMode.hub_url} target="_blank" rel="noreferrer">
                    {veridianMode.hub_url?.replace(/^https?:\/\//, '')}
                  </a>
                  .
                </p>
                <p>
                  {t`If you already have a workspace, sign in below with the email you used to subscribe — a secure magic link will be sent to your inbox.`}
                </p>
              </div>
            }
            extra={[
              <Button
                key="signin"
                type="primary"
                size="large"
                icon={<LoginOutlined />}
                // window.location au lieu de navigate({to:...}) car TanStack Router
                // type le `to` selon les routes typées, et un path string dynamique
                // comme `/console/signin` venant de l'API ne matche pas le type.
                // Hard navigation est OK ici : on quitte la page Create Workspace
                // pour Signin (qui doit recharger l'état auth de toute façon).
                onClick={() => {
                  window.location.href = veridianMode.signin_url
                }}
              >
                {t`Sign in with magic link`}
              </Button>,
              veridianMode.hub_url && (
                <Button
                  key="hub"
                  size="large"
                  onClick={() => window.open(veridianMode.hub_url, '_blank', 'noreferrer')}
                >
                  {t`Subscribe at Veridian`}
                </Button>
              )
            ]}
          />
        </div>
      </MainLayout>
    )
  }

  return (
    <MainLayout>
      <MainLayoutSidebar
        title={t`New workspace`}
        extra={
          <Button
            type="primary"
            ghost
            icon={<ArrowLeftOutlined />}
            onClick={handleBackToDashboard}
            style={{ padding: '4px', lineHeight: 1 }}
          />
        }
      >
        <Form
          name="create-workspace"
          layout="vertical"
          onFinish={onFinish}
          autoComplete="off"
          form={form}
          initialValues={{ id: '' }}
        >
          <Form.Item
            label={t`Workspace Name`}
            name="name"
            rules={[
              { required: true, message: t`Please enter a workspace name` },
              { min: 3, message: t`Workspace name must be at least 3 characters long` }
            ]}
          >
            <Input placeholder={t`Enter a name for your workspace`} onChange={handleNameChange} />
          </Form.Item>

          <Form.Item
            label={
              <span>
                {t`Workspace ID`} &nbsp;
                <Tooltip title={t`This ID will be used in URLs and API requests. It can only contain lowercase letters and numbers.`}>
                  <InfoCircleOutlined />
                </Tooltip>
              </span>
            }
            name="id"
            rules={[
              { required: true, message: t`Workspace ID is required` },
              {
                pattern: /^[a-z0-9]+$/,
                message: t`ID can only contain lowercase letters and numbers`
              }
            ]}
          >
            <Input
              placeholder="workspaceid"
              suffix={
                <Tooltip title={t`ID is automatically generated but can be modified if needed`}>
                  <InfoCircleOutlined style={{ color: 'rgba(0,0,0,.45)' }} />
                </Tooltip>
              }
            />
          </Form.Item>

          <Form.Item
            label={t`Website URL`}
            name="website_url"
            rules={[
              {
                pattern: /^(https?:\/\/)?([\da-z.-]+)\.([a-z.]{2,6})([/\w .-]*)*\/?$/,
                message: t`Please enter a valid URL`,
                validateTrigger: 'onBlur'
              }
            ]}
            extra={t`We'll automatically detect and use your website's favicon`}
          >
            <Input placeholder="https://example.com" />
          </Form.Item>

          <Form.Item>
            <Button
              type="primary"
              htmlType="submit"
              loading={loading}
              style={{ width: '100%', marginTop: 20 }}
            >
              {t`Create Workspace`}
            </Button>
          </Form.Item>
        </Form>
      </MainLayoutSidebar>
    </MainLayout>
  )
}
