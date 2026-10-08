// Lot 3 : écritures de la page Profils d'envoi. Chaque opération relit le
// workspace avant d'écrire (jamais de brouillon périmé), et ne renvoie JAMAIS un
// secret : les mots de passe saisis partent une fois, les champs de lecture seule
// (has_*, vérifié le, identifiants configurés) sont retirés de la requête.

import { workspaceService, type IMAPSettings } from '../../services/api/workspace'
import { emailService } from '../../services/api/email'
import {
  emailProfilesStateService,
  type EmailProfileUsageKind
} from '../../services/api/veridian_email_profiles'
import type { EmailProvider, Integration, Workspace } from '../../services/api/types'
import { marketingProfileIds, smtpSettingsForRequest } from '../settings/veridian_email_profiles'

export class ProfileOperationError extends Error {
  constructor(
    message: string,
    public code: 'last_profile' | 'unverified' | 'not_found' | 'transactional'
  ) {
    super(message)
    this.name = 'ProfileOperationError'
  }
}

async function loadWorkspace(workspaceId: string): Promise<Workspace> {
  return (await workspaceService.get(workspaceId)).workspace
}

function findEmailIntegration(workspace: Workspace, integrationId: string): Integration {
  const integration = (workspace.integrations || []).find(
    (i) => i.id === integrationId && i.type === 'email' && i.email_provider
  )
  if (!integration) throw new ProfileOperationError('Profile not found', 'not_found')
  return integration
}

// Le profil tel qu'il part vers updateIntegration : tout ce que le serveur a
// stocké, moins les champs que seul le serveur émet.
export function providerForRequest(provider: EmailProvider): EmailProvider {
  const next: EmailProvider = { ...provider }
  delete next.veridian_credentials_configured
  delete next.veridian_transport_verified_at
  if (next.smtp) next.smtp = smtpSettingsForRequest(next.smtp)
  return next
}

export async function updateProfile(
  workspaceId: string,
  integrationId: string,
  patch: { name?: string; provider?: (current: EmailProvider) => EmailProvider }
): Promise<void> {
  const workspace = await loadWorkspace(workspaceId)
  const integration = findEmailIntegration(workspace, integrationId)
  const current = integration.email_provider as EmailProvider
  const provider = patch.provider ? patch.provider(current) : current
  await workspaceService.updateIntegration({
    workspace_id: workspaceId,
    integration_id: integrationId,
    name: patch.name ?? integration.name,
    provider: providerForRequest(provider)
  })
}

// Lot 4 : pause, rotation et usage passent par l API dediee (emailProfiles.pause / resume /
// setUsage), plus par updateIntegration ni workspaces.update. Le serveur valide l exclusivite
// et applique tout en une ecriture. On garde en amont une doublure de validation (message
// immediat, sans aller-retour) ; si le serveur refuse quand meme, son message lisible
// remonte tel quel (ApiError.message).

export async function setPaused(workspaceId: string, integrationId: string, paused: boolean) {
  const workspace = await loadWorkspace(workspaceId)
  findEmailIntegration(workspace, integrationId)
  // Un profil transactionnel ne se met pas en pause : un mail transactionnel part toujours.
  if (paused && workspace.settings.transactional_email_provider_id === integrationId) {
    throw new ProfileOperationError('A transactional profile cannot be paused', 'transactional')
  }
  const request = { workspace_id: workspaceId, integration_id: integrationId }
  return paused ? emailProfilesStateService.pause(request) : emailProfilesStateService.resume(request)
}

function applyUsage(workspaceId: string, integrationId: string, usage: EmailProfileUsageKind) {
  return emailProfilesStateService.setUsage({
    workspace_id: workspaceId,
    integration_id: integrationId,
    usage
  })
}

// Entrer dans la rotation = usage commercial ; en sortir = hors service (ni rotation ni
// transactionnel).
export async function setRotation(
  workspaceId: string,
  integrationId: string,
  enabled: boolean
): Promise<void> {
  const workspace = await loadWorkspace(workspaceId)
  const integration = findEmailIntegration(workspace, integrationId)
  if (enabled) {
    if (workspace.settings.transactional_email_provider_id === integrationId) {
      throw new ProfileOperationError('Reserved for transactional', 'transactional')
    }
    if (!integration.email_provider?.veridian_transport_verified_at) {
      throw new ProfileOperationError('Send a successful test first', 'unverified')
    }
  } else if (marketingProfileIds(workspace.settings).filter((id) => id !== integrationId).length === 0) {
    throw new ProfileOperationError('The rotation must keep at least one profile', 'last_profile')
  }
  await applyUsage(workspaceId, integrationId, enabled ? 'commercial' : 'unassigned')
}

// Usage exclusif : un profil est commercial (en rotation), transactionnel (LE profil
// transactionnel, l ancien passe hors service) ou hors service.
export async function setUsage(
  workspaceId: string,
  integrationId: string,
  usage: 'commercial' | 'transactional' | 'unassigned'
): Promise<void> {
  const workspace = await loadWorkspace(workspaceId)
  const integration = findEmailIntegration(workspace, integrationId)
  const verified = !!integration.email_provider?.veridian_transport_verified_at
  if (usage === 'transactional') {
    if (!verified) throw new ProfileOperationError('Send a successful test first', 'unverified')
    const pool = marketingProfileIds(workspace.settings)
    if (pool.includes(integrationId) && pool.filter((id) => id !== integrationId).length === 0) {
      throw new ProfileOperationError('The rotation must keep at least one profile', 'last_profile')
    }
  } else if (usage === 'commercial' && !verified) {
    throw new ProfileOperationError('Send a successful test first', 'unverified')
  }
  await applyUsage(workspaceId, integrationId, usage)
}

export function deleteProfile(workspaceId: string, integrationId: string) {
  return workspaceService.deleteIntegration({ workspace_id: workspaceId, integration_id: integrationId })
}

export function testProfile(workspaceId: string, integrationId: string, to: string) {
  return emailService.testProvider(workspaceId, integrationId, to)
}

// Boîte IMAP : le mot de passe vide à l'édition conserve le secret stocké.
export async function saveInbox(
  workspaceId: string,
  inbox: { id?: string; name: string; settings: IMAPSettings }
): Promise<string> {
  const settings: IMAPSettings = { ...inbox.settings, password: inbox.settings.password || undefined }
  delete (settings as { has_password?: boolean }).has_password
  if (inbox.id) {
    await workspaceService.updateIntegration({
      workspace_id: workspaceId,
      integration_id: inbox.id,
      name: inbox.name,
      imap_settings: settings
    })
    return inbox.id
  }
  const response = await workspaceService.createIntegration({
    workspace_id: workspaceId,
    name: inbox.name,
    type: 'imap',
    imap_settings: settings
  })
  return (response as { integration_id: string }).integration_id
}

export function linkInbox(workspaceId: string, profileId: string, inboxId: string | null) {
  return updateProfile(workspaceId, profileId, {
    provider: (current) => {
      const next = { ...current }
      if (inboxId) next.veridian_return_imap_integration_id = inboxId
      else delete next.veridian_return_imap_integration_id
      return next
    }
  })
}
