/**
 * Veridian — toast "Bienvenue" après auto-login Hub.
 *
 * Backend `veridian_autologin_handler.go` redirige vers `/console` après
 * avoir stocké le token JWT dans localStorage. Pas de `?token=` dans
 * l'URL côté UI (sécurité — l'URL n'apparaît pas dans l'historique
 * navigateur).
 *
 * Stratégie de détection (faute de query param) :
 *   - `document.referrer` contient `/veridian/auto-login` au premier mount
 *   - Anti-spam : pose un flag localStorage `veridian_welcome_shown_at`
 *     pour ne montrer le toast qu'une fois par "session UI" (24h).
 *
 * Note session calme : si Robert préfère un détecteur plus fiable, on peut
 * patcher le backend pour ajouter `?welcome=1` au redirect — ticket
 * trivial.
 *
 * Si serveur renvoie 401 à un `?token=` explicite (cas magic link expiré),
 * la `SignInPage` upstream affiche déjà l'erreur — pas de double-traitement
 * ici.
 */

import { useEffect } from 'react'
import { App } from 'antd'
import { useLingui } from '@lingui/react/macro'
import { useAuth } from '../contexts/AuthContext'

const WELCOME_STORAGE_KEY = 'veridian_welcome_shown_at'
const WELCOME_DEBOUNCE_MS = 24 * 60 * 60 * 1000 // 24h

export function VeridianWelcomeToast() {
  const { t } = useLingui()
  const { notification } = App.useApp()
  const { user, isAuthenticated } = useAuth()

  useEffect(() => {
    if (!isAuthenticated || !user) return

    // Anti-double-toast : si déjà montré dans les dernières 24h, skip.
    try {
      const lastShownStr = window.localStorage.getItem(WELCOME_STORAGE_KEY)
      if (lastShownStr) {
        const lastShown = parseInt(lastShownStr, 10)
        if (Number.isFinite(lastShown) && Date.now() - lastShown < WELCOME_DEBOUNCE_MS) {
          return
        }
      }
    } catch {
      // localStorage indisponible : on tolère un toast par mount, pas
      // dramatique.
    }

    // Détection auto-login Hub via document.referrer. Si le user vient
    // directement (refresh page console), pas de welcome.
    const referrer = document.referrer || ''
    const isAutoLogin = referrer.includes('/veridian/auto-login') || referrer.includes('app.veridian.site')

    if (!isAutoLogin) return

    notification.success({
      message: t`Welcome, ${user.email}`,
      description: t`You are now signed in via Veridian.`,
      duration: 5
    })

    try {
      window.localStorage.setItem(WELCOME_STORAGE_KEY, String(Date.now()))
    } catch {
      // pas grave
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps -- t/notification stables, on déclenche au login
  }, [isAuthenticated, user?.id])

  return null
}
