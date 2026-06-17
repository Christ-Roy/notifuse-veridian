/**
 * Veridian — presets de politique d'envoi cold (ticket 2026-06-16-config-presets-politique-envoi).
 *
 * Un preset = un ENSEMBLE COHÉRENT de valeurs pré-remplies que l'admin applique
 * d'un clic puis ajuste avant de sauver. Il ne crée AUCUNE entité persistée (pas
 * de table preset) : c'est juste de la donnée front que l'UI pousse dans les
 * champs du formulaire workspace (`form.setFieldsValue`) + la SendingWindowCard.
 *
 * Toutes les briques composées ici existent déjà en backend et sont
 * configurables indépendamment (rates `veridian_provider_throttle.go`, caps
 * `veridian_daily_cap.go` / `veridian_per_sender_cap.go`, fenêtre
 * `veridian_sending_window_gate.go`, jitter `veridian_jitter.go`, anti-hash
 * `veridian_content_hash.go`, exclusion `veridian_excluded_classes.go`). Le preset
 * encode le SAVOIR-FAIRE délivrabilité (quels débits pour une IP fraîche vs
 * mature) dans le produit, au lieu de le laisser dans un doc.
 *
 * Le round-robin multi-senders n'a AUCUNE valeur à poser : il s'active tout seul
 * dès qu'une infra a ≥ 2 senders ET qu'on est en contexte cold (poser n'importe
 * laquelle de ces clés bascule le contexte cold côté backend).
 */
import {
  VERIDIAN_PROVIDER_CLASSES,
  VERIDIAN_WARMUP_PRESET,
  VeridianProviderClass,
  VeridianSendingWindow
} from '../api/workspace'

// Shape d'un preset : tous les leviers de politique cold composables. Tous
// optionnels — un preset peut ne pas toucher un levier (ex. la croisière ne pose
// pas d'exclusion). L'UI n'applique QUE les leviers présents.
export interface VeridianSendingPolicyPreset {
  // Identifiant stable du preset (clé technique, jamais affichée brute).
  id: 'warmup' | 'cruise' | 'microsoft_prudence'
  // Débit /minute par classe (SPEED). Fractions OK (0.5 = 1 mail / 2 min).
  rates?: Record<VeridianProviderClass, number>
  // Plafond /jour par classe (VOLUME). Entier.
  classDailyCap?: Record<VeridianProviderClass, number>
  // Plafond /jour vers une même adresse (anti-harcèlement).
  perRecipientDailyCap?: number
  // Plafond /jour par adresse émettrice (warmup IP).
  perSenderDailyCap?: number
  // Fenêtre d'envoi (horaires ouvrables).
  sendingWindow?: VeridianSendingWindow
  // Jitter temporel ±pct du throttle minute (0–0.9).
  jitterPct?: number
  // Anti-hash identique (true = forcé ON).
  antiHashEnabled?: boolean
  // Fenêtre glissante anti-hash, en heures.
  antiHashWindowHours?: number
  // Classes de provider à NE PAS contacter (le worker les skippe proprement).
  excludedClasses?: VeridianProviderClass[]
}

// Helper : pose la même valeur sur toutes les classes connues.
const allClasses = (value: number): Record<VeridianProviderClass, number> =>
  VERIDIAN_PROVIDER_CLASSES.reduce(
    (acc, cls) => {
      acc[cls] = value
      return acc
    },
    {} as Record<VeridianProviderClass, number>
  )

// Comme allClasses mais avec un override par classe (le reste garde `base`).
const allClassesWith = (
  base: number,
  overrides: Partial<Record<VeridianProviderClass, number>>
): Record<VeridianProviderClass, number> => {
  const out = allClasses(base)
  for (const c of Object.keys(overrides) as VeridianProviderClass[]) {
    const v = overrides[c]
    if (typeof v === 'number') out[c] = v
  }
  return out
}

// ── 1. Warm-up prudent ────────────────────────────────────────────────────────
// Démarrage d'une IP/domaine neuf : volume minuscule, fenêtre serrée, dispersion
// maximale. Réutilise le set existant VERIDIAN_WARMUP_PRESET (déjà câblé sur la
// SendingWindowCard via le nonce) + durcit jitter/anti-hash à leurs défauts cold.
// Microsoft EXCLU : c'est la réputation la plus dure à monter, on ne la touche
// pas tant que l'IP n'est pas chaude.
export const WARMUP_PRESET: VeridianSendingPolicyPreset = {
  id: 'warmup',
  rates: VERIDIAN_WARMUP_PRESET.veridian_provider_class_rates, // 0.5/min partout
  classDailyCap: VERIDIAN_WARMUP_PRESET.veridian_provider_class_daily_cap, // 1/jour partout
  perRecipientDailyCap: VERIDIAN_WARMUP_PRESET.veridian_per_recipient_daily_cap, // 1
  perSenderDailyCap: VERIDIAN_WARMUP_PRESET.veridian_per_sender_daily_cap, // 20
  sendingWindow: VERIDIAN_WARMUP_PRESET.veridian_sending_window, // lun-ven 9-18 Europe/Paris
  jitterPct: 0.3, // défaut cold : ±30 %
  antiHashEnabled: true,
  antiHashWindowHours: 72,
  excludedClasses: ['microsoft']
}

// ── 2. Croisière ───────────────────────────────────────────────────────────────
// IP/domaine matures : débit confortable, plafonds larges, fenêtre étendue,
// Microsoft réactivé à débit modéré. Toujours jitter + anti-hash ON (hygiène cold
// permanente). Pas d'exclusion.
export const CRUISE_PRESET: VeridianSendingPolicyPreset = {
  id: 'cruise',
  // Débit modéré, Microsoft un cran plus prudent (réputation plus fragile).
  rates: allClassesWith(5, { google: 4, microsoft: 2 }),
  // Plafonds journaliers confortables ; Microsoft tenu un peu plus bas.
  classDailyCap: allClassesWith(500, { google: 400, microsoft: 200 }),
  perRecipientDailyCap: 1,
  // Volume sain par boîte une fois l'IP chaude.
  perSenderDailyCap: 200,
  // Plage large ouvrable.
  sendingWindow: {
    days: [1, 2, 3, 4, 5],
    start_hour: 8,
    end_hour: 20,
    timezone: 'Europe/Paris'
  },
  jitterPct: 0.2, // ±20 % en croisière (un peu moins erratique qu'en warmup)
  antiHashEnabled: true,
  antiHashWindowHours: 72,
  excludedClasses: []
}

// ── 3. Prudence Microsoft ───────────────────────────────────────────────────────
// Identique à la croisière, mais Microsoft EXCLU. À utiliser quand la réputation
// Microsoft est dégradée (ou jamais montée) et qu'on veut continuer à servir tout
// le reste sans toucher Outlook/M365.
export const MICROSOFT_PRUDENCE_PRESET: VeridianSendingPolicyPreset = {
  ...CRUISE_PRESET,
  id: 'microsoft_prudence',
  excludedClasses: ['microsoft']
}

// Liste ordonnée des presets disponibles (du plus prudent au plus large).
export const VERIDIAN_SENDING_POLICY_PRESETS: VeridianSendingPolicyPreset[] = [
  WARMUP_PRESET,
  CRUISE_PRESET,
  MICROSOFT_PRUDENCE_PRESET
]

export function veridianPresetById(
  id: VeridianSendingPolicyPreset['id']
): VeridianSendingPolicyPreset | undefined {
  return VERIDIAN_SENDING_POLICY_PRESETS.find((p) => p.id === id)
}
