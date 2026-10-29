import type { ReconcileFinding, ReconcileReport } from '../../services/api/veridian_reconcile'

export function finding(over: Partial<ReconcileFinding> = {}): ReconcileFinding {
  return {
    id: 'f1',
    check: 'classes',
    severity: 'warning',
    subject_ref: 'classe:freemail_fr->orange',
    title: '800 contacts classés « freemail_fr » au lieu de « orange »',
    expected: 'classe « orange » (d’après le domaine : orange.fr)',
    observed: '« freemail_fr »',
    explanation: 'L’ancienne classe freemail_fr a été scindée le 22/10.',
    evidence: { count: 800, sample: ['a@orange.fr', 'b@wanadoo.fr'], definition: 'classe attendue = table de suffixes' },
    proposed_action: {
      kind: 'reclassify_contacts',
      label: 'Reclasser 800 contacts en « orange »',
      impact: 'Met custom_string_5 à « orange » sur ces contacts. Aucun mail n’est envoyé.',
      reversible: true,
      params: { from: 'freemail_fr', to: 'orange' }
    },
    first_seen_at: '2026-10-29T10:00:00Z',
    last_seen_at: '2026-10-29T12:00:00Z',
    resolved_at: null,
    resolved_by: '',
    resolution: null,
    ...over
  }
}

export function report(over: Partial<ReconcileReport> = {}): ReconcileReport {
  return {
    generated_at: '2026-10-29T12:00:00Z',
    ran: false,
    checks: [],
    findings: [finding()],
    summary: { open: 1, critical: 0, warning: 1, info: 0, not_reached: 0 },
    definitions: [{ key: 'inscrits', label: 'Inscrits d’une séquence', definition: 'Lignes de contact_automations.', source: 'contact_automations' }],
    ...over
  }
}
