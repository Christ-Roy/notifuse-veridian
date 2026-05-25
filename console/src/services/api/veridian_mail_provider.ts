/**
 * Veridian — mail provider choice API client.
 *
 * Wrappe les endpoints user-auth `GET/POST /api/workspaces/{id}/mail-provider-choice`
 * livrés par l'agent backend mail-pref-migration (vague 6 — ticket
 * `2026-05-25-mail-send-as-user-via-hub-gateway.md` §3.4 + §3.1).
 *
 * Choix possibles (cf. CLAUDE.md pivot pricing — pas de gating, juste un
 * routing technique du sender) :
 *   - `smtp_generic`  : sender générique Veridian (défaut, comportement actuel)
 *   - `hub_gmail`     : route via Hub Mail Gateway `POST <hub>/api/mail/send-as-user`
 *                       (le user doit avoir connecté Gmail côté Hub — si non,
 *                       le backend Go fallback automatiquement sur smtp_generic
 *                       au runtime via le mail-gateway-client lib).
 *
 * Pas d'endpoint Hub `GET /api/users/{userId}/mail-provider-status` aujourd'hui
 * (vérifié → 404 catchall Next.js côté Hub staging). Donc UI ne peut PAS
 * afficher "Status connecté à Gmail (email@...)". Ticket Hub miroir ouvert :
 * `../veridian-hub/todo/2026-05-25-mail-provider-status-endpoint.md`.
 */

import { api } from './client'

export type MailProviderChoice = 'smtp_generic' | 'hub_gmail'

export interface MailProviderChoiceResponse {
  workspace_id: string
  choice: MailProviderChoice
  updated_at: string
}

export const veridianMailProviderApi = {
  /**
   * Récupère le choix de provider courant du workspace. Si l'endpoint
   * n'existe pas encore côté backend (404), retourne le défaut `smtp_generic`
   * pour permettre à l'UI de monter sans erreur visible.
   */
  async getChoice(workspaceId: string): Promise<MailProviderChoiceResponse> {
    try {
      return await api.get<MailProviderChoiceResponse>(
        `/api/workspaces/${encodeURIComponent(workspaceId)}/mail-provider-choice`
      )
    } catch {
      return {
        workspace_id: workspaceId,
        choice: 'smtp_generic',
        updated_at: ''
      }
    }
  },

  async setChoice(
    workspaceId: string,
    choice: MailProviderChoice
  ): Promise<MailProviderChoiceResponse> {
    return await api.post<MailProviderChoiceResponse>(
      `/api/workspaces/${encodeURIComponent(workspaceId)}/mail-provider-choice`,
      { choice }
    )
  }
}
