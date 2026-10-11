import * as crypto from 'crypto'

// Garde « aucun jetable laissé en prod » (fiche 64, 11/10/2026).
//
// Contexte : le 11/10, 98 bases notifuse_ws_* en prod pour 33 workspaces réels ; 67 étaient des restes de
// tests (e2e<timestamp>, abuse*, rot*...). Le job e2e-prod ne lance que les specs @prod-safe, qui par
// règle d'or ne créent AUCUN tenant : ce module rend cette règle MESURABLE. Il compare la liste des
// tenants gérés avant et après le run (prod seulement, E2E_PROD_MODE=1).
//
//  - nouveau tenant au motif de test  : wipé (HMAC admin) et signalé en avertissement ;
//  - nouveau tenant hors motif        : signalé SEULEMENT (ce peut être une vraie inscription pendant le
//    run, on ne wipe jamais un inconnu en prod) ;
//  - liste illisible (avant ou après) : « non abouti », dit tel quel, jamais « rien à signaler ».
// Le run ne casse jamais ici (un échec de e2e-prod déclenche le rollback de la prod) : le verdict va dans
// les logs et le résumé du job. Le filet complet est le contrôle quotidien jetables.py (acquisition status).

export const TEST_TENANT_MOTIF = /^(e2e|test|abuse|rot|fixchk|capcheck|nomtest|agenttest|clitest|ws\d)/i

function sign(secret: string, body: string) {
  const ts = Date.now().toString()
  const signature = crypto.createHmac('sha256', secret).update(`${ts}.${body}`).digest('hex')
  return {
    'Content-Type': 'application/json',
    'x-veridian-app': 'notifuse',
    'X-Veridian-Hub-Signature': signature,
    'X-Veridian-Timestamp': ts,
  }
}

/** Identifiants des tenants gérés, ou null si la mesure n'a pas abouti. */
export async function listManagedTenants(baseUrl: string, secret: string): Promise<string[] | null> {
  try {
    const r = await fetch(`${baseUrl}/api/veridian/admin/tenants?limit=2000`, { method: 'GET', headers: sign(secret, '') })
    if (!r.ok) return null
    const j: any = await r.json()
    if (!Array.isArray(j.managed) || j.managed.length === 0) return null // vide = non abouti, jamais « 0 »
    return j.managed.map((t: any) => String(t.tenant_id))
  } catch {
    return null
  }
}

export function diffNewTenants(before: string[], after: string[]) {
  const known = new Set(before)
  const fresh = after.filter((id) => !known.has(id))
  return {
    wipe: fresh.filter((id) => TEST_TENANT_MOTIF.test(id)),
    reportOnly: fresh.filter((id) => !TEST_TENANT_MOTIF.test(id)),
  }
}

async function wipe(baseUrl: string, secret: string, ids: string[]) {
  const body = JSON.stringify({ tenant_ids: ids })
  const r = await fetch(`${baseUrl}/api/veridian/admin/wipe-test-tenants`, { method: 'POST', headers: sign(secret, body), body })
  return r.ok
}

export async function prodResidueSetup() {
  if (process.env.E2E_PROD_MODE !== '1') return
  const { NOTIFUSE_URL: url, HUB_API_SECRET: secret } = process.env
  if (!url || !secret) return
  const before = await listManagedTenants(url, secret)
  process.env.E2E_PROD_TENANTS_BEFORE = before ? JSON.stringify(before) : ''
  console.log(`[prod-residue] avant: ${before ? before.length + ' tenants' : 'NON ABOUTI (liste illisible)'}`)
}

export async function prodResidueTeardown() {
  if (process.env.E2E_PROD_MODE !== '1') return
  const { NOTIFUSE_URL: url, HUB_API_SECRET: secret } = process.env
  if (!url || !secret) return
  const raw = process.env.E2E_PROD_TENANTS_BEFORE
  const after = await listManagedTenants(url, secret)
  if (!raw || !after) {
    console.log('::warning::[prod-residue] NON ABOUTI : liste des tenants illisible avant ou après, aucun verdict')
    return
  }
  const { wipe: toWipe, reportOnly } = diffNewTenants(JSON.parse(raw), after)
  if (toWipe.length === 0 && reportOnly.length === 0) {
    console.log(`[prod-residue] OK : aucun tenant créé pendant le run (${after.length} tenants)`)
    return
  }
  if (toWipe.length > 0) {
    const ok = await wipe(url, secret, toWipe)
    console.log(`::warning::[prod-residue] E2E prod a créé ${toWipe.length} tenant(s) de test (${toWipe.join(', ')}), wipe ${ok ? 'ok' : 'ECHEC'}. Règle d'or @prod-safe violée : retirer le tag.`)
  }
  if (reportOnly.length > 0) {
    console.log(`::warning::[prod-residue] nouveau(x) tenant(s) hors motif de test pendant le run (inscription réelle ?) : ${reportOnly.join(', ')}. Non touché(s).`)
  }
}
