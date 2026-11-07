import { Outlet, useNavigate, useMatch } from '@tanstack/react-router'
import { Spin } from 'antd'
import { useLingui } from '@lingui/react/macro'
import { useAuth } from '../contexts/AuthContext'
import { useEffect } from 'react'
import { veridianApi } from '../services/api/veridian'
import { autoBounceTarget, markAutoBounceAttempt } from '../lib/veridian_auto_bounce'

export function RootLayout() {
  const { t } = useLingui()
  const { isAuthenticated, loading, workspaces } = useAuth()
  const navigate = useNavigate()

  const isSigninRoute = useMatch({ from: '/console/signin', shouldThrow: false })
  const isAcceptInvitationRoute = useMatch({
    from: '/console/accept-invitation',
    shouldThrow: false
  })
  const isLogoutRoute = useMatch({ from: '/console/logout', shouldThrow: false })
  const isWorkspaceCreateRoute = useMatch({ from: '/console/workspace/create', shouldThrow: false })
  const isSetupRoute = useMatch({ from: '/console/setup', shouldThrow: false })

  // Check if system is installed (explicitly check for true to handle undefined case)
  const isInstalled = window.IS_INSTALLED === true

  const isPublicRoute = isSigninRoute || isAcceptInvitationRoute || isLogoutRoute || isSetupRoute

  // If system is not installed, redirect to setup wizard
  const shouldRedirectToSetup = !isInstalled && !isSetupRoute

  // If not authenticated and not on public routes, redirect to signin
  const shouldRedirectToSignin =
    !isLogoutRoute && !isSigninRoute && !isAuthenticated && !isPublicRoute && !shouldRedirectToSetup

  // If authenticated and has no workspaces, redirect to workspace creation
  const shouldRedirectToCreateWorkspace =
    isAuthenticated && workspaces.length === 0 && !isWorkspaceCreateRoute && !isLogoutRoute

  // console.log('isAuthenticated', isAuthenticated)
  // handle redirection...
  useEffect(() => {
    if (loading) return

    if (shouldRedirectToSetup) {
      navigate({ to: '/console/setup' })
      return
    }

    if (shouldRedirectToSignin) {
      // Check if we're already on the signin pathname to avoid unnecessary navigation
      // This handles race conditions where route matching hasn't completed yet
      const currentPathname = window.location.pathname
      if (currentPathname === '/console/signin') {
        // Already on signin route, don't navigate
        return
      }

      const redirectToSignin = () => {
        // Preserve search parameters when redirecting to signin
        const currentSearch = window.location.search
        const searchParams = new URLSearchParams(currentSearch)
        const search: { email?: string } = {}

        // Preserve email parameter if present
        if (searchParams.has('email')) {
          search.email = searchParams.get('email') || undefined
        }

        navigate({
          to: '/console/signin',
          search: Object.keys(search).length > 0 ? search : undefined,
          replace: true
        })
      }

      // Veridian (CONTRAT-HUB 6bis.8.0) : une visite sans session repart vers le Hub, une seule fois,
      // jamais après une déconnexion explicite ni dans une iframe. Sinon : page de connexion.
      let cancelled = false
      void (async () => {
        const mode = await veridianApi.getMode().catch(() => null)
        if (cancelled) return
        const target = autoBounceTarget({
          mode: mode?.mode,
          hubUrl: mode?.hub_url,
          pathname: window.location.pathname,
          href: window.location.href,
          cookie: document.cookie,
          isTopWindow: window.self === window.top,
          hasToken: !!localStorage.getItem('auth_token')
        })
        if (target) {
          markAutoBounceAttempt()
          window.location.replace(target)
          return
        }
        redirectToSignin()
      })()
      return () => {
        cancelled = true
      }
    }

    if (shouldRedirectToCreateWorkspace) {
      navigate({ to: '/console/workspace/create' })
      return
    }
  }, [loading, shouldRedirectToSetup, shouldRedirectToSignin, shouldRedirectToCreateWorkspace, navigate])

  if (
    loading ||
    shouldRedirectToSetup ||
    shouldRedirectToSignin ||
    shouldRedirectToCreateWorkspace
  ) {
    return (
      <div
        style={{ display: 'flex', justifyContent: 'center', alignItems: 'center', height: '100vh' }}
      >
        <Spin size="large" tip={t`Loading...`} fullscreen />
      </div>
    )
  }

  return <Outlet />
}
