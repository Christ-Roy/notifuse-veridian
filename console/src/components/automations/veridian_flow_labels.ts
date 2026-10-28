import type { ProspectionSequence, ProspectionStage } from '../../services/api/veridian_prospection'

// Effectif minimal (envois d'une étape) avant de comparer des taux et de désigner un meilleur
// modèle. Sous ce seuil, le taux s'affiche mais n'est jamais mis en avant.
export const MIN_SAMPLE_FOR_RATE = 100

// Étape d'un nœud : chaque nœud email appartient à une seule étape (A/B compris, les variantes
// se rejoignent à la même étape). Pas d'étape connue : undefined.
export function stageForNode(sequence: ProspectionSequence | undefined, nodeId: string): ProspectionStage | undefined {
  return sequence?.stages.find((stage) => stage.node_ids.includes(nodeId))
}

// « J+5 · Relance 1 ». Sans étape, le nom seul ; sans nom de modèle, le libellé générique.
export function emailNodeTitle(stageLabel: string | undefined, templateName: string | undefined, fallback: string): string {
  const name = templateName?.trim() || fallback
  return stageLabel ? `${stageLabel} · ${name}` : name
}

// Taux de réponse humaine en pourcentage (0 à 100). null sans envoi.
export function replyRatePercent(replies: number, sent: number): number | null {
  return sent > 0 ? (replies / sent) * 100 : null
}
