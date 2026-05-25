package domain

// === Veridian patch — Mail provider choice per workspace ===
//
// Ticket : todo/2026-05-25-mail-send-as-user-via-hub-gateway.md §3.4 (vague 6 2026-05-25).
//
// Contexte business : permettre a un workspace d'envoyer ses transactionnels
// via le compte Gmail du user owner (route Hub Mail Gateway §3.3) plutot que
// le sender SMTP generique Veridian. La preference est stockee par workspace
// (1 workspace = 1 boite expediteur), pas par user.
//
// Storage : colonne `workspaces.mail_provider_choice` TEXT NOT NULL DEFAULT
// 'smtp_generic'. Migration V48. Pas de patch upstream : un repo Veridian dedie
// (`veridian_mail_provider_postgres.go`) lit/ecrit cette colonne en SQL brut,
// le repo upstream `workspace_postgres.go` reste intact.

import "context"

// MailProviderChoice qualifie le provider d'envoi mail d'un workspace.
//
// Evolutions futures (additif strict, sans break) :
//   - 'microsoft_via_hub' : envoi via Outlook user owner (Phase B)
//   - 'imap_custom'       : SMTP custom par tenant (Phase C)
//
// Toute extension exige : (1) ALTER CONSTRAINT additif cote DB, (2) ajout
// const + branche dans IsValidMailProviderChoice, (3) doc cote Hub
// (CONTRAT-HUB §3.4 ou equivalent).
type MailProviderChoice string

const (
	// MailProviderSMTPGeneric : sender SMTP Veridian generique (defaut).
	// Comportement actuel : tous les workspaces existants sont backfilles a
	// cette valeur par le DEFAULT de la migration V48.
	MailProviderSMTPGeneric MailProviderChoice = "smtp_generic"

	// MailProviderHubGmail : envoi via POST <hub>/api/mail/send-as-user
	// (Gmail user owner du workspace). Active explicitement par le user
	// depuis l'UI settings/mail-account apres OAuth Google cote Hub
	// (cf. veridian-hub/todo/done/2026-05-25-oauth-google-gmail-client-2-setup-console.md).
	MailProviderHubGmail MailProviderChoice = "hub_gmail"
)

// IsValidMailProviderChoice retourne true si la valeur est un provider connu.
// Chaine vide est INVALIDE (le handler refuse 400 invalid_choice) : le caller
// doit envoyer explicitement 'smtp_generic' ou 'hub_gmail', pas s'appuyer sur
// un defaut implicite. Le defaut DB s'applique uniquement au moment de la
// creation du workspace (V48), pas aux mutations posterieures.
func IsValidMailProviderChoice(c MailProviderChoice) bool {
	switch c {
	case MailProviderSMTPGeneric, MailProviderHubGmail:
		return true
	default:
		return false
	}
}

// VeridianMailProviderRepository est l'interface d'acces a la colonne
// `workspaces.mail_provider_choice`. Le service mail-provider l'utilise pour
// lire/ecrire la preference. Implementation Postgres dans
// `internal/repository/veridian_mail_provider_postgres.go`.
type VeridianMailProviderRepository interface {
	// GetMailProviderChoice retourne la preference courante pour un workspace.
	// Si la row workspace n'existe pas, retourne (MailProviderSMTPGeneric, nil)
	// — semantique safe (defaut = comportement actuel). Le caller doit verifier
	// l'existence du workspace AVANT via workspaceRepo.GetByID s'il a besoin de
	// distinguer "workspace absent" vs "workspace avec defaut".
	GetMailProviderChoice(ctx context.Context, workspaceID string) (MailProviderChoice, error)

	// SetMailProviderChoice met a jour la preference pour un workspace.
	// Idempotent : 2 calls successifs avec la meme valeur = meme resultat.
	// Si la row workspace n'existe pas, retourne ErrWorkspaceNotFound
	// (le UPDATE matche 0 row → notify le caller pour 404 cote handler).
	// La validation de `choice` est faite cote service/handler, le repo
	// fait confiance — c'est le CHECK constraint DB qui protege en dernier
	// ressort si une valeur invalide passe les filtres applicatifs.
	SetMailProviderChoice(ctx context.Context, workspaceID string, choice MailProviderChoice) error
}

// === Webhook events vers Hub ===
//
// Le service emit cet event vers le Hub apres SetMailProviderChoice reussi
// (best-effort, pattern emitter existant — cf. veridian_membership_service.go).
// Le Hub consomme ce signal a titre d'audit cross-app (qui change quoi, quand)
// + pour preparer les pre-flight checks cote Mail Gateway (cache invalidation,
// gauge des workspaces hub_gmail actifs).
const (
	// EventTenantMailProviderChoiceChanged est emis apres SetMailProviderChoice
	// reussi (changement effectif OU re-set identique — l'idempotence est cote
	// repo, l'event est emis a chaque call ecrit pour ne pas casser le signal
	// audit). Payload : {workspace_id, previous_choice, new_choice, changed_by, actor}.
	// `previous_choice` peut etre vide si la row n'avait jamais ete set
	// explicitement (auquel cas le defaut DB 'smtp_generic' est la valeur
	// implicite — le service la lit avant UPDATE pour la tracer).
	EventTenantMailProviderChoiceChanged VeridianEvent = "tenant.mail_provider_choice_changed"
)

// MailProviderChoiceResponse est la projection JSON pour les endpoints GET/POST
// /api/workspaces/{id}/mail-provider-choice. UpdatedAt est present uniquement
// sur la reponse POST (mutation). Sur GET, UpdatedAt est zero — le caller le
// distingue en testant .IsZero() ou en consultant uniquement Choice.
type MailProviderChoiceResponse struct {
	WorkspaceID string             `json:"workspace_id"`
	Choice      MailProviderChoice `json:"choice"`
	UpdatedAt   string             `json:"updated_at,omitempty"` // ISO 8601, omis sur GET
}

// SetMailProviderChoiceInput est le body de POST /api/workspaces/{id}/mail-provider-choice.
type SetMailProviderChoiceInput struct {
	Choice MailProviderChoice `json:"choice"`
	// WorkspaceID est injecte par le handler depuis le path param {id}.
	WorkspaceID string `json:"-"`
}
