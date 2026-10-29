// Aides de test de la boîte unifiée (jamais importées par l'application).
import React from 'react'
import { render, type RenderOptions } from '@testing-library/react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { I18nProvider } from '@lingui/react'
import { i18n } from '@lingui/core'
import { App as AntApp, ConfigProvider, theme } from 'antd'

i18n.loadAndActivate({ locale: 'en', messages: {} })

export function newQueryClient(): QueryClient {
  return new QueryClient({ defaultOptions: { queries: { retry: false, gcTime: 0, refetchOnWindowFocus: false }, mutations: { retry: false } } })
}

export function renderMail(ui: React.ReactElement, opts: { dark?: boolean; client?: QueryClient } & Omit<RenderOptions, 'wrapper'> = {}) {
  const client = opts.client ?? newQueryClient()
  const Wrapper = ({ children }: { children: React.ReactNode }) => (
    <QueryClientProvider client={client}>
      <I18nProvider i18n={i18n}>
        <ConfigProvider theme={opts.dark ? { algorithm: theme.darkAlgorithm } : undefined}>
          <AntApp>{children}</AntApp>
        </ConfigProvider>
      </I18nProvider>
    </QueryClientProvider>
  )
  return { client, ...render(ui, { wrapper: Wrapper, ...opts }) }
}

/** Simule la largeur de fenêtre pour les requêtes média min-width / max-width d'Ant Design. */
export function setViewport(width: number): void {
  Object.defineProperty(window, 'innerWidth', { writable: true, configurable: true, value: width })
  window.matchMedia = ((query: string) => {
    const min = /min-width:\s*(\d+)px/.exec(query)
    const max = /max-width:\s*(\d+)px/.exec(query)
    const matches = (min ? width >= Number(min[1]) : true) && (max ? width <= Number(max[1]) : true)
    return {
      matches,
      media: query,
      onchange: null,
      addListener: () => undefined,
      removeListener: () => undefined,
      addEventListener: () => undefined,
      removeEventListener: () => undefined,
      dispatchEvent: () => false
    } as unknown as MediaQueryList
  }) as typeof window.matchMedia
}

/** Remplace le composant Link du routeur par un lien simple (les tests de composants n'ont pas de routeur). */
export function fakeLink({ children, to, params, search, ...rest }: { children?: React.ReactNode; to?: string; params?: Record<string, string>; search?: unknown } & Record<string, unknown>) {
  let href = to ?? ''
  for (const [k, v] of Object.entries(params ?? {})) href = href.replace(`$${k}`, v)
  const qs = search ? `?${new URLSearchParams(search as Record<string, string>).toString()}` : ''
  const { style, className, 'aria-label': aria, 'data-testid': testId } = rest as Record<string, string | undefined> & { style?: React.CSSProperties }
  return (
    <a href={`${href}${qs}`} style={style} className={className} aria-label={aria} data-testid={testId}>
      {children}
    </a>
  )
}
