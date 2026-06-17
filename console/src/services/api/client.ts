import { router } from '../../router'

export class ApiError extends Error {
  constructor(
    message: string,
    public status: number,
    public data?: unknown
  ) {
    super(message)
    this.name = 'ApiError'
  }
}

// Endpoints publics (token-based, hors session) : un 401 y signifie "token
// invalide/expiré", pas "session expirée". Le composant appelant gère
// l'erreur lui-même (ex: AcceptInvitationPage affiche un <Result>), on ne
// doit donc PAS rediriger vers signin sur leur 401.
const PUBLIC_TOKEN_ENDPOINTS = ['/api/workspaces.verifyInvitationToken']

async function handleResponse<T>(response: Response, endpoint: string): Promise<T> {
  if (!response.ok) {
    const errorData = await response.json().catch(() => null)

    const isPublicTokenEndpoint = PUBLIC_TOKEN_ENDPOINTS.some((path) =>
      endpoint.startsWith(path)
    )

    if (
      !isPublicTokenEndpoint &&
      (response.status === 401 ||
        errorData?.error === 'Session expired' ||
        errorData?.message === 'Session expired')
    ) {
      localStorage.removeItem('auth_token')

      router.navigate({ to: '/console/signin' })
    }

    // Extract a HUMAN-READABLE message. Most endpoints return {error: "<msg>"},
    // but some (e.g. analytics_handler.go writeErrorResponse) return
    // {error: true, message: "<msg>"} where `error` is a BOOLEAN flag. If we
    // naively used `errorData.error` there, ApiError.message would become the
    // string "true" and the UI would show "ApiError: true" instead of the real
    // cause (bug P0 dashboard 500, 2026-06-17). So: prefer a string `error`,
    // otherwise fall back to `message`, otherwise a generic label.
    const errMessage =
      (typeof errorData?.error === 'string' && errorData.error) ||
      (typeof errorData?.message === 'string' && errorData.message) ||
      'An error occurred'
    throw new ApiError(errMessage, response.status, errorData)
  }
  return response.json()
}

async function request<T>(endpoint: string, options: RequestInit = {}): Promise<T> {
  const authToken = localStorage.getItem('auth_token')
  const headers = {
    'Content-Type': 'application/json',
    ...(authToken ? { Authorization: `Bearer ${authToken}` } : {}),
    ...options.headers
  }

  let defaultOrigin = window.location.origin
  if (defaultOrigin.includes('notifusedev.com')) {
    defaultOrigin = 'https://localapi.notifuse.com:4000'
  }

  const apiEndpoint = window.API_ENDPOINT?.trim().replace(/\/+$/, '') || defaultOrigin

  const response = await fetch(`${apiEndpoint}${endpoint}`, {
    ...options,
    headers
  })

  return handleResponse<T>(response, endpoint)
}

export const api = {
  get: <T>(endpoint: string) => request<T>(endpoint),
  post: <T>(endpoint: string, data: unknown) =>
    request<T>(endpoint, {
      method: 'POST',
      body: JSON.stringify(data)
    }),
  put: <T>(endpoint: string, data: unknown) =>
    request<T>(endpoint, {
      method: 'PUT',
      body: JSON.stringify(data)
    }),
  delete: <T>(endpoint: string) =>
    request<T>(endpoint, {
      method: 'DELETE'
    })
}
