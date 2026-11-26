/**
 * « Mon IA » dans la console (VER-203, CONTRAT-HUB §5.24.1, fiche 99).
 *
 * Le serveur demande l'état au Hub (GET /api/veridian/ia/etat) et le rend tel quel. La règle d'affichage vit ICI, en un seul endroit :
 *   prete        : bouton « Mon IA », cadre {HUB}/ia/chat?app=notifuse&tenant=<tenant_id> inséré seulement à l'ouverture ;
 *   sans_moteur  : bouton « Relier mon IA » et carte, JAMAIS le cadre ;
 *   tout le reste (éteinte, 404, Hub muet, 5xx, réponse illisible) : rien.
 */

export type EtatMonIA = 'prete' | 'sans_moteur' | 'eteinte' | 'indisponible'
export type AffichageMonIA = 'cadre' | 'carte' | 'rien'

export interface ReponseEtatMonIA {
  etat?: string
  hub_url?: string
  tenant_id?: string
}

/** Ce que l'état permet d'afficher. Seul `prete` ouvre le cadre : une fenêtre qui refuserait chaque question ne s'affiche jamais. */
export function affichageMonIA(etat: string | undefined | null): AffichageMonIA {
  if (etat === 'prete') return 'cadre'
  if (etat === 'sans_moteur') return 'carte'
  return 'rien'
}

const UUID = /^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/i

/** Une adresse de Hub utilisable : https, sans identifiants, sans chemin. Sinon rien n'est montré. */
export function hubOrigin(raw: string | undefined): string | null {
  if (!raw) return null
  try {
    const u = new URL(raw)
    if (u.protocol !== 'https:' || u.username || u.password) return null
    return u.origin
  } catch {
    return null
  }
}

/** Le cadre : {HUB}/ia/chat?app=notifuse&tenant=<tenant_id>. Le tenant est l'organisation du Hub (UUID), jamais l'identifiant d'espace. */
export function urlCadreMonIA(hub: string, tenantId: string): string | null {
  const origin = hubOrigin(hub)
  if (!origin || !UUID.test(tenantId)) return null
  return `${origin}/ia/chat?app=notifuse&tenant=${encodeURIComponent(tenantId)}`
}

/** Le renvoi de la carte : {HUB}/dashboard/ia. */
export function urlRelierMonIA(hub: string): string | null {
  const origin = hubOrigin(hub)
  return origin ? `${origin}/dashboard/ia` : null
}
