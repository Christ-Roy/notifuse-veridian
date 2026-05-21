/**
 * Veridian — intercepteur paywall global.
 *
 * Wrap `window.fetch` au boot pour détecter les réponses qui signalent un
 * état dégradé/suspendu côté backend Notifuse :
 *
 *   - **402 Payment Required** : paywall actif sur le tenant (cf. middleware
 *     `veridian_paywall.go`). Body contient `{error, error_code,
 *     tenant_status}`. On déclenche un modal CTA → app.veridian.site.
 *
 *   - **503 + error_code=hub_sync_dead** : Hub silencieux > 72h, middleware
 *     paywall dégrade en lecture seule (cf. lot H). On affiche un toast
 *     informatif moins anxiogène.
 *
 *   - **X-Tenant-Soft-Deleted: true** : tenant soft-deleted, middleware
 *     obfusque les listes (lot J). On déclenche un événement bandeau global
 *     consommé par `<VeridianSoftDeleteBanner>`.
 *
 * Convention :
 *   - Pas de patch direct de `client.ts` upstream — on wrap `window.fetch`
 *     globalement. Évite de toucher au code Notifuse pristine.
 *   - Pour les modals/notifications, on utilise un EventTarget global que
 *     les composants consomment via hook React (`useVeridianPaywall`).
 *     Évite de devoir importer Ant Design statiquement dans ce module
 *     non-React.
 *
 * Idempotent : `installVeridianFetchInterceptor()` est safe à appeler
 * plusieurs fois (flag global).
 */

declare global {
  interface Window {
    __VERIDIAN_INTERCEPTOR_INSTALLED__?: boolean
  }
}

export type VeridianPaywallEventDetail = {
  status: number
  error?: string
  errorCode?: string
  tenantStatus?: string
  retryAfter?: number
}

export type VeridianSoftDeleteEventDetail = {
  deletedAt?: string
  purgeAt?: string
}

export const VERIDIAN_PAYWALL_EVENT = 'veridian:paywall'
export const VERIDIAN_HUB_SYNC_DEAD_EVENT = 'veridian:hub-sync-dead'
export const VERIDIAN_SOFT_DELETE_EVENT = 'veridian:soft-delete'

/**
 * Dispatch un CustomEvent global. Consommé par les hooks/composants React.
 */
function dispatch<T>(eventName: string, detail: T): void {
  if (typeof window === 'undefined') return
  window.dispatchEvent(new CustomEvent<T>(eventName, { detail }))
}

/**
 * Inspecte une Response clonée pour détecter les signaux Veridian et
 * dispatcher les events appropriés. Ne consomme pas la response originale.
 */
async function inspectResponse(response: Response): Promise<void> {
  // Soft-delete headers (lot J) : présents sur 200 OK aussi.
  if (response.headers.get('X-Tenant-Soft-Deleted') === 'true') {
    dispatch<VeridianSoftDeleteEventDetail>(VERIDIAN_SOFT_DELETE_EVENT, {
      deletedAt: response.headers.get('X-Tenant-Deleted-At') ?? undefined,
      purgeAt: response.headers.get('X-Tenant-Purge-At') ?? undefined
    })
  }

  if (response.ok) return

  // 402 Payment Required = paywall full block.
  if (response.status === 402) {
    let body: { error?: string; error_code?: string; tenant_status?: string } | null = null
    try {
      body = await response.clone().json()
    } catch {
      // pas grave, payload vide
    }
    dispatch<VeridianPaywallEventDetail>(VERIDIAN_PAYWALL_EVENT, {
      status: 402,
      error: body?.error,
      errorCode: body?.error_code,
      tenantStatus: body?.tenant_status
    })
    return
  }

  // 503 avec error_code=hub_sync_dead (lot H) = dégradation temporaire.
  if (response.status === 503) {
    let body: { error?: string; error_code?: string } | null = null
    try {
      body = await response.clone().json()
    } catch {
      // pas grave
    }
    if (body?.error_code === 'hub_sync_dead') {
      const retryAfterHeader = response.headers.get('Retry-After')
      dispatch<VeridianPaywallEventDetail>(VERIDIAN_HUB_SYNC_DEAD_EVENT, {
        status: 503,
        error: body?.error,
        errorCode: 'hub_sync_dead',
        retryAfter: retryAfterHeader ? parseInt(retryAfterHeader, 10) : undefined
      })
    }
  }
}

/**
 * Installe le wrap fetch. Idempotent. Doit être appelé une fois au boot
 * (cf. `main.tsx` ou `App.tsx`).
 */
export function installVeridianFetchInterceptor(): void {
  if (typeof window === 'undefined') return
  if (window.__VERIDIAN_INTERCEPTOR_INSTALLED__) return
  window.__VERIDIAN_INTERCEPTOR_INSTALLED__ = true

  const originalFetch = window.fetch.bind(window)

  window.fetch = async (input: RequestInfo | URL, init?: RequestInit) => {
    const response = await originalFetch(input, init)
    // Inspect en fire-and-forget : ne bloque pas la réponse originale.
    void inspectResponse(response).catch(() => {
      /* silencieux : un échec d'inspection ne doit jamais bloquer l'app */
    })
    return response
  }
}
