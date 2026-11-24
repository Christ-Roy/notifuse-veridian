import { useEffect, type ReactNode } from 'react'
import { ConfigProvider } from 'antd'
import { useQuery } from '@tanstack/react-query'
import { useLingui } from '@lingui/react/macro'
import { api } from '../services/api/client'

// Fiche 88 : la marque de l'organisation vient du Hub (VER-252). Sans poussée du Hub, rien ne change à l'écran.
export interface VeridianOrganisation {
  managed: boolean
  name?: string
  type?: string
  brand?: {
    nom_affiche?: string
    logo_url?: string
    logo_sombre_url?: string
    favicon_url?: string
    couleur_primaire?: string
    couleur_accent?: string
  }
}

export function useVeridianOrganisation(workspaceId: string | undefined): VeridianOrganisation | null {
  const { data } = useQuery<VeridianOrganisation | null>({
    queryKey: ['veridian-organisation', workspaceId],
    queryFn: async () => {
      try {
        return await api.get<VeridianOrganisation>(
          `/api/veridian/organisation?workspace_id=${encodeURIComponent(workspaceId ?? '')}`
        )
      } catch {
        return null
      }
    },
    enabled: !!workspaceId,
    staleTime: 5 * 60 * 1000
  })
  return data ?? null
}

const channel = (v: number) => {
  const c = v / 255
  return c <= 0.03928 ? c / 12.92 : Math.pow((c + 0.055) / 1.055, 2.4)
}

/** La couleur de la marque, seulement si un texte blanc y reste lisible (contraste de 3 au moins). */
export function usableBrandColor(color: string | undefined): string | undefined {
  if (!color || !/^#[0-9a-fA-F]{6}$/.test(color)) return undefined
  const [r, g, b] = [1, 3, 5].map((i) => channel(parseInt(color.slice(i, i + 2), 16)))
  const luminance = 0.2126 * r + 0.7152 * g + 0.0722 * b
  return 1.05 / (luminance + 0.05) >= 3 ? color : undefined
}

export function VeridianBrandProvider({ workspaceId, children }: { workspaceId: string; children: ReactNode }) {
  const organisation = useVeridianOrganisation(workspaceId)
  const primary = usableBrandColor(organisation?.brand?.couleur_primaire)
  const favicon = organisation?.brand?.favicon_url

  useEffect(() => {
    if (!favicon) return
    const link = document.createElement('link')
    link.rel = 'icon'
    link.href = favicon
    link.setAttribute('data-veridian-brand', 'true')
    document.head.appendChild(link)
    return () => link.remove()
  }, [favicon])

  return (
    <ConfigProvider theme={primary ? { token: { colorPrimary: primary, colorLink: primary, colorLinkHover: primary } } : {}}>
      {children}
    </ConfigProvider>
  )
}

export function VeridianOrganisationNotice() {
  const { t } = useLingui()
  return (
    <p className="text-sm text-gray-500" data-testid="organisation-notice">
      {t`The name and logo come from your Veridian organization and are changed there.`}
    </p>
  )
}
