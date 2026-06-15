import { api } from './client'
import type { VeridianProviderClass } from './workspace'

// Veridian fork — client du linter de délivrabilité (spam score) sur un template
// cold RENDU. Endpoint POST /api/veridian/templates.deliverabilityScore (auth JWT
// console + permission templates:read). Le scoring est instantané (package Go pur,
// zéro I/O). Source de vérité backend :
//   internal/http/veridian_deliverability_score_handler.go
//   pkg/veridian_deliverability/veridian_deliverability.go
//
// On envoie le RENDU FINAL (HTML compilé + sujet rendu, Liquid/spintax résolus) :
// le linter détecte justement ce qui n'a PAS été résolu (variables qui fuitent).

// Mode du linter. "strict" = gros providers (Google/Microsoft/Apple) — liens,
// tracking et HTML fortement pénalisés. "lenient" = petits providers (FAI FR,
// corporate, OVH…) — HTML léger toléré. "default" = profil neutre. Si on passe
// une classe de provider, le mode est déduit automatiquement (sauf override).
export type VeridianDeliverabilityMode = 'strict' | 'lenient' | 'default'

export interface VeridianDeliverabilityScoreRequest {
  workspace_id: string
  // Sujet RENDU (Liquid résolu). Optionnel mais recommandé (règles de sujet).
  subject?: string
  // Corps RENDU : HTML compilé (is_html=true) ou texte brut.
  body?: string
  is_html?: boolean
  // Domaine du From (ex "agences-veridian.fr") : détecte un tracking sur un
  // domaine ≠ envoi (signal anti-spam). Optionnel.
  from_domain?: string
  // Classe de provider destinataire (déduit le mode strict/lenient). Optionnel.
  provider_class?: VeridianProviderClass | ''
  // Override explicite du mode (prime sur provider_class). Optionnel.
  mode?: VeridianDeliverabilityMode
}

// Une règle déclenchée : nom stable (façon SpamAssassin), poids appliqué
// (positif = pénalité, négatif = bonus) et message d'aide expliquant quoi corriger.
export interface VeridianDeliverabilityRule {
  name: string
  weight: number
  message: string
}

// Verdict du linter : score borné 0-10 (>5 = risque), mode effectif, règles
// déclenchées (poids décroissant) et un résumé humain.
export interface VeridianDeliverabilityResult {
  score: number
  is_risky: boolean
  mode: VeridianDeliverabilityMode
  rules: VeridianDeliverabilityRule[]
  summary: string
}

export const deliverabilityApi = {
  // Score un template rendu. POST (corps JSON) — le handler route aussi GET pour
  // le debug, mais l'UI envoie toujours en POST (corps HTML potentiellement long).
  score: (req: VeridianDeliverabilityScoreRequest): Promise<VeridianDeliverabilityResult> =>
    api.post<VeridianDeliverabilityResult>('/api/veridian/templates.deliverabilityScore', req)
}
