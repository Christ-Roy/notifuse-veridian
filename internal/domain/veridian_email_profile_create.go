package domain

import (
	"context"
	"fmt"
	"strings"
)

// Veridian fork — lot 3 « page Profils d'envoi » (08/10/2026).
//
// emailProfiles.create : crée d'un seul geste un profil d'envoi SMTP et, si
// demandée, sa boîte IMAP de retour liée. Atomique : le workspace est écrit une
// seule fois, avec les deux intégrations et le lien. Une erreur de validation
// ne laisse donc ni profil sans boîte, ni boîte orpheline. Le secret n'est
// jamais renvoyé.

const (
	VeridianCreateProfileTypeSMTPIMAP         = "smtp_imap"
	VeridianCreateProfileTypeGmailAppPassword = "gmail_app_password"

	VeridianGmailSMTPHost = "smtp.gmail.com"
	VeridianGmailSMTPPort = 587
	VeridianGmailIMAPHost = "imap.gmail.com"
	VeridianGmailIMAPPort = 993

	// Cadence technique posée à la création (frein de sécurité, pas la capacité).
	VeridianCreateProfileGmailRatePerMinute   = 1
	VeridianCreateProfileDefaultRatePerMinute = 60
)

// VeridianCreateProfileSMTP est le transport SMTP saisi.
type VeridianCreateProfileSMTP struct {
	Host     string `json:"host"`
	Port     int    `json:"port"`
	UseTLS   bool   `json:"use_tls"`
	Username string `json:"username"`
	Password string `json:"password"`
}

// VeridianCreateProfileIMAP est la boîte de retour facultative.
type VeridianCreateProfileIMAP struct {
	Host     string `json:"host"`
	Port     int    `json:"port"`
	UseTLS   bool   `json:"use_tls"`
	Username string `json:"username"`
	Password string `json:"password"`
	Folder   string `json:"folder,omitempty"`
}

type VeridianCreateEmailProfileRequest struct {
	WorkspaceID string `json:"workspace_id"`
	// Type : smtp_imap | gmail_app_password.
	Type        string `json:"type"`
	Name        string `json:"name"`
	SenderEmail string `json:"sender_email"`
	SenderName  string `json:"sender_name"`
	// SMTP : obligatoire pour smtp_imap. Ignoré pour Gmail (hôte et port imposés).
	SMTP *VeridianCreateProfileSMTP `json:"smtp,omitempty"`
	// IMAP : facultatif pour smtp_imap. Pour Gmail, imap.gmail.com est créé et lié
	// avec la même adresse et le même mot de passe d'application.
	IMAP *VeridianCreateProfileIMAP `json:"imap,omitempty"`
	// Gmail seulement.
	AppPassword      string `json:"app_password,omitempty"`
	GmailAccountType string `json:"gmail_account_type,omitempty"`
	ProfileDailyCap  int    `json:"profile_daily_cap,omitempty"`
}

type VeridianCreateEmailProfileResult struct {
	IntegrationID     string `json:"integration_id"`
	IMAPIntegrationID string `json:"imap_integration_id,omitempty"`
}

// Validate vérifie la forme de la demande. Les contrôles métier fins (plafond
// Gmail, 16 caractères du mot de passe d'application) passent par
// Integration.Validate au moment de la création.
func (r *VeridianCreateEmailProfileRequest) Validate() error {
	if strings.TrimSpace(r.WorkspaceID) == "" {
		return fmt.Errorf("workspace_id is required")
	}
	if strings.TrimSpace(r.SenderEmail) == "" || !strings.Contains(r.SenderEmail, "@") {
		return fmt.Errorf("a valid sender_email is required")
	}
	switch r.Type {
	case VeridianCreateProfileTypeGmailAppPassword:
		if strings.TrimSpace(r.AppPassword) == "" {
			return fmt.Errorf("app_password is required")
		}
		switch r.GmailAccountType {
		case "", "personal", "workspace":
		default:
			return fmt.Errorf("gmail_account_type must be personal or workspace")
		}
	case VeridianCreateProfileTypeSMTPIMAP:
		if r.SMTP == nil || strings.TrimSpace(r.SMTP.Host) == "" || strings.TrimSpace(r.SMTP.Username) == "" || r.SMTP.Password == "" {
			return fmt.Errorf("smtp host, username and password are required")
		}
		if r.SMTP.Port <= 0 || r.SMTP.Port > 65535 {
			return fmt.Errorf("invalid smtp port")
		}
		if r.IMAP != nil && (strings.TrimSpace(r.IMAP.Host) == "" || strings.TrimSpace(r.IMAP.Username) == "" || r.IMAP.Password == "") {
			return fmt.Errorf("imap host, username and password are required when a return inbox is requested")
		}
	default:
		return fmt.Errorf("unsupported profile type: %q", r.Type)
	}
	return nil
}

type VeridianEmailProfileCreateService interface {
	CreateEmailProfile(ctx context.Context, req VeridianCreateEmailProfileRequest) (*VeridianCreateEmailProfileResult, error)
}
