import { api } from './client'

// Domaines d'envoi authentifiés (VER-166, fiche 97) : miroir du contrat serveur `sendingDomains.*`. Les enregistrements à publier et
// les verdicts DNS viennent du serveur ; ce fichier ne calcule rien. Le serveur ne rend jamais la clé privée.

export type DomainRecordKind = 'dkim' | 'spf' | 'dmarc'
export type DomainStatus = 'pending' | 'verified'
export type DomainVerdict = 'pass' | 'inherited' | 'missing' | 'mismatch' | 'multiple' | 'temperror'

export interface DomainRecord {
  kind: DomainRecordKind
  type: string
  name: string
  value: string
  required: boolean
  note?: string
}

export interface DomainCheck {
  kind: DomainRecordKind
  ok: boolean
  verdict: DomainVerdict | string
  found?: string[]
  detail: string
}

export interface SendingDomain {
  id: string
  workspace_id: string
  domain: string
  selector: string
  status: DomainStatus
  complete: boolean
  checks: DomainCheck[]
  records: DomainRecord[]
  created_by: string
  created_at: string
  last_checked_at?: string
  verified_at?: string
}

export interface SendingDomainList {
  domains: SendingDomain[]
  spf_include: string
}

const BASE = '/api/veridian'

export const sendingDomainsService = {
  list: (workspaceId: string) =>
    api.get<SendingDomainList>(`${BASE}/sendingDomains.list?workspace_id=${encodeURIComponent(workspaceId)}`),
  create: (workspaceId: string, domain: string) =>
    api.post<{ domain: SendingDomain }>(`${BASE}/sendingDomains.create`, { workspace_id: workspaceId, domain }),
  verify: (workspaceId: string, id: string) =>
    api.post<{ domain: SendingDomain }>(`${BASE}/sendingDomains.verify`, { workspace_id: workspaceId, id }),
  remove: (workspaceId: string, id: string) =>
    api.post<{ deleted: string; domain: string }>(`${BASE}/sendingDomains.delete`, { workspace_id: workspaceId, id })
}

/** Le verdict d'un enregistrement au dernier contrôle, ou undefined s'il n'a jamais été vérifié. */
export function checkOf(d: SendingDomain, kind: DomainRecordKind): DomainCheck | undefined {
  return (d.checks ?? []).find((c) => c.kind === kind)
}
