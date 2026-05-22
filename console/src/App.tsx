import { ConfigProvider, App as AntApp } from 'antd'
// === Veridian patch — design system console (ticket DA 2026-05-22) ===
// Le thème Antd vit désormais dans theme/veridian.ts (avant : inline ici,
// squelettique). Inter chargée en self-host (avant : déclarée dans
// index.css mais jamais chargée → fallback silencieux system-ui).
import '@fontsource/inter/400.css'
import '@fontsource/inter/500.css'
import '@fontsource/inter/600.css'
import '@fontsource/inter/700.css'
import { veridianTheme } from './theme/veridian'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { RouterProvider } from '@tanstack/react-router'
import { I18nProvider } from '@lingui/react'
import { router } from './router'
import { AuthProvider } from './contexts/AuthContext'
import { LocaleProvider, useLocale, i18n } from './contexts/LocaleContext'
import { initializeAnalytics } from './utils/analytics-config'
// === Veridian patch === Global UX layer : modal paywall (402), toast
// hub_sync_dead (503), welcome toast post auto-login. Composants
// event-driven, ne rendent rien si pas d'event. Doivent vivre sous
// <AntApp> pour utiliser App.useApp().
import { VeridianPaywallModal } from './components/veridian_paywall_modal'
import { VeridianWelcomeToast } from './components/veridian_welcome_toast'
import enUS from 'antd/locale/en_US'
import frFR from 'antd/locale/fr_FR'
import esES from 'antd/locale/es_ES'
import deDE from 'antd/locale/de_DE'
import caES from 'antd/locale/ca_ES'
import type { Locale as AntdLocale } from 'antd/es/locale'
import type { Locale } from './i18n'

const antdLocales: Record<Locale, AntdLocale> = {
  en: enUS,
  fr: frFR,
  es: esES,
  de: deDE,
  ca: caES,
}

const queryClient = new QueryClient({
  defaultOptions: {
    queries: {
      refetchOnWindowFocus: false,
      retry: 1
    }
  }
})

// Initialize analytics service
initializeAnalytics()

// Inner component that uses LocaleContext
function AppContent() {
  const { locale } = useLocale()

  return (
    // key={locale} forces I18nProvider and all children to remount when locale changes,
    // ensuring all components re-render with the new translations
    <I18nProvider i18n={i18n} key={locale}>
      <ConfigProvider theme={veridianTheme} locale={antdLocales[locale]}>
        <AntApp>
          {/* === Veridian patch === Global UX components — voir veridian_*.tsx */}
          <VeridianPaywallModal />
          <VeridianWelcomeToast />
          <RouterProvider router={router} />
        </AntApp>
      </ConfigProvider>
    </I18nProvider>
  )
}

export function App() {
  return (
    <QueryClientProvider client={queryClient}>
      <AuthProvider>
        <LocaleProvider>
          <AppContent />
        </LocaleProvider>
      </AuthProvider>
    </QueryClientProvider>
  )
}

export default App
