import { describe, it, expect } from 'vitest'
import {
  SIDEBAR_GROUP_KEYS,
  messageTypeFromSearch,
  selectedSidebarKey,
  templatesFamilyFromSearch
} from './veridian_sidebar_model'

describe('groupes de la sidebar (lot 4)', () => {
  it("Transactionnel = modeles transactionnels, API d'envoi / SMTP Bridge, journal transactionnel", () => {
    expect([...SIDEBAR_GROUP_KEYS.transactional]).toEqual([
      'templates-transactional',
      'transactional-notifications',
      'logs-transactional'
    ])
  })

  it("Prospection = tout le commercial, profils d'envoi compris, et plus de groupe Envoi", () => {
    expect([...SIDEBAR_GROUP_KEYS.prospection]).toEqual([
      'contacts',
      'lists',
      'templates',
      'broadcasts',
      'automations',
      'sending-profiles',
      'logs',
      'send-queue'
    ])
    expect(Object.keys(SIDEBAR_GROUP_KEYS)).toEqual(['prospection', 'transactional'])
  })

  it('aucune entree dans les deux groupes', () => {
    const both = SIDEBAR_GROUP_KEYS.prospection.filter((k) =>
      (SIDEBAR_GROUP_KEYS.transactional as readonly string[]).includes(k)
    )
    expect(both).toEqual([])
  })
})

describe('famille de modeles et type de journal', () => {
  it('modeles : commercial par defaut, transactionnel par family ou category', () => {
    expect(templatesFamilyFromSearch({})).toBe('commercial')
    expect(templatesFamilyFromSearch(undefined)).toBe('commercial')
    expect(templatesFamilyFromSearch({ family: 'transactional' })).toBe('transactional')
    expect(templatesFamilyFromSearch({ category: 'transactional' })).toBe('transactional')
    expect(templatesFamilyFromSearch({ category: 'marketing' })).toBe('commercial')
    expect(templatesFamilyFromSearch({ family: 'commercial', category: 'transactional' })).toBe('commercial')
  })

  it('journal : commercial par defaut, valeur inconnue = commercial', () => {
    expect(messageTypeFromSearch({})).toBe('commercial')
    expect(messageTypeFromSearch({ type: 'transactional' })).toBe('transactional')
    expect(messageTypeFromSearch({ type: 'autre chose' })).toBe('commercial')
  })
})

describe('cle selectionnee : meme route, parametre different', () => {
  const base = '/console/workspace/ws-1'
  it('Modeles', () => {
    expect(selectedSidebarKey(`${base}/templates`, {})).toBe('templates')
    expect(selectedSidebarKey(`${base}/templates`, { family: 'commercial' })).toBe('templates')
    expect(selectedSidebarKey(`${base}/templates`, { family: 'transactional' })).toBe('templates-transactional')
  })
  it('Journal', () => {
    expect(selectedSidebarKey(`${base}/logs`, {})).toBe('logs')
    expect(selectedSidebarKey(`${base}/logs`, { type: 'commercial' })).toBe('logs')
    expect(selectedSidebarKey(`${base}/logs`, { type: 'transactional' })).toBe('logs-transactional')
  })
  it('autres routes', () => {
    expect(selectedSidebarKey(`${base}/transactional-notifications`)).toBe('transactional-notifications')
    expect(selectedSidebarKey(`${base}/sending-profiles`)).toBe('sending-profiles')
    expect(selectedSidebarKey(`${base}/contacts`)).toBe('contacts')
    expect(selectedSidebarKey(`${base}/settings/team`)).toBe('settings')
    expect(selectedSidebarKey(`${base}/file-manager`)).toBe('')
    expect(selectedSidebarKey(base)).toBe('analytics')
  })
})
