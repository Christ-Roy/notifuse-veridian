import { describe, it, expect, vi, beforeEach } from 'vitest'
import { fireEvent, render, screen, waitFor } from '@testing-library/react'
import { App } from 'antd'
import { i18n } from '@lingui/core'
import { I18nProvider } from '@lingui/react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'

vi.mock('../../router', () => ({ router: { navigate: vi.fn() } }))
vi.mock('../../services/api/client', () => {
  class ApiError extends Error {
    constructor(
      message: string,
      public status: number,
      public data?: unknown
    ) {
      super(message)
      this.name = 'ApiError'
    }
  }
  return { ApiError, api: { get: vi.fn(), post: vi.fn() } }
})

import { ApiError, api } from '../../services/api/client'
import type { SendingDomain, SendingDomainList } from '../../services/api/veridian_sending_domains'
import { VeridianSendingDomains } from './veridian_sending_domains'

i18n.loadAndActivate({ locale: 'en', messages: {} })

const DKIM_VALUE = 'v=DKIM1; k=rsa; p=MIIBIjANBgkqhkiG9w0BAQEFAAOCAQ8AMIIBCgKCAQEAtest'

const dom = (over: Partial<SendingDomain> = {}): SendingDomain => ({
  id: 'd1',
  workspace_id: 'ws1',
  domain: 'client.fr',
  selector: 'vd202611abcdef',
  status: 'pending',
  complete: false,
  checks: [{ kind: 'dkim', ok: false, verdict: 'missing', detail: 'aucun enregistrement TXT' }],
  records: [
    { kind: 'dkim', type: 'TXT', name: 'vd202611abcdef._domainkey.client.fr', value: DKIM_VALUE, required: true },
    { kind: 'spf', type: 'TXT', name: 'client.fr', value: 'v=spf1 include:_spf-envoi.veridian.site ~all', required: true },
    { kind: 'dmarc', type: 'TXT', name: '_dmarc.client.fr', value: 'v=DMARC1; p=none; adkim=r; aspf=r', required: false }
  ],
  created_by: 'humain:u1',
  created_at: '2026-11-26T10:00:00Z',
  ...over
})

function mockApi(list: SendingDomainList | Error, post: (url: string, body: unknown) => unknown = () => ({})) {
  vi.mocked(api.get).mockImplementation(async () => {
    if (list instanceof Error) throw list
    return list
  })
  vi.mocked(api.post).mockImplementation(async (url: string, body: unknown) => {
    const r = post(url, body)
    if (r instanceof Error) throw r
    return r
  })
}

function renderScreen() {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  return render(
    <QueryClientProvider client={client}>
      <I18nProvider i18n={i18n}>
        <App>
          <VeridianSendingDomains workspaceId="ws1" />
        </App>
      </I18nProvider>
    </QueryClientProvider>
  )
}

beforeEach(() => vi.clearAllMocks())

describe('VeridianSendingDomains', () => {
  it('shows the exact records to publish and the server verdicts', async () => {
    mockApi({ domains: [dom()], spf_include: '_spf-envoi.veridian.site' })
    renderScreen()
    expect(await screen.findByText('Waiting for DNS')).toBeTruthy()
    expect(screen.getAllByText('client.fr')).toHaveLength(2) // titre de la carte et nom du SPF
    expect(screen.getByText('vd202611abcdef._domainkey.client.fr')).toBeTruthy()
    expect(screen.getByText(DKIM_VALUE)).toBeTruthy()
    expect(screen.getByText('v=spf1 include:_spf-envoi.veridian.site ~all')).toBeTruthy()
    expect(screen.getByText('Not found')).toBeTruthy()
    expect(screen.getByText('aucun enregistrement TXT')).toBeTruthy()
    expect(vi.mocked(api.get).mock.calls[0][0]).toBe('/api/veridian/sendingDomains.list?workspace_id=ws1')
  })

  it('adds a domain, then verifies it with the workspace id', async () => {
    mockApi({ domains: [dom()], spf_include: '_spf-envoi.veridian.site' })
    renderScreen()
    await screen.findByText('Waiting for DNS')
    fireEvent.change(screen.getByLabelText('Domain name'), { target: { value: ' autre.fr ' } })
    fireEvent.click(screen.getByRole('button', { name: /Add domain/ }))
    await waitFor(() =>
      expect(api.post).toHaveBeenCalledWith('/api/veridian/sendingDomains.create', { workspace_id: 'ws1', domain: 'autre.fr' })
    )
    fireEvent.click(screen.getByRole('button', { name: /Verify/ }))
    await waitFor(() =>
      expect(api.post).toHaveBeenCalledWith('/api/veridian/sendingDomains.verify', { workspace_id: 'ws1', id: 'd1' })
    )
  })

  it('says verified and complete when the server says so', async () => {
    mockApi({
      domains: [
        dom({
          status: 'verified',
          complete: true,
          last_checked_at: '2026-11-26T10:05:00Z',
          checks: [
            { kind: 'dkim', ok: true, verdict: 'pass', detail: '' },
            { kind: 'spf', ok: true, verdict: 'pass', detail: '' },
            { kind: 'dmarc', ok: true, verdict: 'pass', detail: '' }
          ]
        })
      ],
      spf_include: '_spf-envoi.veridian.site'
    })
    renderScreen()
    expect(await screen.findByText('Verified: emails are signed')).toBeTruthy()
    expect(screen.getByText('DKIM, SPF and DMARC in place')).toBeTruthy()
    expect(screen.getAllByText('In place')).toHaveLength(3)
  })

  it('explains a refused domain name', async () => {
    mockApi({ domains: [], spf_include: '' }, () => new ApiError('invalid', 400))
    renderScreen()
    await screen.findByText('No sending domain yet.')
    fireEvent.change(screen.getByLabelText('Domain name'), { target: { value: 'fr' } })
    fireEvent.click(screen.getByRole('button', { name: /Add domain/ }))
    expect(await screen.findByText('This is not a valid domain name (example: yourcompany.com).')).toBeTruthy()
  })
})
