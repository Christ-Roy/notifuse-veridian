/**
 * Veridian — multi-mail-accounts API client (vague 7, 2026-05-25).
 *
 * Wrappe les endpoints user-auth proxy livres cote Notifuse :
 *
 *   GET  /api/veridian/mail-accounts/me
 *   POST /api/veridian/mail-accounts/me/{accountId}/default
 *
 * Le proxy Notifuse resout JWT user -> hub_user_id local -> Hub HMAC.
 *
 * Mode optimiste : si Hub indisponible (endpoint pas encore livre, user
 * sans hub_user_id, network), le proxy repond TOUJOURS 200 avec
 * `hub_available: false`. Ce client ne throw donc jamais sur "Hub down" :
 * l'UI consomme `hub_available` pour decider de l'affichage.
 *
 * Spec Hub : `../veridian-hub/todo/2026-05-25-mail-provider-status-endpoint.md`.
 */

import { api, ApiError } from './client'

export type MailAccountProvider = 'google' | 'microsoft'

export interface MailAccount {
  id: string
  provider: MailAccountProvider
  email: string
  name: string
  is_default: boolean
  needs_reauth: boolean
  connected_at: string
}

export interface MailAccountsListResponse {
  hub_available: boolean
  accounts: MailAccount[]
}

export interface MailAccountSetDefaultResponse {
  hub_available: boolean
  user_id?: string
  account_id?: string
  is_default: boolean
  reason?: string
}

export const veridianMailAccountsApi = {
  /**
   * Liste les comptes mail OAuth du user courant. Retourne TOUJOURS un
   * objet (jamais throw sauf 401 session expiree gere par client.ts).
   * Si proxy/Hub down -> hub_available=false + accounts=[].
   */
  async list(): Promise<MailAccountsListResponse> {
    try {
      return await api.get<MailAccountsListResponse>(
        '/api/veridian/mail-accounts/me'
      )
    } catch (err) {
      // 401 deja gere par client.ts (redirect signin). Pour les autres
      // erreurs (404 endpoint absent du build, 500), fallback gracieux.
      if (err instanceof ApiError && err.status === 401) {
        throw err
      }
      return { hub_available: false, accounts: [] }
    }
  },

  /**
   * Marque un compte comme defaut pour les envois `hub_gmail`. Retourne
   * `hub_available: false` si proxy ne peut pas atteindre Hub.
   */
  async setDefault(accountId: string): Promise<MailAccountSetDefaultResponse> {
    try {
      return await api.post<MailAccountSetDefaultResponse>(
        `/api/veridian/mail-accounts/me/${encodeURIComponent(accountId)}/default`,
        {}
      )
    } catch (err) {
      if (err instanceof ApiError && err.status === 401) {
        throw err
      }
      return {
        hub_available: false,
        is_default: false,
        reason: 'request_failed'
      }
    }
  }
}
