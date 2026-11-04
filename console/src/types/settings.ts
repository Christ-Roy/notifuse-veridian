export interface SystemSettingsData {
  root_email: string
  api_endpoint: string
  smtp_host: string
  smtp_port: number
  smtp_username: string
  smtp_password: string
  smtp_from_email: string
  smtp_from_name: string
  smtp_use_tls: boolean
  smtp_ehlo_hostname: string
  telemetry_enabled: boolean
  check_for_updates: boolean
  smtp_bridge_enabled: boolean
  smtp_bridge_domain: string
  smtp_bridge_port: number
  smtp_bridge_tls_cert_base64: string
  smtp_bridge_tls_key_base64: string
  // Relais SMTP de soumission et certificat géré (fiche 71). Le jeton est écrit seul : jamais renvoyé.
  veridian_smtp_relay_enabled: boolean
  veridian_relay_cert_managed: boolean
  veridian_relay_acme_email: string
  veridian_relay_acme_directory: 'production' | 'test'
  veridian_relay_cf_token?: string
  // Lecture seule (posés par le serveur, ignorés à l'écriture).
  has_veridian_relay_cf_token: boolean
  veridian_relay_cert_not_after: string
  veridian_relay_cert_serial: string
  veridian_relay_cert_last_renewed: string
  veridian_relay_cert_last_error: string
}

export interface SystemSettingsResponse {
  settings: SystemSettingsData
  env_overrides: Record<string, boolean>
}
