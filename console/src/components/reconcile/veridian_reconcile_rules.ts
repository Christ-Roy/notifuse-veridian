import type { ReconcileCheck, ReconcileFinding, ReconcileSeverity } from '../../services/api/veridian_reconcile'

// Règles pures de la page « Écarts » : ordre, regroupement, lecture de la preuve.
// Aucune chaîne traduisible ici (les libellés sont rendus par les composants).

const SEVERITY_RANK: Record<ReconcileSeverity, number> = { critical: 3, warning: 2, info: 1 }

/** Les plus graves d'abord, puis les plus récemment constatés. */
export function sortFindings(findings: ReconcileFinding[]): ReconcileFinding[] {
  return [...findings].sort((a, b) => {
    const bySeverity = (SEVERITY_RANK[b.severity] ?? 0) - (SEVERITY_RANK[a.severity] ?? 0)
    if (bySeverity !== 0) return bySeverity
    return b.last_seen_at.localeCompare(a.last_seen_at)
  })
}

export function countBySeverity(findings: ReconcileFinding[]): Record<ReconcileSeverity, number> {
  const out: Record<ReconcileSeverity, number> = { critical: 0, warning: 0, info: 0 }
  for (const f of findings) {
    if (!f.resolved_at) out[f.severity] = (out[f.severity] ?? 0) + 1
  }
  return out
}

/** Contrôles qui n'ont pas pu conclure : à montrer, jamais à compter comme « ok ». */
export function notReached(checks: ReconcileCheck[]): ReconcileCheck[] {
  return checks.filter((c) => c.status === 'non_abouti')
}

export interface EvidenceLine {
  key: string
  value: string
}

/** Lignes de preuve lisibles : l'échantillon d'adresses et les nombres, sans le bruit. */
export function evidenceLines(evidence: Record<string, unknown>): EvidenceLine[] {
  const lines: EvidenceLine[] = []
  for (const [key, raw] of Object.entries(evidence ?? {})) {
    if (raw === null || raw === undefined || raw === '') continue
    if (Array.isArray(raw)) {
      if (raw.length === 0) continue
      lines.push({ key, value: raw.map((x) => (typeof x === 'object' ? JSON.stringify(x) : String(x))).join(', ') })
    } else if (typeof raw === 'object') {
      lines.push({ key, value: JSON.stringify(raw) })
    } else {
      lines.push({ key, value: String(raw) })
    }
  }
  return lines
}

/** Vrai quand l'action peut s'appliquer sans autre information. */
export function canApply(f: ReconcileFinding): boolean {
  return !f.resolved_at && !!f.proposed_action
}
