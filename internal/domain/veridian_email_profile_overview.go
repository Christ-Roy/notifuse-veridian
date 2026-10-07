package domain

import (
	"context"
	"strings"
	"time"
)

// Veridian fork — lot 2 « vérité d'un profil » (08/10/2026).
//
// Contrat de GET /api/veridian/emailProfiles.overview : une seule lecture pour
// l'écran « Profils d'envoi » et pour le CLI (`notifuse profiles:overview`).
// Couvre TOUS les profils email du workspace, commerciaux comme transactionnels
// (emailProfiles.usage ne couvrait que le pool commercial). Aucun secret : ni
// mot de passe, ni jeton, ni chiffré.

// Types de profil d'envoi.
const (
	VeridianProfileTypeSMTP             = "smtp"
	VeridianProfileTypeGmailAppPassword = "gmail_app_password"
	VeridianProfileTypeGmailOAuth       = "gmail_oauth"
)

// VeridianProfileType classe un profil : SMTP générique, Gmail par mot de passe
// d'application, Gmail OAuth, ou le nom du fournisseur API (ses, sendgrid...).
func (e *EmailProvider) VeridianProfileType() string {
	if e == nil {
		return ""
	}
	if e.Kind != EmailProviderKindSMTP {
		return string(e.Kind)
	}
	if e.VeridianIsGmailProfile() {
		if e.SMTP != nil && e.SMTP.AuthType == "oauth2" {
			return VeridianProfileTypeGmailOAuth
		}
		return VeridianProfileTypeGmailAppPassword
	}
	return VeridianProfileTypeSMTP
}

type VeridianOverviewSender struct {
	Email     string `json:"email"`
	Name      string `json:"name"`
	IsDefault bool   `json:"is_default"`
}

// VeridianOverviewInbox est une boîte IMAP de retour (réponses et rejets).
type VeridianOverviewInbox struct {
	IntegrationID string `json:"integration_id"`
	Name          string `json:"name"`
	Host          string `json:"host"`
	Address       string `json:"address"`
	Folder        string `json:"folder,omitempty"`
	// LinkedProfiles : profils rattachés à cette boîte. Vide = boîte de retour
	// GLOBALE du workspace (la détection reste de toute façon globale).
	LinkedProfiles []string `json:"linked_profiles"`
}

type VeridianEmailProfileOverview struct {
	IntegrationID         string                   `json:"integration_id"`
	Name                  string                   `json:"name"`
	Kind                  string                   `json:"kind"`
	Type                  string                   `json:"type"`
	Usage                 string                   `json:"usage"`
	InRotation            bool                     `json:"in_rotation"`
	Paused                bool                     `json:"paused"`
	Verified              bool                     `json:"verified"`
	VerifiedAt            *time.Time               `json:"verified_at"`
	CredentialsConfigured bool                     `json:"credentials_configured"`
	Senders               []VeridianOverviewSender `json:"senders"`
	// ReturnInbox : boîte IMAP liée au profil (nil = aucun lien).
	ReturnInbox *VeridianOverviewInbox `json:"return_inbox"`
	Plan        VeridianEffectivePlan  `json:"plan"`
}

type VeridianEmailProfilesOverviewTotals struct {
	CommercialSentToday    int `json:"commercial_sent_today"`
	TransactionalSentToday int `json:"transactional_sent_today"`
	// CommercialCapacityToday : somme des plafonds du jour des profils commerciaux
	// actifs ; nil dès qu'un profil actif n'a aucun plafond.
	CommercialCapacityToday  *int `json:"commercial_capacity_today"`
	ActiveCommercialProfiles int  `json:"active_commercial_profiles"`
	PausedProfiles           int  `json:"paused_profiles"`
}

type VeridianEmailProfilesOverview struct {
	Date        string                         `json:"date"`
	GeneratedAt time.Time                      `json:"generated_at"`
	Timezone    string                         `json:"timezone"`
	Profiles    []VeridianEmailProfileOverview `json:"profiles"`
	// GlobalInboxes : boîtes IMAP liées à aucun profil (retours globaux).
	GlobalInboxes []VeridianOverviewInbox `json:"global_inboxes"`
	// UsageConflicts : profils déclarés à la fois dans le pool commercial et
	// transactionnels (règle d'exclusivité violée). Vide en régime normal.
	UsageConflicts []string                            `json:"usage_conflicts"`
	Totals         VeridianEmailProfilesOverviewTotals `json:"totals"`
}

// VeridianPlanObservationRow : envois acceptés du jour, groupés.
type VeridianPlanObservationRow struct {
	ProfileID     string
	SenderEmail   string
	ProviderClass string
	Accepted      int
}

// VeridianPlanCounterRow : une ligne de veridian_daily_quota_counters du jour.
type VeridianPlanCounterRow struct {
	Kind          string
	Scope         string // identifiant du profil (kind profile) ou domaine émetteur
	ProviderClass string
	Used          int
}

type VeridianEmailProfileOverviewRepository interface {
	// GetPlanObservations lit les compteurs du jour UTC (depuis `since`) : envois
	// acceptés (message_history) et réservations atomiques.
	GetPlanObservations(ctx context.Context, workspaceID string, since time.Time) ([]VeridianPlanObservationRow, []VeridianPlanCounterRow, error)
}

type VeridianEmailProfileOverviewService interface {
	GetEmailProfilesOverview(ctx context.Context, workspaceID string) (*VeridianEmailProfilesOverview, error)
}

// VeridianBuildPlanObserved agrège les lignes brutes en compteurs pour un profil.
// Les domaines et adresses sont comptés tous profils confondus : la chauffe et
// les plafonds de classe sont keyés par domaine émetteur, pas par profil.
func VeridianBuildPlanObserved(profileID string, rows []VeridianPlanObservationRow, counters []VeridianPlanCounterRow) VeridianPlanObserved {
	obs := VeridianPlanObserved{
		DomainSent:          map[string]int{},
		SenderSent:          map[string]int{},
		DomainClassSent:     map[string]map[string]int{},
		DomainReserved:      map[string]int{},
		DomainClassReserved: map[string]map[string]int{},
	}
	for _, r := range rows {
		if r.ProfileID == profileID && profileID != "" {
			obs.ProfileAccepted += r.Accepted
		}
		email := strings.ToLower(strings.TrimSpace(r.SenderEmail))
		if email == "" {
			continue
		}
		obs.SenderSent[email] += r.Accepted
		d := veridianDomainFromEmail(email)
		if d == "" {
			continue
		}
		obs.DomainSent[d] += r.Accepted
		if r.ProviderClass != "" {
			if obs.DomainClassSent[d] == nil {
				obs.DomainClassSent[d] = map[string]int{}
			}
			obs.DomainClassSent[d][r.ProviderClass] += r.Accepted
		}
	}
	for _, c := range counters {
		switch c.Kind {
		case VeridianDailyQuotaKindProfile:
			if c.Scope == profileID {
				obs.ProfileReserved = c.Used
			}
		case VeridianDailyQuotaKindWarmup:
			obs.DomainReserved[c.Scope] = c.Used
		case VeridianDailyQuotaKindProviderClass:
			if obs.DomainClassReserved[c.Scope] == nil {
				obs.DomainClassReserved[c.Scope] = map[string]int{}
			}
			obs.DomainClassReserved[c.Scope][c.ProviderClass] = c.Used
		}
	}
	return obs
}
