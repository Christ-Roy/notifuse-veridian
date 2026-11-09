/**
 * Veridian — liens « Brancher mon IA » : lecture de la route publique du Hub et validation de sa réponse.
 * Rien n'est recopié ici : on lit `GET /api/mcp/liens-installation?app=notifuse` (CORS ouvert, cache 5 min).
 * Seuls les schémas https:, cursor: et vscode: sont acceptés pour un bouton (pas de javascript:).
 */

export const VERIDIAN_MCP_LINKS_URL =
  'https://app.veridian.site/api/mcp/liens-installation?app=notifuse'
export const VERIDIAN_MCP_FALLBACK_URL = 'https://app.veridian.site/api/mcp'
export const VERIDIAN_MCP_REVOKE_URL = 'https://app.veridian.site/dashboard/ia'

const ALLOWED_SCHEMES = ['https:', 'cursor:', 'vscode:']

export interface InstallAssistant {
  cle: string
  libelle: string
  installation: string
  etape: string | null
}

export interface InstallLinks {
  url: string
  assistants: InstallAssistant[]
  claudeCode: string | null
  phrase: string | null
}

function safeLink(v: unknown): string | null {
  if (typeof v !== 'string') return null
  try {
    return ALLOWED_SCHEMES.includes(new URL(v).protocol) ? v : null
  } catch {
    return null
  }
}

/** Valide la réponse du Hub : ce qui n'est pas exploitable est écarté ; rien d'exploitable = null (repli). */
export function sanitizeInstallLinks(raw: unknown): InstallLinks | null {
  if (!raw || typeof raw !== 'object') return null
  const r = raw as Record<string, unknown>
  const url = safeLink(r.url)
  if (!url) return null
  const assistants: InstallAssistant[] = []
  if (Array.isArray(r.assistants)) {
    for (const a of r.assistants) {
      if (!a || typeof a !== 'object') continue
      const x = a as Record<string, unknown>
      const installation = safeLink(x.installation)
      if (!installation || typeof x.libelle !== 'string' || !x.libelle || typeof x.cle !== 'string') continue
      assistants.push({
        cle: x.cle,
        libelle: x.libelle,
        installation,
        etape: typeof x.etape === 'string' && x.etape ? x.etape : null
      })
    }
  }
  const app = r.app && typeof r.app === 'object' ? (r.app as Record<string, unknown>) : null
  return {
    url,
    assistants,
    claudeCode: typeof r.claudeCode === 'string' && r.claudeCode ? r.claudeCode : null,
    phrase: app && typeof app.phrase === 'string' && app.phrase ? app.phrase : null
  }
}

/** Lit les liens au Hub (4 s max). Rend null en cas d'échec : l'appelant affiche le repli. */
export async function fetchInstallLinks(
  fetchImpl: typeof fetch = fetch,
  timeoutMs = 4000
): Promise<InstallLinks | null> {
  const ctrl = new AbortController()
  const timer = setTimeout(() => ctrl.abort(), timeoutMs)
  try {
    const resp = await fetchImpl(VERIDIAN_MCP_LINKS_URL, { signal: ctrl.signal })
    if (!resp.ok) return null
    return sanitizeInstallLinks(await resp.json())
  } catch {
    return null
  } finally {
    clearTimeout(timer)
  }
}

