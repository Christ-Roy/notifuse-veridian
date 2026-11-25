import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { fireEvent, render, screen, waitFor, within } from '@testing-library/react'
import { App } from 'antd'
import { i18n } from '@lingui/core'
import { I18nProvider } from '@lingui/react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'

const navigate = vi.hoisted(() => vi.fn())
vi.mock('../../router', () => ({ router: { navigate } }))
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
import type { Workspace } from '../../services/api/workspace'
import type { MailboxAccess, MailboxAccessList } from '../../services/api/veridian_mailbox_access'
import { VeridianMailboxAccess } from './veridian_mailbox_access'

i18n.loadAndActivate({ locale: 'en', messages: {} })

const PASSWORD = 'S3cret-Passw0rd-xyz'

const access = (over: Partial<MailboxAccess> = {}): MailboxAccess => ({
  id: 'a1',
  workspace_id: 'ws1',
  profile_id: 'p1',
  address: 'ben@example.com',
  label: 'Phone',
  can_send: true,
  daily_cap: 150,
  created_by: 'u1',
  created_at: '2026-11-20T10:00:00Z',
  holder_user_id: 'u1',
  holder_email: 'ben@example.com',
  ...over
})

const list = (over: Partial<MailboxAccessList> = {}): MailboxAccessList => ({
  accesses: [access()],
  connection: {
    imap: { host: 'mail.example.com', port: 993, security: 'ssl' },
    smtp: { host: 'mail.example.com', port: 465, security: 'ssl' }
  },
  attempts: [
    { at: '2026-11-25T09:00:00Z', via: 'imap', address: 'ben@example.com', ok: false, reason: 'bad_credentials' },
    { at: '2026-11-25T09:05:00Z', via: 'smtp', address: 'ben@example.com', ok: true }
  ],
  attempts_since: '2026-11-18T00:00:00Z',
  ...over
})

const workspace = {
  id: 'ws1',
  name: 'WS',
  settings: {},
  integrations: [
    {
      id: 'p1',
      name: 'Main profile',
      type: 'email',
      created_at: '',
      updated_at: '',
      email_provider: {
        kind: 'smtp',
        rate_limit_per_minute: 10,
        senders: [{ id: 's1', email: 'ben@example.com', name: 'Ben', is_default: true }]
      }
    }
  ]
} as unknown as Workspace

function mockApi(data: MailboxAccessList | Error, extra: (url: string, body?: unknown) => unknown = () => undefined) {
  vi.mocked(api.get).mockImplementation(async () => {
    if (data instanceof Error) throw data
    return data
  })
  vi.mocked(api.post).mockImplementation(async (url: string, body: unknown) => {
    const r = extra(url, body)
    if (r instanceof Error) throw r
    return r ?? {}
  })
}

function renderScreen(isOwner = true, ws: Workspace | null = workspace) {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  return render(
    <QueryClientProvider client={client}>
      <I18nProvider i18n={i18n}>
        <App>
          <VeridianMailboxAccess workspaceId="ws1" workspace={ws} isOwner={isOwner} />
        </App>
      </I18nProvider>
    </QueryClientProvider>
  )
}

const writeText = vi.fn().mockResolvedValue(undefined)

beforeEach(() => {
  vi.clearAllMocks()
  localStorage.clear()
  Object.defineProperty(navigator, 'clipboard', { value: { writeText }, configurable: true })
})
afterEach(() => {
  vi.unstubAllGlobals()
  vi.restoreAllMocks()
})

describe('liste des accès', () => {
  it('montre adresse, libellé, titulaire, droits, dernier usage et état ; les révoqués sont repliés', async () => {
    mockApi(
      list({
        accesses: [
          access({ last_used_at: new Date(Date.now() - 3 * 3600 * 1000).toISOString() }),
          access({ id: 'a2', label: 'CRM', holder_user_id: undefined, holder_email: undefined, can_send: false, daily_cap: 0 }),
          access({ id: 'a3', label: 'Old tablet', revoked_at: '2026-11-21T10:00:00Z' })
        ]
      })
    )
    renderScreen()
    expect(await screen.findByText('Phone')).toBeInTheDocument()
    expect(screen.getByText('Read and send, up to 150 per day')).toBeInTheDocument()
    expect(screen.getByText('The workspace (shared)')).toBeInTheDocument()
    expect(screen.getByText('Read only')).toBeInTheDocument()
    expect(screen.getByText('Never')).toBeInTheDocument()
    expect(screen.getByText('3 hours ago')).toBeInTheDocument()
    expect(screen.getAllByText('Active')).toHaveLength(2)
    // révoqué : replié par défaut
    expect(screen.queryByText('Old tablet')).toBeNull()
    fireEvent.click(screen.getByText(/Revoked accesses \(1\)/))
    expect(await screen.findByText('Old tablet')).toBeInTheDocument()
    expect(screen.getByText('Revoked')).toBeInTheDocument()
  })

  it('un accès fermé dit pourquoi et ne propose que de révoquer', async () => {
    mockApi(list({ accesses: [access({ closed: true, closed_reason: 'holder_gone' })] }))
    renderScreen()
    await screen.findByText('Closed')
    expect(screen.getByText(/left the workspace/)).toBeInTheDocument()
    expect(screen.queryByRole('button', { name: /Set up my app/ })).toBeNull()
    expect(screen.queryByRole('button', { name: /Disable access/ })).toBeNull()
    expect(screen.getByRole('button', { name: /Revoke access/ })).toBeInTheDocument()
  })

  it('état vide, avec le bouton de création', async () => {
    mockApi(list({ accesses: [], attempts: [] }))
    renderScreen()
    expect(await screen.findByText(/No access yet/)).toBeInTheDocument()
    expect(screen.getAllByRole('button', { name: 'Create an access' }).length).toBeGreaterThan(0)
  })

  it('chargement puis erreur avec réessayer', async () => {
    mockApi(new ApiError('boom', 500, { error: 'boom', code: 'internal' }))
    renderScreen()
    expect(screen.getByRole('status')).toBeInTheDocument()
    expect(await screen.findByText('Something went wrong. Try again.')).toBeInTheDocument()
    mockApi(list())
    fireEvent.click(screen.getByRole('button', { name: /Try again/ }))
    expect(await screen.findByText('Phone')).toBeInTheDocument()
  })

  it('droit insuffisant (403) : explication, pas de bouton de création', async () => {
    mockApi(new ApiError('no', 403, { error: 'no', code: 'forbidden', message: 'no' }))
    renderScreen()
    expect(await screen.findByText('You cannot manage mailbox access')).toBeInTheDocument()
    expect(screen.queryByRole('button', { name: 'Create an access' })).toBeNull()
  })

  it('désactiver appelle setDisabled puis relit la liste', async () => {
    mockApi(list())
    renderScreen()
    fireEvent.click(await screen.findByRole('button', { name: /Disable access Phone/ }))
    await waitFor(() =>
      expect(api.post).toHaveBeenCalledWith('/api/veridian/mail.access.setDisabled', {
        workspace_id: 'ws1',
        id: 'a1',
        disabled: true
      })
    )
    await waitFor(() => expect(api.get).toHaveBeenCalledTimes(2))
  })

  it('la révocation demande confirmation, puis appelle revoke', async () => {
    mockApi(list())
    renderScreen()
    fireEvent.click(await screen.findByRole('button', { name: /Revoke access Phone/ }))
    expect(await screen.findByText('Revoke this access?')).toBeInTheDocument()
    expect(api.post).not.toHaveBeenCalled()
    fireEvent.click(screen.getByRole('button', { name: 'Revoke for good' }))
    await waitFor(() =>
      expect(api.post).toHaveBeenCalledWith('/api/veridian/mail.access.revoke', { workspace_id: 'ws1', id: 'a1' })
    )
  })
})

describe('création', () => {
  const created = () => ({
    access: access({ id: 'new1', label: 'CRM' }),
    username: 'ben@example.com',
    password: PASSWORD,
    connection: list().connection
  })

  async function openCreate(isOwner = true) {
    mockApi(list({ accesses: [] }), (url) => (url.endsWith('mail.access.create') ? created() : undefined))
    renderScreen(isOwner)
    fireEvent.click((await screen.findAllByRole('button', { name: 'Create an access' }))[0])
    return screen.findByRole('dialog')
  }

  it('montre le mot de passe UNE fois : masqué, affichable, copiable, jamais stocké', async () => {
    const dialog = await openCreate()
    fireEvent.change(within(dialog).getByLabelText('Name'), { target: { value: 'CRM' } })
    fireEvent.click(within(dialog).getByRole('button', { name: 'Create' }))

    expect(await within(dialog).findByText('This password is shown only once.')).toBeInTheDocument()
    expect(api.post).toHaveBeenCalledWith('/api/veridian/mail.access.create', {
      workspace_id: 'ws1',
      profile_id: 'p1',
      address: 'ben@example.com',
      label: 'CRM',
      can_send: true,
      daily_cap: 200,
      shared: false
    })
    // masqué par défaut
    const secret = within(dialog).getByTestId('secret-value')
    expect(secret.textContent).not.toContain(PASSWORD)
    fireEvent.click(within(dialog).getByRole('button', { name: /Show Password/ }))
    expect(within(dialog).getByTestId('secret-value').textContent).toBe(PASSWORD)
    fireEvent.click(within(dialog).getByRole('button', { name: /Hide Password/ }))
    expect(within(dialog).getByTestId('secret-value').textContent).not.toContain(PASSWORD)
    // copiable
    fireEvent.click(within(dialog).getByRole('button', { name: /Copy Password/ }))
    await waitFor(() => expect(writeText).toHaveBeenCalledWith(PASSWORD))
    // jamais dans le stockage du navigateur
    expect(JSON.stringify(Object.entries(localStorage))).not.toContain(PASSWORD)
    expect(vi.mocked(localStorage.setItem).mock.calls.flat().join(' ')).not.toContain(PASSWORD)
    expect(sessionStorage.length).toBe(0)

    // fermer = oublier : plus de mot de passe dans le document
    fireEvent.click(within(dialog).getByRole('button', { name: 'Done' }))
    await waitFor(() => expect(screen.queryByTestId('secret-value')).toBeNull())
    expect(document.body.textContent).not.toContain(PASSWORD)
  })

  it('enchaîne sur « Set up my app » avec le mot de passe disponible', async () => {
    const dialog = await openCreate()
    fireEvent.change(within(dialog).getByLabelText('Name'), { target: { value: 'CRM' } })
    fireEvent.click(within(dialog).getByRole('button', { name: 'Create' }))
    await within(dialog).findByText('This password is shown only once.')
    fireEvent.click(within(dialog).getByRole('button', { name: 'Set up my app' }))
    expect(await screen.findByText('Which app do you use?')).toBeInTheDocument()
    fireEvent.click(screen.getByRole('button', { name: 'Thunderbird' }))
    expect((await screen.findAllByText('mail.example.com', { selector: 'code' })).length).toBe(2)
    expect(screen.getAllByTestId('secret-value')[0].textContent).not.toContain(PASSWORD)
  })

  it('un membre sans droit de partage ne peut pas choisir « The workspace »', async () => {
    const dialog = await openCreate(false)
    const shared = within(dialog).getByRole('radio', { name: /The workspace \(shared mailbox or tool\)/ })
    expect(shared).toBeDisabled()
    expect(within(dialog).getByText(/Only the workspace owner can create a shared access/)).toBeInTheDocument()
    expect(within(dialog).getByRole('radio', { name: /Me \(closes when I leave the workspace\)/ })).toBeChecked()
  })

  it('le propriétaire peut partager : shared=true part au serveur', async () => {
    const dialog = await openCreate(true)
    fireEvent.change(within(dialog).getByLabelText('Name'), { target: { value: 'CRM' } })
    fireEvent.click(within(dialog).getByRole('radio', { name: /The workspace \(shared mailbox or tool\)/ }))
    fireEvent.click(within(dialog).getByRole('button', { name: 'Create' }))
    await waitFor(() =>
      expect(api.post).toHaveBeenCalledWith(
        '/api/veridian/mail.access.create',
        expect.objectContaining({ shared: true })
      )
    )
  })

  it('exige un nom, et traduit owner_required et too_many', async () => {
    const dialog = await openCreate(true)
    fireEvent.click(within(dialog).getByRole('button', { name: 'Create' }))
    expect(await within(dialog).findByText(/Give this access a name/)).toBeInTheDocument()
    expect(api.post).not.toHaveBeenCalled()

    mockApi(list({ accesses: [] }), () => new ApiError('x', 403, { error: 'x', code: 'owner_required' }))
    fireEvent.change(within(dialog).getByLabelText('Name'), { target: { value: 'CRM' } })
    fireEvent.click(within(dialog).getByRole('button', { name: 'Create' }))
    expect(await within(dialog).findByText('Only the workspace owner can create a shared access.')).toBeInTheDocument()

    mockApi(list({ accesses: [] }), () => new ApiError('x', 409, { error: 'x', code: 'too_many' }))
    fireEvent.click(within(dialog).getByRole('button', { name: 'Create' }))
    expect(await within(dialog).findByText(/maximum number of accesses/)).toBeInTheDocument()
  })

  it('sans profil d’envoi : explique quoi faire', async () => {
    mockApi(list({ accesses: [] }))
    renderScreen(true, { ...workspace, integrations: [] })
    fireEvent.click((await screen.findAllByRole('button', { name: 'Create an access' }))[0])
    expect(await screen.findByText(/No sending profile yet/)).toBeInTheDocument()
  })
})

describe('régler mon application', () => {
  async function openSetup() {
    mockApi(list(), (url) =>
      url.endsWith('profileLink')
        ? { url: 'https://notifuse.example/p/tok123', expires_at: '2026-11-25T18:15:00Z' }
        : undefined
    )
    renderScreen()
    fireEvent.click(await screen.findByRole('button', { name: /Set up my app for Phone/ }))
    await screen.findByText('Which app do you use?')
  }

  it('Thunderbird : fiche pas à pas avec les valeurs du serveur, SSL/TLS des deux côtés, sans mot de passe en clair', async () => {
    await openSetup()
    fireEvent.click(screen.getByRole('button', { name: 'Thunderbird' }))
    expect(await screen.findByText(/Choose SSL\/TLS, not STARTTLS, for receiving AND for sending/)).toBeInTheDocument()
    expect(screen.getByText(/Use the same password for receiving and for sending/)).toBeInTheDocument()
    expect(screen.getByText(/The username is your whole address/)).toBeInTheDocument()
    expect(screen.getAllByText('993', { selector: 'code' })).toHaveLength(1)
    expect(screen.getAllByText('465', { selector: 'code' })).toHaveLength(1)
    expect(screen.getByText(/Password: the one shown when this access was created/)).toBeInTheDocument()
    fireEvent.click(screen.getAllByRole('button', { name: /Copy Server/ })[0])
    await waitFor(() => expect(writeText).toHaveBeenCalledWith('mail.example.com'))
  })

  it('iPhone : profil de configuration, bouton « Open on this device » et code QR', async () => {
    await openSetup()
    fireEvent.click(screen.getByRole('button', { name: 'iPhone or iPad' }))
    fireEvent.click(await screen.findByRole('button', { name: 'Get my configuration profile' }))
    const open = await screen.findByRole('link', { name: 'Open on this device' })
    expect(open).toHaveAttribute('href', 'https://notifuse.example/p/tok123')
    expect(screen.getByRole('img', { name: /QR code/ })).toBeInTheDocument()
    expect(api.post).toHaveBeenCalledWith('/api/veridian/mail.access.profileLink', { workspace_id: 'ws1', id: 'a1' })
  })

  it('Check my settings : STARTTLS sur le mauvais port -> consigne « Choose SSL/TLS », avec le port attendu', async () => {
    mockApi(list(), (url, body) => {
      if (url.endsWith('mail.access.test')) {
        const b = body as { via: string; security: string }
        return b.via === 'imap'
          ? {
              ok: false,
              checks: [
                { code: 'host', ok: true },
                { code: 'port', ok: false, expected: 993 },
                { code: 'security', ok: false, expected: 'ssl' },
                { code: 'password_empty', ok: false }
              ]
            }
          : { ok: true, checks: [{ code: 'door', ok: true }] }
      }
      return undefined
    })
    renderScreen()
    fireEvent.click(await screen.findByRole('button', { name: /Set up my app for Phone/ }))
    fireEvent.click(await screen.findByRole('tab', { name: 'Check my settings' }))

    // formulaire incomplet : rien n'est envoyé
    fireEvent.click(screen.getByRole('button', { name: 'Test' }))
    expect(await screen.findByText(/Fill in the server, the port and the security setting/)).toBeInTheDocument()
    expect(api.post).not.toHaveBeenCalled()

    const sides = ['imap', 'smtp']
    for (const via of sides) {
      fireEvent.change(document.getElementById(`mbx-check-${via}-host`) as HTMLElement, {
        target: { value: 'mail.example.com' }
      })
      fireEvent.change(document.getElementById(`mbx-check-${via}-port`) as HTMLElement, { target: { value: '443' } })
      fireEvent.change(document.getElementById(`mbx-check-${via}-username`) as HTMLElement, {
        target: { value: 'ben' }
      })
    }
    const starttls = screen.getAllByRole('radio', { name: 'STARTTLS' })
    starttls.forEach((r) => fireEvent.click(r))
    fireEvent.click(screen.getByRole('button', { name: 'Test' }))

    expect(await screen.findByText('Choose SSL/TLS instead of STARTTLS or None.')).toBeInTheDocument()
    expect(screen.getByText('Change the port to 993.')).toBeInTheDocument()
    expect(screen.getByText(/Fill in the password/)).toBeInTheDocument()
    const bad = document.querySelectorAll('[data-check="security"][data-ok="false"]')
    expect(bad).toHaveLength(1)
    expect(document.querySelector('[data-check="host"]')).toHaveAttribute('data-ok', 'true')
    expect(api.post).toHaveBeenCalledWith(
      '/api/veridian/mail.access.test',
      expect.objectContaining({
        workspace_id: 'ws1',
        address: 'ben@example.com',
        via: 'imap',
        host: 'mail.example.com',
        port: 443,
        security: 'starttls',
        username: 'ben',
        password: ''
      })
    )
  })

  it('Check my settings : trop d’essais (429) -> message clair', async () => {
    mockApi(list(), (url) =>
      url.endsWith('mail.access.test') ? new ApiError('x', 429, { error: 'x', code: 'rate_limited' }) : undefined
    )
    renderScreen()
    fireEvent.click(await screen.findByRole('button', { name: /Set up my app for Phone/ }))
    fireEvent.click(await screen.findByRole('tab', { name: 'Check my settings' }))
    for (const via of ['imap', 'smtp']) {
      fireEvent.change(document.getElementById(`mbx-check-${via}-host`) as HTMLElement, { target: { value: 'h' } })
      fireEvent.change(document.getElementById(`mbx-check-${via}-port`) as HTMLElement, { target: { value: '993' } })
    }
    screen.getAllByRole('radio', { name: 'SSL/TLS' }).forEach((r) => fireEvent.click(r))
    fireEvent.click(screen.getByRole('button', { name: 'Test' }))
    expect((await screen.findAllByText(/Too many tries in a short time/)).length).toBeGreaterThan(0)
  })
})

describe('essais récents', () => {
  it('liste les essais avec le motif du refus', async () => {
    mockApi(list())
    renderScreen()
    expect(await screen.findByText('Refused: wrong address or password')).toBeInTheDocument()
    expect(screen.getByText('Succeeded')).toBeInTheDocument()
    expect(screen.queryByText(/No attempt reached us/)).toBeNull()
  })

  it('aucun essai depuis attempts_since : dit STARTTLS ou autre port, avec le port SSL/TLS', async () => {
    mockApi(list({ attempts: [] }))
    renderScreen()
    const msg = await screen.findByText(/No attempt reached us since/)
    expect(msg.textContent).toMatch(/probably set to STARTTLS or to another port/)
    expect(msg.textContent).toMatch(/Use SSL\/TLS, port 993 to receive, 465 to send\./)
  })

  it('aucun essai mais aucun accès ouvert : pas de fausse alerte', async () => {
    mockApi(list({ attempts: [], accesses: [access({ disabled_at: '2026-11-24T10:00:00Z' })] }))
    renderScreen()
    await screen.findByText('Disabled')
    expect(screen.queryByText(/probably set to STARTTLS/)).toBeNull()
  })
})

describe('export', () => {
  it('télécharge le .mbox par fetch avec l’en-tête Authorization', async () => {
    localStorage.setItem('auth_token', 'tok')
    const fetchMock = vi.fn().mockResolvedValue(
      new Response('From a\n', { status: 200, headers: { 'Content-Disposition': 'attachment; filename="inbox.mbox"' } })
    )
    vi.stubGlobal('fetch', fetchMock)
    const createObjectURL = vi.fn(() => 'blob:x')
    Object.assign(URL, { createObjectURL, revokeObjectURL: vi.fn() })
    const clicked: string[] = []
    vi.spyOn(HTMLAnchorElement.prototype, 'click').mockImplementation(function (this: HTMLAnchorElement) {
      clicked.push(this.download)
    })
    mockApi(list())
    renderScreen()
    expect(await screen.findByText('Import it in Thunderbird, Apple Mail or another provider.')).toBeInTheDocument()
    fireEvent.click(screen.getByRole('button', { name: 'Download Inbox (Main profile)' }))
    await waitFor(() => expect(fetchMock).toHaveBeenCalled())
    expect(String(fetchMock.mock.calls[0][0])).toContain('mail.export?workspace_id=ws1&profile_id=p1&folder=inbox')
    expect((fetchMock.mock.calls[0][1] as RequestInit).headers).toEqual({ Authorization: 'Bearer tok' })
    await waitFor(() => expect(createObjectURL).toHaveBeenCalled())
    expect(clicked).toEqual(['inbox.mbox'])
  })

  it('un refus du serveur s’affiche, rien n’est téléchargé', async () => {
    vi.stubGlobal(
      'fetch',
      vi.fn().mockResolvedValue(new Response(JSON.stringify({ error: 'no', code: 'forbidden' }), { status: 403 }))
    )
    mockApi(list())
    renderScreen()
    fireEvent.click(await screen.findByRole('button', { name: 'Download Sent (Main profile)' }))
    expect(await screen.findByText(/do not have the right/)).toBeInTheDocument()
  })
})
