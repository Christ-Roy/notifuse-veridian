import type { ProspectionSequence, ProspectionStage } from '../../services/api/veridian_prospection'
import { MIN_SAMPLE_FOR_RATE, replyRatePercent } from '../automations/veridian_flow_labels'

// Une ligne du tableau « Performance des campagnes » : une étape d'une séquence (un modèle, ou
// les variantes A/B de l'étape). Module pur, sans React.
export interface PerformanceRow {
  key: string
  automationId: string
  sequence: string
  stage: number
  label: string
  models: string[]
  enrolled: number
  sent: number
  replies: number
  // Pourcentage 0-100 ; null sans envoi.
  rate: number | null
  unsubscribed: number
  rejected: number
  repliedExits: number
  eligible: boolean
  best: boolean
}

// `modelName` rend le nom d'un modèle à partir de son identifiant (repli côté appelant).
export function buildPerformanceRows(
  sequences: ProspectionSequence[],
  modelName: (templateId: string) => string
): PerformanceRow[] {
  const rows = sequences.flatMap((sequence) =>
    sequence.stages.map((stage: ProspectionStage) => {
      const rate = replyRatePercent(stage.replies_human, stage.sent)
      return {
        key: `${sequence.automation_id}-${stage.stage}`,
        automationId: sequence.automation_id,
        sequence: sequence.name,
        stage: stage.stage,
        label: stage.label,
        models: (stage.template_ids ?? []).map(modelName),
        enrolled: sequence.enrolled,
        sent: stage.sent,
        replies: stage.replies_human,
        rate,
        unsubscribed: stage.exited_unsubscribed,
        rejected: stage.exited_rejected,
        repliedExits: stage.exited_replied,
        eligible: stage.sent >= MIN_SAMPLE_FOR_RATE,
        best: false
      }
    })
  )

  // Meilleur par séquence : taux le plus haut parmi les étapes à effectif suffisant, taux > 0.
  // Les ex aequo sont tous marqués.
  const best = new Map<string, number>()
  for (const row of rows) {
    if (row.eligible && row.rate !== null && row.rate > 0) {
      best.set(row.automationId, Math.max(best.get(row.automationId) ?? 0, row.rate))
    }
  }
  return rows.map((row) => ({
    ...row,
    best: row.eligible && row.rate !== null && row.rate > 0 && row.rate === best.get(row.automationId)
  }))
}
