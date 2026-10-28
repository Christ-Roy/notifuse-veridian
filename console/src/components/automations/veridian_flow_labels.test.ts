import { describe, it, expect } from 'vitest'
import { emailNodeTitle, MIN_SAMPLE_FOR_RATE, replyRatePercent, stageForNode } from './veridian_flow_labels'
import type { ProspectionSequence } from '../../services/api/veridian_prospection'

const sequence = {
  automation_id: 'a1',
  name: 'prospection5mails',
  status: 'live',
  list_id: 'l1',
  enrolled: 1797,
  stages: [
    { stage: 1, label: 'J0', node_ids: ['j0a', 'j0b'], template_ids: ['tplA'] },
    { stage: 2, label: 'J+5', node_ids: ['j5'], template_ids: ['tplB'] }
  ]
} as unknown as ProspectionSequence

describe('libellés du flux d automation', () => {
  it('titre d un nœud email : « J+5 · Relance 1 »', () => {
    expect(emailNodeTitle('J+5', 'Relance 1', 'Email')).toBe('J+5 · Relance 1')
  })

  it('sans étape connue : seul le nom du modèle', () => {
    expect(emailNodeTitle(undefined, 'Relance 1', 'Email')).toBe('Relance 1')
  })

  it('sans modèle ou nom vide : repli sur le libellé générique', () => {
    expect(emailNodeTitle('J+5', undefined, 'Email')).toBe('J+5 · Email')
    expect(emailNodeTitle('J+5', '   ', 'Email')).toBe('J+5 · Email')
  })

  it('retrouve l étape d un nœud par ses node_ids (A/B compris)', () => {
    expect(stageForNode(sequence, 'j0b')?.label).toBe('J0')
    expect(stageForNode(sequence, 'j5')?.label).toBe('J+5')
    expect(stageForNode(sequence, 'inconnu')).toBeUndefined()
    expect(stageForNode(undefined, 'j5')).toBeUndefined()
  })

  it('taux de réponse : pourcentage, null sans envoi', () => {
    expect(replyRatePercent(20, 74)).toBeCloseTo(27.03, 2)
    expect(replyRatePercent(0, 0)).toBeNull()
  })

  it('seuil d effectif pour désigner un meilleur modèle : 100 envois', () => {
    expect(MIN_SAMPLE_FOR_RATE).toBe(100)
  })
})
