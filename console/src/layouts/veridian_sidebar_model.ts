// Modele de la sidebar : groupes Prospection / Transactionnel et cle selectionnee.
// Fichier pur (sans React) pour etre teste sans monter le layout.
// Lot 4 console (07/10/2026) : le transactionnel et le commercial sont deux groupes
// distincts ; les pages Modeles et Journal sont les memes pages, filtrees par un
// parametre de recherche de route (jamais une copie de page).

import type { MessageType } from '../services/api/messages_history'

export type TemplatesFamily = 'commercial' | 'transactional'

export interface TemplatesSearchLike {
  category?: string
  family?: string
}

export interface LogsSearchLike {
  type?: string
}

/** Famille de modeles affichee : le parametre `family`, sinon deduite de `category`. */
export function templatesFamilyFromSearch(search: TemplatesSearchLike | undefined): TemplatesFamily {
  if (search?.family === 'transactional') return 'transactional'
  if (search?.family === 'commercial') return 'commercial'
  return search?.category === 'transactional' ? 'transactional' : 'commercial'
}

/** Type de journal affiche : commercial par defaut, toute valeur inconnue retombe dessus. */
export function messageTypeFromSearch(search: LogsSearchLike | undefined): MessageType {
  return search?.type === 'transactional' ? 'transactional' : 'commercial'
}

/** Cles des entrees de chaque groupe, dans l ordre d affichage. */
export const SIDEBAR_GROUP_KEYS = {
  prospection: [
    'contacts',
    'lists',
    'templates',
    'broadcasts',
    'automations',
    'sending-profiles',
    'logs',
    'send-queue'
  ],
  transactional: ['templates-transactional', 'transactional-notifications', 'logs-transactional']
} as const

export interface SidebarSearch extends TemplatesSearchLike, LogsSearchLike {}

/** Cle de menu selectionnee pour une route et ses parametres de recherche. */
export function selectedSidebarKey(pathname: string, search?: SidebarSearch): string {
  if (pathname.includes('/settings')) return 'settings'
  if (pathname.includes('/lists')) return 'lists'
  if (pathname.includes('/templates')) {
    return templatesFamilyFromSearch(search) === 'transactional'
      ? 'templates-transactional'
      : 'templates'
  }
  if (pathname.includes('/contacts')) return 'contacts'
  // Entree masquee de la sidebar (le gestionnaire reste atteignable par le selecteur d images)
  if (pathname.includes('/file-manager')) return ''
  if (pathname.includes('/transactional-notifications')) return 'transactional-notifications'
  if (pathname.includes('/sending-profiles')) return 'sending-profiles'
  if (pathname.includes('/send-queue')) return 'send-queue'
  if (pathname.includes('/logs')) {
    return messageTypeFromSearch(search) === 'transactional' ? 'logs-transactional' : 'logs'
  }
  if (pathname.includes('/broadcasts')) return 'broadcasts'
  if (pathname.includes('/automations')) return 'automations'
  return 'analytics'
}
