import { useEffect, lazy, Suspense } from 'react'
import type { ComponentType } from 'react'
import { Spin } from 'antd'
import { createRootRoute, createRoute, useParams, useNavigate } from '@tanstack/react-router'
import { RootLayout } from './layouts/RootLayout'
import { WorkspaceLayout } from './layouts/WorkspaceLayout'
import { SignInPage } from './pages/SignInPage'
import { LogoutPage } from './pages/LogoutPage'
import { AcceptInvitationPage } from './pages/AcceptInvitationPage'
import SetupWizard from './pages/SetupWizard'
import { createRouter } from '@tanstack/react-router'

// === Veridian patch — perf-ui-baseline (ticket 2026-05-22) ===
// Sans lazy-loading, TanStack Router importe TOUTES les pages en
// statique → tout le code applicatif (dashboard, contacts, settings,
// email builder, automations, analytics…) part dans le chunk initial,
// même pour afficher l'écran de login.
//
// On garde en import statique uniquement le chemin critique AVANT
// authentification (SignIn, Logout, AcceptInvitation, Setup) — c'est ce
// qu'il faut pour le premier paint. Toutes les pages post-login passent
// en React.lazy() : leur code n'est téléchargé qu'à la navigation vers
// l'écran. Les pages lourdes (Automations→@xyflow, Analytics→echarts,
// Templates/Broadcasts/Blog→email builder+Monaco) y gagnent le plus,
// mais sortir aussi les pages Antd « simples » vide le chunk initial.
//
// veridianLazyPage wrappe React.lazy dans un Suspense local (spinner
// Antd) pour que TanStack Router puisse rendre le composant sans
// Suspense global dans App.tsx.
function veridianLazyPage(
  loader: () => Promise<{ [key: string]: ComponentType<unknown> }>,
  exportName: string
): ComponentType {
  const LazyComp = lazy(async () => {
    const mod = await loader()
    return { default: mod[exportName] }
  })
  return function VeridianLazyRoute() {
    return (
      <Suspense
        fallback={
          <div style={{ display: 'flex', justifyContent: 'center', padding: 64 }}>
            <Spin size="large" />
          </div>
        }>
        <LazyComp />
      </Suspense>
    )
  }
}

// Pages post-login — toutes lazy-loadées.
const DashboardPage = veridianLazyPage(() => import('./pages/DashboardPage'), 'DashboardPage')
const CreateWorkspacePage = veridianLazyPage(
  () => import('./pages/CreateWorkspacePage'),
  'CreateWorkspacePage'
)
const WorkspaceSettingsPage = veridianLazyPage(
  () => import('./pages/WorkspaceSettingsPage'),
  'WorkspaceSettingsPage'
)
const ContactsPage = veridianLazyPage(() => import('./pages/ContactsPage'), 'ContactsPage')
const ListsPage = veridianLazyPage(() => import('./pages/ListsPage'), 'ListsPage')
const FileManagerPage = veridianLazyPage(() => import('./pages/FileManagerPage'), 'FileManagerPage')
const TransactionalNotificationsPage = veridianLazyPage(
  () => import('./pages/TransactionalNotificationsPage'),
  'TransactionalNotificationsPage'
)
const LogsPage = veridianLazyPage(() => import('./pages/LogsPage'), 'LogsPage')
const DebugSegmentPage = veridianLazyPage(
  () => import('./pages/DebugSegmentPage'),
  'DebugSegmentPage'
)
const AutomationsPage = veridianLazyPage(() => import('./pages/AutomationsPage'), 'AutomationsPage')
const AnalyticsPage = veridianLazyPage(() => import('./pages/AnalyticsPage'), 'AnalyticsPage')
const TemplatesPage = veridianLazyPage(() => import('./pages/TemplatesPage'), 'TemplatesPage')
const BroadcastsPage = veridianLazyPage(() => import('./pages/BroadcastsPage'), 'BroadcastsPage')
const BlogPage = veridianLazyPage(() => import('./pages/BlogPage'), 'BlogPage')

export interface ContactsSearch {
  email?: string
  external_id?: string
  first_name?: string
  last_name?: string
  full_name?: string
  phone?: string
  country?: string
  language?: string
  list_id?: string
  contact_list_status?: string
  segments?: string[]
  limit?: number
}

export interface SignInSearch {
  email?: string
}

export interface AcceptInvitationSearch {
  token?: string
}

export interface BlogSearch {
  status?: string
  category_id?: string
}

export interface FileManagerSearch {
  path?: string
}

// Create the root route
const rootRoute = createRootRoute({
  component: RootLayout
})

// Create the index route
const indexRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: '/console/',
  component: DashboardPage
})

// Create the signin route
const signinRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: '/console/signin',
  component: SignInPage,
  validateSearch: (search: Record<string, unknown>): SignInSearch => ({
    email: search.email as string | undefined
  })
})

// Create the logout route
const logoutRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: '/console/logout',
  component: LogoutPage
})

// Create the setup wizard route
const setupRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: '/console/setup',
  component: SetupWizard
})

// Create the accept invitation route
const acceptInvitationRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: '/console/accept-invitation',
  component: AcceptInvitationPage,
  validateSearch: (search: Record<string, unknown>): AcceptInvitationSearch => ({
    token: search.token as string | undefined
  })
})

// Create the workspace create route
const workspaceCreateRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: '/console/workspace/create',
  component: CreateWorkspacePage
})

// Create the workspace route
const workspaceRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: '/console/workspace/$workspaceId',
  component: WorkspaceLayout
})

// Create the default workspace route (redirects to analytics/dashboard)
const workspaceIndexRoute = createRoute({
  getParentRoute: () => workspaceRoute,
  path: '/',
  component: AnalyticsPage
})

// Create workspace child routes
const workspaceBroadcastsRoute = createRoute({
  getParentRoute: () => workspaceRoute,
  path: '/broadcasts',
  component: BroadcastsPage
})

const workspaceAutomationsRoute = createRoute({
  getParentRoute: () => workspaceRoute,
  path: '/automations',
  component: AutomationsPage
})

const workspaceListsRoute = createRoute({
  getParentRoute: () => workspaceRoute,
  path: '/lists',
  component: ListsPage
})

export const workspaceFileManagerRoute = createRoute({
  getParentRoute: () => workspaceRoute,
  path: '/file-manager',
  component: FileManagerPage,
  validateSearch: (search: Record<string, unknown>): FileManagerSearch => ({
    path: search.path as string | undefined
  })
})

const workspaceTransactionalNotificationsRoute = createRoute({
  getParentRoute: () => workspaceRoute,
  path: '/transactional-notifications',
  component: TransactionalNotificationsPage
})

const workspaceLogsRoute = createRoute({
  getParentRoute: () => workspaceRoute,
  path: '/logs',
  component: LogsPage
})

export const workspaceContactsRoute = createRoute({
  getParentRoute: () => workspaceRoute,
  path: '/contacts',
  component: ContactsPage,
  validateSearch: (search: Record<string, unknown>): ContactsSearch => ({
    email: search.email as string | undefined,
    external_id: search.external_id as string | undefined,
    first_name: search.first_name as string | undefined,
    last_name: search.last_name as string | undefined,
    full_name: search.full_name as string | undefined,
    phone: search.phone as string | undefined,
    country: search.country as string | undefined,
    language: search.language as string | undefined,
    list_id: search.list_id as string | undefined,
    contact_list_status: search.contact_list_status as string | undefined,
    segments: Array.isArray(search.segments)
      ? (search.segments as string[])
      : search.segments
        ? [search.segments as string]
        : undefined,
    limit: search.limit ? Number(search.limit) : 10
  })
})

// eslint-disable-next-line react-refresh/only-export-components -- Internal redirect component
const WorkspaceSettingsRedirect = () => {
  const { workspaceId } = useParams({ from: '/console/workspace/$workspaceId/settings' })
  const navigate = useNavigate()

  useEffect(() => {
    navigate({
      to: '/console/workspace/$workspaceId/settings/$section',
      params: { workspaceId, section: 'team' },
      replace: true
    })
  }, [workspaceId, navigate])

  return null
}

const workspaceSettingsRedirectRoute = createRoute({
  getParentRoute: () => workspaceRoute,
  path: '/settings',
  component: WorkspaceSettingsRedirect
})

// === Veridian patch — fail-safe route /dashboard (bug P0 2026-06-14) ===
// Le Dashboard vit sur l'index workspace (`/console/workspace/$id`), pas sur
// `/dashboard`. Mais un ancien bundle (servi par un cache navigateur), un
// bookmark, ou un lien externe peuvent pointer vers `/console/workspace/$id/
// dashboard` — qui n'existait PAS → defaultNotFoundComponent de TanStack →
// "Not Found" sur le premier écran (bug audité par Robert). On ajoute une route
// `/dashboard` qui REDIRIGE vers l'index : peu importe d'où vient le lien, on
// atterrit sur le Dashboard, jamais sur "Not Found". Fail-safe, pas de doublon
// de page (l'écran reste l'index/AnalyticsPage).
// eslint-disable-next-line react-refresh/only-export-components -- Internal redirect component
const WorkspaceDashboardRedirect = () => {
  const { workspaceId } = useParams({ from: '/console/workspace/$workspaceId/dashboard' })
  const navigate = useNavigate()

  useEffect(() => {
    navigate({
      to: '/console/workspace/$workspaceId',
      params: { workspaceId },
      replace: true
    })
  }, [workspaceId, navigate])

  return null
}

const workspaceDashboardRedirectRoute = createRoute({
  getParentRoute: () => workspaceRoute,
  path: '/dashboard',
  component: WorkspaceDashboardRedirect
})

const workspaceSettingsRoute = createRoute({
  getParentRoute: () => workspaceRoute,
  path: '/settings/$section',
  component: WorkspaceSettingsPage
})

const workspaceTemplatesRoute = createRoute({
  getParentRoute: () => workspaceRoute,
  path: '/templates',
  component: TemplatesPage
})

const workspaceAnalyticsRoute = createRoute({
  getParentRoute: () => workspaceRoute,
  path: '/analytics',
  component: AnalyticsPage
})

const workspaceNewSegmentRoute = createRoute({
  getParentRoute: () => workspaceRoute,
  path: '/debug-segment',
  component: DebugSegmentPage
})

const workspaceBlogRoute = createRoute({
  getParentRoute: () => workspaceRoute,
  path: '/blog',
  component: BlogPage,
  validateSearch: (search: Record<string, unknown>): BlogSearch => ({
    status: search.status as string | undefined,
    category_id: search.category_id as string | undefined
  })
})

// Create the router
const routeTree = rootRoute.addChildren([
  indexRoute,
  signinRoute,
  logoutRoute,
  setupRoute,
  acceptInvitationRoute,
  workspaceCreateRoute,
  workspaceRoute.addChildren([
    workspaceIndexRoute,
    workspaceDashboardRedirectRoute,
    workspaceBroadcastsRoute,
    workspaceAutomationsRoute,
    workspaceContactsRoute,
    workspaceListsRoute,
    workspaceTransactionalNotificationsRoute,
    workspaceLogsRoute,
    workspaceFileManagerRoute,
    workspaceSettingsRedirectRoute,
    workspaceSettingsRoute,
    workspaceTemplatesRoute,
    workspaceAnalyticsRoute,
    workspaceNewSegmentRoute,
    workspaceBlogRoute
  ])
])

// Create and export the router with explicit type
export const router = createRouter({
  routeTree
})

declare module '@tanstack/react-router' {
  interface Register {
    router: typeof router
  }
}
