// === Veridian patch — wordmark console (ticket DA 2026-05-22) ===
//
// Logo de la console : wordmark texte « veridian.mail » — le produit
// emails de la suite Veridian, en cohérence avec la LP veridian.site
// (création de sites). Remplace le logo image Notifuse historique
// (public/logo.png) dans la sidebar et le SetupWizard.
//
// Wordmark texte (pas une image) : net à toute résolution, zéro asset à
// maintenir, et le suffixe `.mail` peut être teinté du violet de marque.
// Pattern standard des SaaS B2B modernes (Linear, Vercel…).

import { veridianColors } from '../theme/veridian'

interface VeridianLogoProps {
  /** Sidebar repliée → forme compacte (juste « v. »). */
  collapsed?: boolean
  /** Hauteur de police en px. Défaut 18 (sidebar). */
  size?: number
}

/**
 * Wordmark « veridian.mail ». `veridian` en gris foncé, `.mail` en violet
 * de marque. En mode `collapsed`, n'affiche que « v. ».
 */
export function VeridianLogo({ collapsed = false, size = 18 }: VeridianLogoProps) {
  const base: React.CSSProperties = {
    fontFamily: "'Inter', system-ui, sans-serif",
    fontWeight: 700,
    fontSize: size,
    lineHeight: 1,
    letterSpacing: '-0.02em',
    userSelect: 'none',
    whiteSpace: 'nowrap',
  }

  if (collapsed) {
    return (
      <span style={base} aria-label="veridian.mail">
        <span style={{ color: '#1f2430' }}>v</span>
        <span style={{ color: veridianColors.primary }}>.</span>
      </span>
    )
  }

  return (
    <span style={base} aria-label="veridian.mail">
      <span style={{ color: '#1f2430' }}>veridian</span>
      <span style={{ color: veridianColors.primary }}>.mail</span>
    </span>
  )
}
