// Thème de la boîte unifiée : clair ou sombre d'après les tokens Ant Design en cours, couleurs de nature.
import { theme } from 'antd'

import type { MailKind } from '../../services/api/veridian_mail'
import { hasOwnColors } from './veridian_mail_frame_rules'
import { KIND_STYLE, themeModeFromBackground, type KindStyle, type ThemeMode } from './veridian_mail_rules'

export type FrameSurface = 'auto' | 'light' | 'dark'

/** Thème effectif du papier : un mail qui pose ses propres couleurs reste sur papier clair, même dans la console sombre. */
export function effectiveFrameMode(themeMode: ThemeMode, html: string, surface: FrameSurface): ThemeMode {
  if (surface === 'light') return 'light'
  if (surface === 'dark') return 'dark'
  if (themeMode === 'dark' && hasOwnColors(html)) return 'light'
  return themeMode
}

/** Thème de la console (clair ou sombre), lu dans les tokens Ant Design en cours. */
export function useMailThemeMode(): ThemeMode {
  const { token } = theme.useToken()
  return themeModeFromBackground(token.colorBgContainer)
}

export function useKindStyle(kind: MailKind): KindStyle {
  const mode = useMailThemeMode()
  return (KIND_STYLE[kind] ?? KIND_STYLE.other)[mode]
}
