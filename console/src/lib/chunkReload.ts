// Chunk perime apres un deploiement : l'onglet ouvert sur l'ancienne version demande un
// chunk que le nouveau build a supprime (« Failed to fetch dynamically imported module »,
// 19/10/2026). On recharge UNE fois la page (le nouvel index.html pointe vers les bons
// chunks) ; si l'erreur revient dans la minute, on affiche un message au lieu de boucler.

const KEY = 'veridian_chunk_reload_at'
const WINDOW_MS = 60_000
const CHUNK_ERROR_RE =
  /Failed to fetch dynamically imported module|error loading dynamically imported module|Importing a module script failed|Unable to preload CSS/i

interface Deps {
  storage: Pick<Storage, 'getItem' | 'setItem'>
  reload: () => void
  showMessage: () => void
  now: () => number
}

export function isChunkLoadError(reason: unknown): boolean {
  const msg = reason instanceof Error ? reason.message : typeof reason === 'string' ? reason : ''
  return CHUNK_ERROR_RE.test(msg)
}

/** Un seul rechargement par fenetre de 60 s ; sinon message. Retourne l'action choisie. */
export function recoverFromChunkError(deps: Deps): 'reload' | 'message' {
  let last = 0
  try {
    last = Number(deps.storage.getItem(KEY)) || 0
  } catch {
    // stockage indisponible : pas de garde possible, on affiche le message (jamais de boucle)
    deps.showMessage()
    return 'message'
  }
  if (last && deps.now() - last < WINDOW_MS) {
    deps.showMessage()
    return 'message'
  }
  try {
    deps.storage.setItem(KEY, String(deps.now()))
  } catch {
    deps.showMessage()
    return 'message'
  }
  deps.reload()
  return 'reload'
}

function showBanner() {
  if (document.getElementById('veridian-chunk-banner')) return
  const el = document.createElement('div')
  el.id = 'veridian-chunk-banner'
  el.setAttribute('role', 'alert')
  el.style.cssText =
    'position:fixed;top:0;left:0;right:0;z-index:99999;padding:12px 16px;background:#fff7e6;color:#613400;border-bottom:1px solid #ffd591;font:14px sans-serif;text-align:center'
  el.textContent =
    'La console a été mise à jour. Rechargez la page (Ctrl+F5) pour continuer. / The console was updated. Reload the page (Ctrl+F5) to continue.'
  document.body.appendChild(el)
}

export function installChunkReloadGuard(win: Window = window): void {
  const deps: Deps = {
    storage: win.sessionStorage,
    reload: () => win.location.reload(),
    showMessage: showBanner,
    now: () => Date.now()
  }
  // Evenement emis par Vite quand le preload d'un chunk dynamique echoue.
  win.addEventListener('vite:preloadError', (event) => {
    event.preventDefault()
    recoverFromChunkError(deps)
  })
  // import() de React.lazy qui rejette sans passer par le preload.
  win.addEventListener('unhandledrejection', (event) => {
    if (isChunkLoadError((event as PromiseRejectionEvent).reason)) {
      event.preventDefault()
      recoverFromChunkError(deps)
    }
  })
}
