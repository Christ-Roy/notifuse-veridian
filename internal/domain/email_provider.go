package domain

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"github.com/Notifuse/notifuse/pkg/notifuse_mjml"
	"github.com/asaskevich/govalidator"
	"github.com/google/uuid"
)

//go:generate mockgen -destination mocks/mock_email_service.go -package mocks github.com/Notifuse/notifuse/internal/domain EmailServiceInterface
//go:generate mockgen -destination mocks/mock_http_client.go -package mocks github.com/Notifuse/notifuse/internal/domain HTTPClient
//go:generate mockgen -destination mocks/mock_ses_client.go -package mocks github.com/Notifuse/notifuse/internal/domain SESClient
//go:generate mockgen -destination mocks/mock_email_provider_service.go -package mocks github.com/Notifuse/notifuse/internal/domain EmailProviderService

// HTTPClient defines the interface for HTTP operations
type HTTPClient interface {
	Do(req *http.Request) (*http.Response, error)
}

// EmailProviderKind defines the type of email provider
type EmailProviderKind string

const (
	EmailProviderKindSMTP      EmailProviderKind = "smtp"
	EmailProviderKindSES       EmailProviderKind = "ses"
	EmailProviderKindSparkPost EmailProviderKind = "sparkpost"
	EmailProviderKindPostmark  EmailProviderKind = "postmark"
	EmailProviderKindMailgun   EmailProviderKind = "mailgun"
	EmailProviderKindMailjet   EmailProviderKind = "mailjet"
	EmailProviderKindSendGrid  EmailProviderKind = "sendgrid"
)

// EmailSender represents an email sender with name and email address
type EmailSender struct {
	ID        string `json:"id"`
	Email     string `json:"email"`
	Name      string `json:"name"`
	IsDefault bool   `json:"is_default"`
}

// NewEmailSender creates a new sender with the given email and name
func NewEmailSender(email, name string) EmailSender {
	return EmailSender{
		ID:        uuid.New().String(),
		Email:     email,
		Name:      name,
		IsDefault: true,
	}
}

// EmailProvider contains configuration for an email service provider
type EmailProvider struct {
	Kind               EmailProviderKind  `json:"kind"`
	SES                *AmazonSESSettings `json:"ses,omitempty"`
	SMTP               *SMTPSettings      `json:"smtp,omitempty"`
	SparkPost          *SparkPostSettings `json:"sparkpost,omitempty"`
	Postmark           *PostmarkSettings  `json:"postmark,omitempty"`
	Mailgun            *MailgunSettings   `json:"mailgun,omitempty"`
	Mailjet            *MailjetSettings   `json:"mailjet,omitempty"`
	SendGrid           *SendGridSettings  `json:"sendgrid,omitempty"`
	Senders            []EmailSender      `json:"senders"`
	RateLimitPerMinute int                `json:"rate_limit_per_minute"`

	// Veridian fork — config cold outbound PAR INFRA d'envoi (R2). L'infra
	// d'envoi EST l'intégration (host/port/IP/relai SMTP + senders + son propre
	// rate). Une IP fraîche en warm-up porte des débits/plafonds plus bas qu'une
	// IP mature, indépendamment du workspace. Ces 3 champs s'insèrent comme
	// niveau INTERMÉDIAIRE dans les cascades de config existantes (broadcast →
	// INFRA → workspace) : cf. veridian_provider_throttle.go (rates) et
	// veridian_daily_cap.go (caps). Tous omitempty → infra non configurée =
	// héritage workspace, comportement upstream strictement inchangé.
	VeridianProviderClassRates    map[string]float64 `json:"veridian_provider_class_rates,omitempty"`
	VeridianProviderClassDailyCap map[string]int     `json:"veridian_provider_class_daily_cap,omitempty"`
	VeridianPerRecipientDailyCap  int                `json:"veridian_per_recipient_daily_cap,omitempty"`

	// Veridian fork — plafond JOURNALIER par ADRESSE ÉMETTRICE (warmup IP, cold
	// outbound). Dimension ÉMETTRICE, distincte des caps ci-dessus qui sont keyés
	// DESTINATAIRE (par adresse reçue / par classe de provider receveur). Ici :
	// max N envois/jour PAR boîte d'envoi (sender) de cette infra. C'est le warmup
	// IP/domaine classique — chaque boîte qui monte en charge porte son propre
	// plafond quotidien, indépendant du destinataire. Source de vérité = COUNT
	// message_history filtré par l'adresse FROM réelle (colonne veridian_sender_email,
	// V53) depuis minuit UTC. Le PLUS RESTRICTIF gagne avec les caps destinataire/
	// classe. Niveau INTERMÉDIAIRE de la cascade (broadcast → INFRA → workspace),
	// comme les caps R2. 0 = pas de plafond émetteur (opt-in strict, non-régression).
	// Persisté dans le JSON blob integrations sans migration ni allowlist (omitempty).
	// Cf. veridian_daily_cap.go (gate veridianPerSenderCapGate).
	VeridianPerSenderDailyCap int `json:"veridian_per_sender_daily_cap,omitempty"`

	// Veridian fork — JITTER TEMPOREL par infra (cold outbound). Amplitude (±) de
	// dispersion du délai de re-planification du throttle minute par classe, en
	// FRACTION du pas nominal (0.30 = ±30 %). Casse le rythme métronomique (tell
	// de machine cold) sans toucher le débit moyen. Niveau INTERMÉDIAIRE de la
	// cascade (broadcast → INFRA → workspace), comme les rates/caps R2. Pointeur :
	// nil = non configuré (→ défaut cold 0.30 appliqué par le gate), *0 = jitter
	// explicitement désactivé. Persisté dans le JSON blob integrations sans
	// migration ni allowlist (omitempty). Cf. veridian_jitter.go.
	VeridianJitterPct *float64 `json:"veridian_jitter_pct,omitempty"`

	// Veridian fork — ANTI-HASH IDENTIQUE par infra (cold outbound). Empêche deux
	// mails au rendu identique (sujet + corps) de partir vers la même classe de
	// provider destinataire dans une fenêtre glissante. enabled : pointeur nil =
	// non configuré (→ défaut cold ON), *false = désactivé voulu. window (heures,
	// <=0 = non configuré → défaut 72h). Niveau INTERMÉDIAIRE de la cascade
	// (broadcast → INFRA → workspace), comme les rates/caps R2. Persisté dans le
	// JSON blob integrations sans migration ni allowlist (omitempty). Cf.
	// veridian_content_hash.go + veridian_content_dedup.go.
	VeridianAntiHashEnabled     *bool `json:"veridian_anti_hash_enabled,omitempty"`
	VeridianAntiHashWindowHours int   `json:"veridian_anti_hash_window_hours,omitempty"`

	// Veridian fork — FENÊTRE D'ENVOI par infra (cold outbound). L'infra d'envoi
	// = le domaine/IP émetteur ; ses horaires ouvrables peuvent différer du
	// workspace (ex. une IP warm-up cantonnée 10h-16h). Niveau INTERMÉDIAIRE de
	// la cascade (broadcast → INFRA → workspace), comme les rates/caps R2. nil =
	// pas de fenêtre infra → héritage workspace, non-régression stricte. Persisté
	// dans le JSON blob integrations sans migration ni allowlist (omitempty).
	// Cf. veridian_sending_window.go + veridian_sending_window_gate.go.
	VeridianSendingWindow *VeridianSendingWindow `json:"veridian_sending_window,omitempty"`

	// Veridian fork — custom tracking domain PAR INFRA d'envoi (Lot 5 cold). En
	// cold, les liens de tracking (pixel ouverture /t/, redirect clic /r/) doivent
	// vivre sur un sous-domaine ALIGNÉ au domaine d'envoi de cette infra (ex.
	// envoi depuis agences-veridian.fr → tracking sur track.agences-veridian.fr).
	// Un lien de tracking vers un domaine tiers (notifuse.app.veridian.site) est un
	// signal anti-spam (mismatch domaine perçu, casse l'alignement DKIM/DMARC). Comme
	// l'infra EST le domaine d'envoi (host/IP/senders), le tracking domain se règle
	// AU NIVEAU INFRA, en cohérence avec les rates/caps R2. Accepte un domaine nu
	// (track.agences-veridian.fr → préfixé https://) ou une URL complète. Prime sur
	// le CustomEndpointURL workspace upstream et sur l'API endpoint global. Vide =
	// fallback endpoint résolu en amont (workspace > global), non-régression stricte.
	// Cf. veridian_tracking_domain.go. omitempty → persisté dans le JSON blob
	// integrations sans migration ni allowlist (comme les autres champs R2).
	VeridianTrackingDomain string `json:"veridian_tracking_domain,omitempty"`

	// Veridian fork — EXCLUSION de classes de provider destinataire PAR INFRA
	// (cold outbound). Liste de classes à NE PAS contacter depuis cette infra
	// d'envoi (ex. exclure ["microsoft"] sur une IP fraîche le temps du warm-up,
	// puis l'ouvrir quand l'IP est mature). Niveau INTERMÉDIAIRE de la cascade
	// (broadcast → INFRA → workspace), comme les rates/caps R2. Distinct des
	// rates/caps (0 ≠ exclu). Persisté dans le JSON blob integrations sans
	// migration ni allowlist (omitempty). Cf. veridian_excluded_classes.go.
	VeridianExcludedProviderClasses []string `json:"veridian_excluded_provider_classes,omitempty"`

	// Veridian fork — WARMUP PROGRESSIF PAR INFRA (rampe auto du cap journalier,
	// 2026-06-17). Au lieu d'un cap statique 1/jour/classe, le cap monte par paliers
	// au fil des jours écoulés depuis le début du warmup de cette infra (standard
	// Lemlist/Instantly). Quand StartedAt + Schedule sont posés, le cap effectif
	// dérivé de `now - StartedAt` PRIME sur VeridianProviderClassDailyCap (uniforme
	// sur toutes les classes) — calculé À LA LECTURE par le worker, AUCUN cron,
	// AUCUNE migration (3 champs omitempty, JSON blob integrations, pattern R2). Vides
	// = pas de warmup, héritage du cap statique (non-régression stricte).
	// Cf. veridian_warmup.go + veridian_daily_cap.go (veridianResolveDailyCaps).
	VeridianWarmupStartedAt *time.Time `json:"veridian_warmup_started_at,omitempty"`
	VeridianWarmupSchedule  []int      `json:"veridian_warmup_schedule,omitempty"`
	VeridianWarmupStepDays  int        `json:"veridian_warmup_step_days,omitempty"`
}

// Validate validates the email provider settings
func (e *EmailProvider) Validate(passphrase string) error {
	// If Kind is empty, consider it as not configured
	if e.Kind == "" {
		return nil
	}

	// Validate rate limit
	if e.RateLimitPerMinute <= 0 {
		return fmt.Errorf("rate limit per minute is required and must be greater than 0")
	}

	// Validate senders
	if len(e.Senders) == 0 {
		return fmt.Errorf("at least one sender is required")
	}

	for i, sender := range e.Senders {
		if sender.Email == "" {
			return fmt.Errorf("sender email is required for sender at index %d", i)
		}
		if !govalidator.IsEmail(sender.Email) {
			return fmt.Errorf("invalid sender email: %s at index %d", sender.Email, i)
		}
		if sender.Name == "" {
			return fmt.Errorf("sender name is required for sender at index %d", i)
		}
		// If ID is set but not a valid UUID, generate a new one
		if sender.ID != "" && !govalidator.IsUUID(sender.ID) {
			newUUID := uuid.New().String()
			e.Senders[i].ID = newUUID
		}
	}

	// Validate Kind value
	switch e.Kind {
	case EmailProviderKindSMTP:
		if e.SMTP == nil {
			return fmt.Errorf("SMTP settings required when email provider kind is smtp")
		}
		return e.SMTP.Validate(passphrase)
	case EmailProviderKindSES:
		if e.SES == nil {
			return fmt.Errorf("SES settings required when email provider kind is ses")
		}
		return e.SES.Validate(passphrase)
	case EmailProviderKindSparkPost:
		if e.SparkPost == nil {
			return fmt.Errorf("SparkPost settings required when email provider kind is sparkpost")
		}
		return e.SparkPost.Validate(passphrase)
	case EmailProviderKindPostmark:
		if e.Postmark == nil {
			return fmt.Errorf("postmark settings required when email provider kind is postmark")
		}
		return e.Postmark.Validate(passphrase)
	case EmailProviderKindMailgun:
		if e.Mailgun == nil {
			return fmt.Errorf("mailgun settings required when email provider kind is mailgun")
		}
		return e.Mailgun.Validate(passphrase)
	case EmailProviderKindMailjet:
		if e.Mailjet == nil {
			return fmt.Errorf("mailjet settings required when email provider kind is mailjet")
		}
		return e.Mailjet.Validate(passphrase)
	case EmailProviderKindSendGrid:
		if e.SendGrid == nil {
			return fmt.Errorf("sendgrid settings required when email provider kind is sendgrid")
		}
		return e.SendGrid.Validate(passphrase)
	default:
		return fmt.Errorf("invalid email provider kind: %s", e.Kind)
	}
}

func (e *EmailProvider) GetSender(id string) *EmailSender {
	if id != "" {
		for i := range e.Senders {
			if e.Senders[i].ID == id {
				return &e.Senders[i]
			}
		}
	}

	// use default sender
	for i := range e.Senders {
		if e.Senders[i].IsDefault {
			return &e.Senders[i]
		}
	}

	return nil
}

// EncryptSecretKeys encrypts all secret keys in the email provider
func (e *EmailProvider) EncryptSecretKeys(passphrase string) error {
	if e.Kind == EmailProviderKindSES && e.SES != nil && e.SES.SecretKey != "" {
		if err := e.SES.EncryptSecretKey(passphrase); err != nil {
			return err
		}
		e.SES.SecretKey = ""
	}

	if e.Kind == EmailProviderKindSMTP && e.SMTP != nil {
		if e.SMTP.Password != "" {
			if err := e.SMTP.EncryptPassword(passphrase); err != nil {
				return err
			}
			e.SMTP.Password = ""
		}
		if e.SMTP.OAuth2ClientSecret != "" {
			if err := e.SMTP.EncryptOAuth2ClientSecret(passphrase); err != nil {
				return err
			}
			e.SMTP.OAuth2ClientSecret = ""
		}
		if e.SMTP.OAuth2RefreshToken != "" {
			if err := e.SMTP.EncryptOAuth2RefreshToken(passphrase); err != nil {
				return err
			}
			e.SMTP.OAuth2RefreshToken = ""
		}
	}

	if e.Kind == EmailProviderKindSparkPost && e.SparkPost != nil && e.SparkPost.APIKey != "" {
		if err := e.SparkPost.EncryptAPIKey(passphrase); err != nil {
			return err
		}
		e.SparkPost.APIKey = ""
	}

	if e.Kind == EmailProviderKindPostmark && e.Postmark != nil && e.Postmark.ServerToken != "" {
		if err := e.Postmark.EncryptServerToken(passphrase); err != nil {
			return err
		}
		e.Postmark.ServerToken = ""
	}

	if e.Kind == EmailProviderKindMailgun && e.Mailgun != nil && e.Mailgun.APIKey != "" {
		if err := e.Mailgun.EncryptAPIKey(passphrase); err != nil {
			return err
		}
		e.Mailgun.APIKey = ""
	}

	if e.Kind == EmailProviderKindMailjet && e.Mailjet != nil {
		if e.Mailjet.APIKey != "" {
			if err := e.Mailjet.EncryptAPIKey(passphrase); err != nil {
				return err
			}
			e.Mailjet.APIKey = ""
		}

		if e.Mailjet.SecretKey != "" {
			if err := e.Mailjet.EncryptSecretKey(passphrase); err != nil {
				return err
			}
			e.Mailjet.SecretKey = ""
		}
	}

	if e.Kind == EmailProviderKindSendGrid && e.SendGrid != nil && e.SendGrid.APIKey != "" {
		if err := e.SendGrid.EncryptAPIKey(passphrase); err != nil {
			return err
		}
		e.SendGrid.APIKey = ""
	}

	return nil
}

// DecryptSecretKeys decrypts all encrypted secret keys in the email provider
func (e *EmailProvider) DecryptSecretKeys(passphrase string) error {
	if e.Kind == EmailProviderKindSES && e.SES != nil && e.SES.EncryptedSecretKey != "" {
		if err := e.SES.DecryptSecretKey(passphrase); err != nil {
			return err
		}
	}

	if e.Kind == EmailProviderKindSMTP && e.SMTP != nil {
		if e.SMTP.EncryptedUsername != "" {
			if err := e.SMTP.DecryptUsername(passphrase); err != nil {
				return err
			}
		}
		if e.SMTP.EncryptedPassword != "" {
			if err := e.SMTP.DecryptPassword(passphrase); err != nil {
				return err
			}
		}
		if e.SMTP.EncryptedOAuth2ClientSecret != "" {
			if err := e.SMTP.DecryptOAuth2ClientSecret(passphrase); err != nil {
				return err
			}
		}
		if e.SMTP.EncryptedOAuth2RefreshToken != "" {
			if err := e.SMTP.DecryptOAuth2RefreshToken(passphrase); err != nil {
				return err
			}
		}
	}

	if e.Kind == EmailProviderKindSparkPost && e.SparkPost != nil && e.SparkPost.EncryptedAPIKey != "" {
		if err := e.SparkPost.DecryptAPIKey(passphrase); err != nil {
			return err
		}
	}

	if e.Kind == EmailProviderKindPostmark && e.Postmark != nil && e.Postmark.EncryptedServerToken != "" {
		if err := e.Postmark.DecryptServerToken(passphrase); err != nil {
			return err
		}
	}

	if e.Kind == EmailProviderKindMailgun && e.Mailgun != nil && e.Mailgun.EncryptedAPIKey != "" {
		if err := e.Mailgun.DecryptAPIKey(passphrase); err != nil {
			return err
		}
	}

	if e.Kind == EmailProviderKindMailjet && e.Mailjet != nil {
		if e.Mailjet.EncryptedAPIKey != "" {
			if err := e.Mailjet.DecryptAPIKey(passphrase); err != nil {
				return err
			}
		}

		if e.Mailjet.EncryptedSecretKey != "" {
			if err := e.Mailjet.DecryptSecretKey(passphrase); err != nil {
				return err
			}
		}
	}

	if e.Kind == EmailProviderKindSendGrid && e.SendGrid != nil && e.SendGrid.EncryptedAPIKey != "" {
		if err := e.SendGrid.DecryptAPIKey(passphrase); err != nil {
			return err
		}
	}

	return nil
}

type EmailOptions struct {
	FromName           *string      `json:"from_name,omitempty"`       // Override default sender from name
	Subject            *string      `json:"subject,omitempty"`         // Override template subject
	SubjectPreview     *string      `json:"subject_preview,omitempty"` // Override template preheader
	CC                 []string     `json:"cc,omitempty"`
	BCC                []string     `json:"bcc,omitempty"`
	ReplyTo            string       `json:"reply_to,omitempty"`
	Attachments        []Attachment `json:"attachments,omitempty"`
	ListUnsubscribeURL string       `json:"list_unsubscribe_url,omitempty"` // RFC-8058 one-click unsubscribe URL
}

// IsEmpty returns true if no email options are set
func (eo EmailOptions) IsEmpty() bool {
	return eo.FromName == nil &&
		eo.Subject == nil &&
		eo.SubjectPreview == nil &&
		len(eo.CC) == 0 &&
		len(eo.BCC) == 0 &&
		eo.ReplyTo == ""
}

// ToChannelOptions converts EmailOptions to ChannelOptions for storage
func (eo EmailOptions) ToChannelOptions() *ChannelOptions {
	// Don't create ChannelOptions if all fields are empty
	if eo.IsEmpty() {
		return nil
	}

	return &ChannelOptions{
		FromName:       eo.FromName,
		Subject:        eo.Subject,
		SubjectPreview: eo.SubjectPreview,
		CC:             eo.CC,
		BCC:            eo.BCC,
		ReplyTo:        eo.ReplyTo,
	}
}

// SendEmailProviderRequest encapsulates all parameters needed to send an email via a provider
type SendEmailProviderRequest struct {
	WorkspaceID   string         `validate:"required"`
	IntegrationID string         `validate:"required"`
	MessageID     string         `validate:"required"`
	FromAddress   string         `validate:"required"`
	FromName      string         `validate:"required"`
	To            string         `validate:"required"`
	Subject       string         `validate:"required"`
	Content       string         `validate:"required"`
	Provider      *EmailProvider `validate:"required"`
	EmailOptions  EmailOptions
}

// Validate ensures all required fields are present and valid
func (r *SendEmailProviderRequest) Validate() error {
	if r.WorkspaceID == "" {
		return fmt.Errorf("workspace ID is required")
	}
	if r.IntegrationID == "" {
		return fmt.Errorf("integration ID is required")
	}
	if r.MessageID == "" {
		return fmt.Errorf("message ID is required")
	}
	if r.FromAddress == "" {
		return fmt.Errorf("from address is required")
	}
	if r.FromName == "" {
		return fmt.Errorf("from name is required")
	}
	if r.To == "" {
		return fmt.Errorf("to address is required")
	}
	if r.Subject == "" {
		return fmt.Errorf("subject is required")
	}
	if r.Content == "" {
		return fmt.Errorf("content is required")
	}
	if r.Provider == nil {
		return fmt.Errorf("email provider is required")
	}
	return nil
}

// SendEmailRequest encapsulates all parameters needed to send an email using a template
type SendEmailRequest struct {
	// Core identification
	WorkspaceID                 string `validate:"required"`
	IntegrationID               string `validate:"required"`
	MessageID                   string `validate:"required"`
	ExternalID                  *string
	AutomationID                *string
	TransactionalNotificationID *string

	// Target and content
	Contact        *Contact        `validate:"required"`
	TemplateConfig ChannelTemplate `validate:"required"`
	MessageData    MessageData

	// Configuration
	TrackingSettings notifuse_mjml.TrackingSettings
	EmailProvider    *EmailProvider `validate:"required"`
	EmailOptions     EmailOptions
}

// Validate ensures all required fields are present and valid
func (r *SendEmailRequest) Validate() error {
	if r.WorkspaceID == "" {
		return fmt.Errorf("workspace ID is required")
	}
	if r.IntegrationID == "" {
		return fmt.Errorf("integration ID is required")
	}
	if r.MessageID == "" {
		return fmt.Errorf("message ID is required")
	}
	if r.Contact == nil {
		return fmt.Errorf("contact is required")
	}
	if r.EmailProvider == nil {
		return fmt.Errorf("email provider is required")
	}
	if r.TemplateConfig.TemplateID == "" {
		return fmt.Errorf("template ID is required")
	}
	return nil
}

// EmailServiceInterface defines the interface for the email service
type EmailServiceInterface interface {
	TestEmailProvider(ctx context.Context, workspaceID string, provider EmailProvider, to string) error
	SendEmail(ctx context.Context, request SendEmailProviderRequest, isMarketing bool) error
	SendEmailForTemplate(ctx context.Context, request SendEmailRequest) error
	VisitLink(ctx context.Context, messageID string, workspaceID string) error
	OpenEmail(ctx context.Context, messageID string, workspaceID string) error
}

type EmailProviderService interface {
	SendEmail(ctx context.Context, request SendEmailProviderRequest) error
}
