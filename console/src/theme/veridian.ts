// === Veridian patch — design system console (ticket DA 2026-05-22) ===
//
// Source de vérité du thème Ant Design v5 de la console Veridian.
// Avant : le ThemeConfig vivait inline dans App.tsx, réduit à
// `colorPrimary` + quelques overrides éparpillés (la moitié en
// commentaires morts). Résultat : pas de couleurs sémantiques, pas de
// borderRadius cohérent, fond `#F9F9F9` réinjecté à la main une dizaine
// de fois, font Inter déclarée mais jamais appliquée au thème.
//
// Ce fichier centralise les tokens. Principe « 80/20, on ne casse
// rien » : on garde le violet de marque #7763F1 et l'esthétique Antd
// sobre existante, on se contente de la rendre COHÉRENTE et explicite.
// Le polish fin écran par écran se fait en hot-reload (skill
// ui-polish-team) — pas ici.

import type { ThemeConfig } from 'antd'

// --- Palette de marque Veridian ---------------------------------------
// Le violet #7763F1 est la couleur primaire historique de la console.
// On la garde. Les variantes hover/active suivent l'échelle Antd
// (assombrissement progressif) pour des états interactifs cohérents.
export const veridianColors = {
  primary: '#7763F1',
  primaryHover: '#6553D9',
  primaryActive: '#5544C0',

  // Couleurs sémantiques — explicites plutôt que les défauts Antd, pour
  // qu'un changement de DA soit centralisé ici.
  success: '#52C41A',
  warning: '#FAAD14',
  error: '#FF4D4F',
  info: '#7763F1', // aligné sur le primary (pas le bleu Antd par défaut)

  // Surfaces — `#F9F9F9` était réinjecté en dur partout (Layout, Card,
  // Drawer, Modal, Timeline). Centralisé ici comme `surface`.
  surface: '#F9F9F9',
  bgLayout: '#F9F9F9',
} as const

// --- Échelle typographique --------------------------------------------
// Inter chargée en self-host via @fontsource/inter (cf. main.tsx).
// Avant, `font-family: Inter` était déclaré dans index.css mais aucune
// font n'était chargée → fallback silencieux sur system-ui.
export const veridianFontFamily =
  "'Inter', -apple-system, BlinkMacSystemFont, 'Segoe UI', Roboto, 'Helvetica Neue', Arial, sans-serif"

// --- ThemeConfig Ant Design v5 ----------------------------------------
export const veridianTheme: ThemeConfig = {
  token: {
    colorPrimary: veridianColors.primary,
    colorLink: veridianColors.primary,
    colorLinkHover: veridianColors.primaryHover,
    colorSuccess: veridianColors.success,
    colorWarning: veridianColors.warning,
    colorError: veridianColors.error,
    colorInfo: veridianColors.info,

    colorBgLayout: veridianColors.bgLayout,

    fontFamily: veridianFontFamily,

    // borderRadius unifié : avant, Card était forcé à 4px tandis que le
    // reste de l'app restait au défaut Antd 6px → incohérence visible.
    // On fixe 6px partout (le défaut Antd v5, esthétique moderne).
    borderRadius: 6,
  },
  components: {
    Layout: {
      bodyBg: veridianColors.surface,
      lightSiderBg: veridianColors.surface,
      siderBg: veridianColors.surface,
    },
    Card: {
      headerFontSize: 16,
      colorBgContainer: veridianColors.surface,
      colorBorderSecondary: 'var(--color-gray-200)',
      // borderRadius géré par le token global (6px) — plus d'override 4px.
    },
    Table: {
      headerBg: 'transparent',
      fontSize: 12,
      colorTextHeading: 'rgb(51 65 85)',
      colorBgContainer: 'transparent',
      rowHoverBg: 'transparent',
      // headerBg=transparent ne couvre PAS la colonne triée : Antd a des tokens
      // dédiés (headerSortActiveBg / headerSortHoverBg) qui, sans override,
      // résolvaient en noir → en-tête noir illisible sur la colonne triée par
      // défaut (ex. "Sent" dans le tableau engagement par classe). On les aligne
      // sur transparent comme le reste de l'en-tête.
      headerSortActiveBg: 'transparent',
      headerSortHoverBg: 'transparent',
    },
    Drawer: {
      colorBgElevated: veridianColors.surface,
    },
    Modal: {
      colorBgElevated: veridianColors.surface,
    },
    Timeline: {
      dotBg: veridianColors.surface,
    },
  },
}
