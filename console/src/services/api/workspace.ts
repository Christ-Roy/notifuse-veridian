import { api } from './client'
import type { EmailBlock } from '../../components/email_builder/types'

// Template Block type
export interface TemplateBlock {
  id: string
  name: string
  block: EmailBlock
  created: string
  updated: string
}

// SEO Settings type (matches blog.go's SEOSettings)
export interface SEOSettings {
  meta_title?: string
  meta_description?: string
  og_title?: string
  og_description?: string
  og_image?: string
  canonical_url?: string
  keywords?: string[]
  meta_robots?: string
}

// Blog Settings type (styling + SEO for blog)
export interface BlogSettings {
  title?: string
  logo_url?: string
  icon_url?: string
  styling?: Record<string, unknown> // EditorStyleConfig - stored as JSON
  seo?: SEOSettings
  home_page_size?: number
  category_page_size?: number
  feed_summary_only?: boolean
  feed_max_items?: number
}

export interface WorkspaceSettings {
  website_url?: string
  logo_url?: string | null
  cover_url?: string | null
  timezone: string
  file_manager?: FileManagerSettings
  transactional_email_provider_id?: string
  marketing_email_provider_id?: string
  email_tracking_enabled: boolean
  template_blocks?: TemplateBlock[]
  custom_endpoint_url?: string
  custom_field_labels?: Record<string, string>
  blog_enabled?: boolean
  blog_settings?: BlogSettings
  default_language: string
  languages: string[]
  // Veridian fork — cold outreach (tunnel de vente)
  // Débits par classe de provider destinataire (emails/minute, fractions OK).
  veridian_provider_class_rates?: Record<VeridianProviderClass, number>
  // Politique du pixel d'ouverture par classe (true = pixel ON). Override du
  // défaut tunnel (ON freemail_fr/yahoo_aol/corporate, OFF google/microsoft).
  veridian_open_pixel_by_class?: Record<VeridianProviderClass, boolean>
  // Plafond JOURNALIER d'envois par classe (emails/jour MAX). Distinct du débit
  // minute ci-dessus : le rate borne la VITESSE, le cap borne le VOLUME du jour.
  // Source de vérité backend : COUNT message_history depuis minuit. Vide/omis =
  // pas de plafond journalier (le débit minute reste appliqué).
  veridian_provider_class_daily_cap?: Record<VeridianProviderClass, number>
  // Plafond JOURNALIER d'envois vers une MÊME adresse (anti-harcèlement).
  // Entier global, non keyé par classe. 0 / omis = illimité.
  veridian_per_recipient_daily_cap?: number
  // Plafond JOURNALIER par ADRESSE ÉMETTRICE (warmup IP). Dimension ÉMETTRICE
  // (≠ caps destinataire ci-dessus) : max N envois/jour par boîte d'envoi. En
  // warmup, chaque boîte monte son propre volume. Source de vérité backend :
  // COUNT message_history par sender depuis minuit (V53). 0 / omis = pas de plafond.
  veridian_per_sender_daily_cap?: number
  // Fenêtre d'envoi (horaires ouvrables) cold outbound. Hors fenêtre, le worker
  // re-planifie l'envoi à la prochaine ouverture. Vide/omis = envoi 24/7
  // (non-régression). Source de vérité backend :
  // internal/domain/veridian_sending_window.go. Cascade : broadcast → infra →
  // workspace (ce niveau) → rien.
  veridian_sending_window?: VeridianSendingWindow
  // Classes de providers destinataires à NE PAS contacter (cold outbound). Levier
  // DÉDIÉ, distinct des rates/caps (un rate/cap à 0 = "pleine vitesse/illimité",
  // PAS une exclusion). Les contacts d'une classe exclue sont skippés proprement
  // (pas de SMTP, pas de bounce) ; le reste du broadcast part. Cas d'usage : ne
  // pas taper microsoft sur une IP en warm-up. Cascade : broadcast → infra →
  // workspace (ce niveau). Vide/omis = aucune exclusion. Source de vérité backend :
  // internal/domain/veridian_excluded_classes.go.
  veridian_excluded_provider_classes?: VeridianProviderClass[]
  // Jitter temporel (±pct) du throttle minute par classe (cold outbound). Disperse
  // le délai de re-planification autour de sa valeur nominale pour casser le rythme
  // métronomique (tell de machine cold). ⚠️ POINTEUR côté Go (*float64), sémantique
  // TRI-ÉTAT : `undefined`/absent = défaut cold ±0.30 ; `0` = jitter DÉSACTIVÉ
  // explicite (opt-out, NE PAS omettre un 0 voulu). Clamp backend [0, 0.9]. Source
  // de vérité : internal/service/queue/veridian_jitter.go. Cascade : broadcast →
  // infra → workspace (ce niveau).
  veridian_jitter_pct?: number
  // Anti-hash identique (cold outbound) : empêche deux mails au RENDU identique
  // (sujet+corps normalisés) de partir vers la même classe dans une fenêtre
  // glissante. ⚠️ POINTEUR côté Go (*bool), sémantique TRI-ÉTAT : `undefined`/absent
  // = défaut cold ON ; `false` = désactivé explicite (NE PAS omettre un false voulu) ;
  // `true` = forcé ON. Source de vérité : internal/domain/veridian_content_hash.go.
  veridian_anti_hash_enabled?: boolean
  // Fenêtre glissante (heures) de l'anti-hash. <=0 ou omis = défaut 72h. Entier.
  veridian_anti_hash_window_hours?: number
}

// Veridian fork — fenêtre d'envoi hebdomadaire (cold outbound). Miroir EXACT du
// shape JSON Go (internal/domain/veridian_sending_window.go) :
//   days        : jours autorisés, 0=dimanche … 6=samedi (time.Weekday). Vide =
//                 tous les jours.
//   start_hour  : heure d'ouverture INCLUSE (0-23), start_minute (0-59).
//   end_hour    : heure de fermeture EXCLUSIVE (0-24), end_minute (0-59).
//   timezone    : nom IANA (ex "Europe/Paris"). Vide = fallback timezone workspace.
// Une plage où start == end (ou end < start) est traitée comme "pas de fenêtre"
// (envoi 24/7) côté backend — on ne fige jamais le pipeline sur une config absurde.
export interface VeridianSendingWindow {
  days?: number[]
  start_hour: number
  start_minute?: number
  end_hour: number
  end_minute?: number
  timezone?: string
}

// Veridian fork — classes canoniques de provider destinataire (cf. backend
// internal/domain/veridian_provider_class.go). Source de vérité = le Go ;
// dupliquée ici pour le typage UI de la config cold outreach.
//
// Les 5 premières sont historiques (classification par suffixe). Les suivantes
// (Lot 4, 2026-06-14) sont issues de la classification par MX réel : un domaine
// custom hébergé Google/M365/OVH/… est désormais classé selon son MX, pas jeté
// en `corporate`. Garder STRICTEMENT aligné sur VeridianAllProviderClasses() Go.
export type VeridianProviderClass =
  | 'google'
  | 'microsoft'
  | 'yahoo_aol'
  | 'freemail_fr'
  | 'corporate'
  | 'ovh'
  | 'ionos'
  | 'apple_icloud'
  | 'security_gateway'
  | 'other_hoster'
  | 'corporate_selfhost'

export const VERIDIAN_PROVIDER_CLASSES: VeridianProviderClass[] = [
  'google',
  'microsoft',
  'yahoo_aol',
  'freemail_fr',
  'corporate',
  'ovh',
  'ionos',
  'apple_icloud',
  'security_gateway',
  'other_hoster',
  'corporate_selfhost'
]

// Défaut tunnel du pixel d'ouverture (miroir de veridianDefaultOpenPixelByClass
// côté Go) : ON petits providers, OFF gros / sensibles. Sert de placeholder
// visuel quand aucune config explicite n'existe.
export const VERIDIAN_DEFAULT_OPEN_PIXEL: Record<VeridianProviderClass, boolean> = {
  google: false,
  microsoft: false,
  yahoo_aol: true,
  freemail_fr: true,
  corporate: true,
  ovh: true,
  ionos: true,
  apple_icloud: false,
  security_gateway: false,
  other_hoster: true,
  corporate_selfhost: true
}

// Veridian fork — PRESET « Mode warmup » (cold outbound, ticket 2026-06-16). Set
// cohérent de valeurs qui constitue le démarrage prudent d'une nouvelle IP/domaine :
// 1 envoi/jour vers chaque classe de provider destinataire + 1/jour/adresse
// (anti-harcèlement) + débit lent dans la journée + fenêtre ouvrable lun-ven 9-18
// Europe/Paris. Le round-robin entre adresses d'envoi n'a AUCUNE valeur à poser :
// il s'active tout seul dès que l'infra a ≥ 2 senders ET qu'on est en contexte cold
// (poser n'importe laquelle de ces clés au niveau workspace bascule le contexte
// cold). Source de vérité des gates backend : veridian_daily_cap.go /
// veridian_provider_throttle.go / veridian_sending_window_gate.go. V1 = cap STATIQUE
// bas ; la rampe PROGRESSIVE (1→2→5→10/jour auto) est un ticket séparé
// (2026-06-16-warmup-progressif-rampe-auto.md), NON implémentée ici.
//
// 🔴 Doctrine warm-up 2026-06-18 (§7.3bis tunnel de vente) : la SEULE limite qui
// compte = « 1 infra émettrice (IP + domaine) → 1 classe destinataire = N/jour ».
// Le cap-classe destinataire (veridian_provider_class_daily_cap) est désormais keyé
// PAR INFRA ÉMETTRICE côté backend (couple domaine-émetteur × classe), donc posé à 1
// il plafonne CHAQUE domaine d'envoi à 1/jour/classe. Le cap PAR SENDER individuel
// (veridian_per_sender_daily_cap) est VOLONTAIREMENT RETIRÉ de ce preset : c'est un
// faux modèle réputationnel (les N adresses d'un même domaine partagent l'IP/
// réputation, le bon grain est le domaine, pas l'adresse). Le champ reste supporté
// par le backend (réglable à la main si besoin), il n'est juste plus posé ici.
export interface VeridianWarmupPreset {
  veridian_provider_class_daily_cap: Record<VeridianProviderClass, number>
  veridian_per_recipient_daily_cap: number
  veridian_provider_class_rates: Record<VeridianProviderClass, number>
  veridian_sending_window: VeridianSendingWindow
}

const veridianAllClassesValue = (value: number): Record<VeridianProviderClass, number> =>
  VERIDIAN_PROVIDER_CLASSES.reduce(
    (acc, cls) => {
      acc[cls] = value
      return acc
    },
    {} as Record<VeridianProviderClass, number>
  )

export const VERIDIAN_WARMUP_PRESET: VeridianWarmupPreset = {
  // 1 envoi / jour / classe de provider destinataire (toutes les classes connues).
  // Keyé PAR INFRA ÉMETTRICE côté backend (couple domaine-émetteur × classe) →
  // chaque domaine d'envoi est plafonné à 1/jour/classe indépendamment des autres.
  veridian_provider_class_daily_cap: veridianAllClassesValue(1),
  // Jamais 2 mails/jour à la même adresse.
  veridian_per_recipient_daily_cap: 1,
  // veridian_per_sender_daily_cap VOLONTAIREMENT ABSENT : faux modèle réputationnel
  // (cf. note doctrine §7.3bis ci-dessus). Le grain de réputation = le domaine
  // d'envoi (déjà couvert par le cap-classe par infra), pas l'adresse individuelle.
  // Rythme lent dans la journée : 0.5/min = au plus 1 mail / 2 min par classe
  // (étale au lieu d'un burst). Le cap/jour=1 domine déjà ; le rate humanise.
  veridian_provider_class_rates: veridianAllClassesValue(0.5),
  // Heures ouvrables (anti-spam + crédible humain). Jours 1-5 = lun-ven (0=dim).
  veridian_sending_window: {
    days: [1, 2, 3, 4, 5],
    start_hour: 9,
    end_hour: 18,
    timezone: 'Europe/Paris'
  }
}

export interface FileManagerSettings {
  provider?: string
  endpoint: string
  access_key: string
  bucket: string
  region?: string
  secret_key?: string
  encrypted_secret_key?: string
  cdn_endpoint?: string
  force_path_style?: boolean
}

export type EmailProviderKind = 'smtp' | 'ses' | 'sparkpost' | 'postmark' | 'mailgun' | 'mailjet' | 'sendgrid'

export interface Sender {
  id: string
  email: string
  name: string
  is_default: boolean
}

export interface EmailProvider {
  kind: EmailProviderKind
  ses?: AmazonSES
  smtp?: SMTPSettings
  sparkpost?: SparkPostSettings
  postmark?: PostmarkSettings
  mailgun?: MailgunSettings
  mailjet?: MailjetSettings
  sendgrid?: SendGridSettings
  senders: Sender[]
  rate_limit_per_minute: number

  // Veridian fork — config cold outbound PAR INFRA d'envoi (R2 + Lot 5/8). Une
  // infra (= cette intégration EmailProvider, son host/IP/relai SMTP + senders)
  // peut porter ses propres débits/plafonds (en warm-up) et son custom tracking
  // domain aligné au domaine d'envoi. Source de vérité backend :
  // internal/domain/email_provider.go. Persisté comme JSON blob (pas d'allowlist
  // champ-par-champ — passe automatiquement via updateIntegration).
  veridian_provider_class_rates?: Record<VeridianProviderClass, number>
  veridian_provider_class_daily_cap?: Record<VeridianProviderClass, number>
  veridian_per_recipient_daily_cap?: number
  // Plafond JOURNALIER par ADRESSE ÉMETTRICE de cette infra (warmup IP). Cf.
  // internal/domain/email_provider.go (VeridianPerSenderDailyCap).
  veridian_per_sender_daily_cap?: number
  // Custom tracking domain aligné au domaine d'envoi (ex track.agences-veridian.fr).
  // Domaine nu OU URL complète. Vide = fallback workspace/global. Cf.
  // internal/domain/veridian_tracking_domain.go.
  veridian_tracking_domain?: string
  // Classes de providers destinataires à NE PAS contacter DEPUIS CETTE INFRA (cold
  // outbound). Niveau intermédiaire de la cascade (broadcast → INFRA → workspace).
  // Typiquement : exclure microsoft sur une IP fraîche le temps du warm-up. Vide/
  // omis = aucune exclusion sur l'infra (héritage workspace). Cf.
  // internal/domain/veridian_excluded_classes.go.
  veridian_excluded_provider_classes?: VeridianProviderClass[]
  // Fenêtre d'envoi (horaires ouvrables) PAR INFRA. Niveau intermédiaire de la
  // cascade (broadcast → INFRA → workspace) : une IP fraîche peut être cantonnée
  // 10h-16h pendant que le reste du workspace envoie plus large. Vide/omis =
  // héritage workspace. Cf. internal/domain/email_provider.go (VeridianSendingWindow).
  veridian_sending_window?: VeridianSendingWindow
  // Jitter temporel PAR INFRA (±pct). Pointeur côté Go (*float64), TRI-ÉTAT :
  // `undefined` = héritage (workspace puis défaut cold) ; `0` = désactivé explicite ;
  // valeur > 0 = override infra. Cf. internal/domain/email_provider.go (VeridianJitterPct).
  veridian_jitter_pct?: number
  // Anti-hash PAR INFRA. Pointeur côté Go (*bool), TRI-ÉTAT : `undefined` = héritage ;
  // `false` = désactivé explicite ; `true` = forcé ON. Cf. email_provider.go
  // (VeridianAntiHashEnabled).
  veridian_anti_hash_enabled?: boolean
  // Fenêtre anti-hash (heures) PAR INFRA. <=0/omis = héritage. Cf. email_provider.go.
  veridian_anti_hash_window_hours?: number
  // Politique du pixel d'ouverture (email.opened) PAR CLASSE, AU NIVEAU INFRA.
  // Niveau intermédiaire de la cascade (broadcast → INFRA → workspace → défaut
  // tunnel) : une IP fraîche peut couper le pixel partout le temps du warm-up,
  // indépendamment du workspace. Map {classe: bool}. Vide/omis = héritage
  // workspace (puis défaut tunnel). Cf. internal/domain/email_provider.go
  // (VeridianOpenPixelByClass) + internal/domain/veridian_open_pixel.go.
  veridian_open_pixel_by_class?: Record<VeridianProviderClass, boolean>
}

// Veridian fork — config d'une boîte IMAP pollée par Notifuse (réception :
// bounces NDR Postfix + réponses prospects cold). C'est la brique self-service
// qui débloque bounce-loop (Lot 2) + stop-on-reply (Lot 3) sans script externe.
// Source de vérité backend : internal/domain/veridian_imap_integration.go.
// Le password n'est jamais renvoyé en clair par l'API (encrypted_password seul
// persiste) ; l'UI le laisse vide à l'édition pour ne pas le changer.
export interface IMAPSettings {
  host: string
  port: number
  username: string
  password?: string
  encrypted_password?: string
  use_tls: boolean
  folder?: string
  polling_interval_seconds?: number
}

export interface AmazonSES {
  region: string
  access_key: string
  secret_key?: string
  encrypted_secret_key?: string
}

export type SMTPAuthType = 'basic' | 'oauth2'
export type SMTPOAuth2Provider = 'microsoft' | 'google'

export interface SMTPSettings {
  host: string
  port: number
  username: string
  password?: string
  encrypted_password?: string
  encrypted_username?: string
  use_tls: boolean
  ehlo_hostname?: string

  // Authentication type: 'basic' (default) or 'oauth2'
  auth_type?: SMTPAuthType

  // OAuth2 fields
  oauth2_provider?: SMTPOAuth2Provider // 'microsoft' or 'google'
  oauth2_tenant_id?: string // Microsoft only
  oauth2_client_id?: string
  oauth2_client_secret?: string
  encrypted_oauth2_client_secret?: string
  oauth2_refresh_token?: string // Google only
  encrypted_oauth2_refresh_token?: string // Google only
}

export interface SparkPostSettings {
  api_key?: string
  encrypted_api_key?: string
  sandbox_mode: boolean
  endpoint: string
}

export interface PostmarkSettings {
  server_token?: string
  encrypted_server_token?: string
  message_stream?: string
}

export interface MailgunSettings {
  api_key?: string
  encrypted_api_key?: string
  domain: string
  region?: 'US' | 'EU'
}

export interface MailjetSettings {
  api_key?: string
  encrypted_api_key?: string
  secret_key?: string
  encrypted_secret_key?: string
  sandbox_mode: boolean
}

export interface SendGridSettings {
  api_key?: string
  encrypted_api_key?: string
}

export type IntegrationType =
  | 'email'
  | 'sms'
  | 'whatsapp'
  | 'supabase'
  | 'llm'
  | 'firecrawl'
  // Veridian fork — boîte IMAP pollée (réception bounces/réponses cold, Lot 8).
  | 'imap'

// LLM Provider types
export type LLMProviderKind = 'anthropic' | 'openai'

export interface AnthropicSettings {
  api_key?: string
  encrypted_api_key?: string
  model: string
}

export interface OpenAISettings {
  api_key?: string
  encrypted_api_key?: string
  model: string
  base_url?: string
}

export interface LLMProvider {
  kind: LLMProviderKind
  anthropic?: AnthropicSettings
  openai?: OpenAISettings
}

// Firecrawl settings for web scraping and search
export interface FirecrawlSettings {
  api_key?: string
  encrypted_api_key?: string
  base_url?: string
}

export interface SupabaseAuthEmailHookSettings {
  signature_key?: string
  encrypted_signature_key?: string
}

export interface SupabaseUserCreatedHookSettings {
  signature_key?: string
  encrypted_signature_key?: string
  add_user_to_lists?: string[] // Array of list IDs
  custom_json_field?: string
  reject_disposable_email?: boolean // Reject user creation if email is disposable
}

export interface SupabaseIntegrationSettings {
  auth_email_hook: SupabaseAuthEmailHookSettings
  before_user_created_hook: SupabaseUserCreatedHookSettings
}

export interface Integration {
  id: string
  name: string
  type: IntegrationType
  email_provider?: EmailProvider
  supabase_settings?: SupabaseIntegrationSettings
  llm_provider?: LLMProvider
  firecrawl_settings?: FirecrawlSettings
  // Veridian fork — présent ssi type === 'imap' (Lot 8).
  imap_settings?: IMAPSettings
  created_at: string
  updated_at: string
}

export interface CreateWorkspaceRequest {
  id: string
  name: string
  settings: WorkspaceSettings
}

export interface Workspace {
  id: string
  name: string
  settings: WorkspaceSettings
  integrations?: Integration[]
  created_at: string
  updated_at: string
}

export interface CreateWorkspaceResponse {
  workspace: Workspace
}

export interface ListWorkspacesResponse {
  workspaces: Workspace[]
}

export interface GetWorkspaceResponse {
  workspace: Workspace
}

export interface UpdateWorkspaceRequest {
  id: string
  name?: string
  settings?: Partial<WorkspaceSettings>
}

export interface UpdateWorkspaceResponse {
  workspace: Workspace
}

export interface CreateAPIKeyRequest {
  workspace_id: string
  email_prefix: string
}

export interface CreateAPIKeyResponse {
  token: string
  email: string
}

export interface RemoveMemberRequest {
  workspace_id: string
  user_id: string
}

export interface RemoveMemberResponse {
  status: string
  message: string
}

export interface DeleteWorkspaceRequest {
  id: string
}

export interface DeleteWorkspaceResponse {
  status: string
}

// Integration related types
export interface CreateIntegrationRequest {
  workspace_id: string
  name: string
  type: IntegrationType
  provider?: EmailProvider
  supabase_settings?: SupabaseIntegrationSettings
  llm_provider?: LLMProvider
  firecrawl_settings?: FirecrawlSettings
  // Veridian fork — config IMAP self-service (Lot 8), présent ssi type === 'imap'.
  imap_settings?: IMAPSettings
}

export interface UpdateIntegrationRequest {
  workspace_id: string
  integration_id: string
  name: string
  provider?: EmailProvider
  supabase_settings?: SupabaseIntegrationSettings
  llm_provider?: LLMProvider
  firecrawl_settings?: FirecrawlSettings
  // Veridian fork — config IMAP self-service (Lot 8). Password vide à l'édition =
  // ne change pas le mot de passe (le backend préserve encrypted_password).
  imap_settings?: IMAPSettings
}

export interface DeleteIntegrationRequest {
  workspace_id: string
  integration_id: string
}

// Integration responses
export interface CreateIntegrationResponse {
  integration_id: string
}

export interface UpdateIntegrationResponse {
  status: string
}

export interface DeleteIntegrationResponse {
  status: string
}

// Workspace Member types
export interface WorkspaceMember {
  user_id: string
  workspace_id: string
  role: string
  email: string
  type: 'user' | 'api_key'
  created_at: string
  updated_at: string
  invitation_expires_at?: string
  invitation_id?: string
  permissions: UserPermissions
}

export interface GetWorkspaceMembersResponse {
  members: WorkspaceMember[]
}

// Workspace Member Invitation types
export interface InviteMemberRequest {
  workspace_id: string
  email: string
  permissions: UserPermissions
}

export interface InviteMemberResponse {
  status: string
  message: string
}

// === Veridian patch — Hub invitation flow (2026-05-23) ===
// Request/response du nouvel endpoint POST /api/veridian/workspaces.inviteMember
// qui delegue l'invitation cross-app au Hub Veridian (au lieu de creer une
// invitation locale notifuse_invitations).
// Voir todo/2026-05-20-hub-invitation-flow-multi-membre.md.
export interface VeridianInviteMemberRequest {
  workspace_id: string
  email: string
  role?: 'owner' | 'admin' | 'member'
  message?: string
}

export interface VeridianInviteMemberResponse {
  status: string
  message: string
  hub_invitation_id: string
  magic_link_url: string
  expires_at: string
  reused: boolean
}

// Permission types
export interface ResourcePermissions {
  read: boolean
  write: boolean
}

export interface UserPermissions {
  contacts: ResourcePermissions
  lists: ResourcePermissions
  templates: ResourcePermissions
  broadcasts: ResourcePermissions
  transactional: ResourcePermissions
  workspace: ResourcePermissions
  message_history: ResourcePermissions
  blog: ResourcePermissions
  automations: ResourcePermissions
  llm: ResourcePermissions
}

// Set User Permissions types
export interface SetUserPermissionsRequest {
  workspace_id: string
  user_id: string
  permissions: UserPermissions
}

export interface SetUserPermissionsResponse {
  status: string
  message: string
}

// Invitation types
export interface WorkspaceInvitation {
  id: string
  workspace_id: string
  inviter_id: string
  email: string
  expires_at: string
  created_at: string
  updated_at: string
}

export interface User {
  id: string
  email: string
  name: string
  type: string
  created_at: string
  updated_at: string
}

export interface VerifyInvitationTokenResponse {
  status: string
  invitation: WorkspaceInvitation
  workspace: Workspace
  valid: boolean
}

export interface AcceptInvitationResponse {
  status: string
  message: string
  workspace_id: string
  email: string
  token: string
  user: User
  expires_at: string
}

export interface DeleteInvitationRequest {
  invitation_id: string
}

export interface DeleteInvitationResponse {
  status: string
  message: string
}

interface DetectFaviconResponse {
  iconUrl: string
  coverUrl?: string
}

export const workspaceService = {
  list: () => api.get<ListWorkspacesResponse>('/api/workspaces.list'),

  get: (id: string) => api.get<GetWorkspaceResponse>(`/api/workspaces.get?id=${id}`),

  create: (data: CreateWorkspaceRequest) =>
    api.post<CreateWorkspaceResponse>('/api/workspaces.create', data),

  update: (data: UpdateWorkspaceRequest) =>
    api.post<UpdateWorkspaceResponse>('/api/workspaces.update', data),

  delete: (data: DeleteWorkspaceRequest) =>
    api.post<DeleteWorkspaceResponse>('/api/workspaces.delete', data),

  detectFavicon: (url: string) => api.post<DetectFaviconResponse>('/api/detect-favicon', { url }),

  getMembers: (id: string) =>
    api.get<GetWorkspaceMembersResponse>(`/api/workspaces.members?id=${id}`),

  inviteMember: (data: InviteMemberRequest) =>
    api.post<InviteMemberResponse>('/api/workspaces.inviteMember', data),

  // === Veridian patch === Invitation via Hub (mode managed only).
  // Le front decide entre cette methode et inviteMember() en fonction de
  // /api/veridian/mode (cf. WorkspaceMembers.tsx).
  inviteMemberViaHub: (data: VeridianInviteMemberRequest) =>
    api.post<VeridianInviteMemberResponse>('/api/veridian/workspaces.inviteMember', data),

  createAPIKey: (data: CreateAPIKeyRequest) =>
    api.post<CreateAPIKeyResponse>('/api/workspaces.createAPIKey', data),

  removeMember: (data: RemoveMemberRequest) =>
    api.post<RemoveMemberResponse>('/api/workspaces.removeMember', data),

  // Integration endpoints
  createIntegration: (data: CreateIntegrationRequest) =>
    api.post<CreateIntegrationResponse>('/api/workspaces.createIntegration', data),

  updateIntegration: (data: UpdateIntegrationRequest) =>
    api.post<UpdateIntegrationResponse>('/api/workspaces.updateIntegration', data),

  deleteIntegration: (data: DeleteIntegrationRequest) =>
    api.post<DeleteIntegrationResponse>('/api/workspaces.deleteIntegration', data),

  // Invitation endpoints
  verifyInvitationToken: (token: string) =>
    api.post<VerifyInvitationTokenResponse>('/api/workspaces.verifyInvitationToken', { token }),

  acceptInvitation: (token: string) =>
    api.post<AcceptInvitationResponse>('/api/workspaces.acceptInvitation', { token }),

  deleteInvitation: (data: DeleteInvitationRequest) =>
    api.post<DeleteInvitationResponse>('/api/workspaces.deleteInvitation', data),

  setUserPermissions: (data: SetUserPermissionsRequest) =>
    api.post<SetUserPermissionsResponse>('/api/workspaces.setUserPermissions', data)
}
