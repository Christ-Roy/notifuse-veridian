import type { SystemSettingsData } from '../../types/settings'

// Champs d'état posés par le serveur : lecture seule, jamais renvoyés en écriture.
const READ_ONLY_FIELDS = [
  'has_veridian_relay_cf_token',
  'veridian_relay_cert_not_after',
  'veridian_relay_cert_serial',
  'veridian_relay_cert_last_renewed',
  'veridian_relay_cert_last_error'
] as const

// toSettingsUpdate prépare le corps de settings.update. Les valeurs courantes du formulaire partent
// telles quelles (sémantique « absent = inchangé » côté serveur). Le jeton DNS n'part que s'il est saisi :
// un champ vide conserve le jeton stocké.
export function toSettingsUpdate(values: SystemSettingsData): SystemSettingsData {
  const body: Partial<SystemSettingsData> = { ...values }
  for (const field of READ_ONLY_FIELDS) {
    delete body[field]
  }
  if (!body.veridian_relay_cf_token) {
    delete body.veridian_relay_cf_token
  }
  return body as SystemSettingsData
}

// relayCertNeedsEmail : une adresse de contact ACME est exigée dès que le certificat est géré.
export function relayCertNeedsEmail(managed: boolean | undefined): boolean {
  return managed === true
}
