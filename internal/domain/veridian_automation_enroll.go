package domain

// === Veridian patch — sprint AI-first API (2026-06-15) ===
// Enrôlement programmatique de contacts dans une automation via API.
//
// Le maillon manquant du pilotage "AI-first" : on pouvait créer/activer/pauser une
// séquence par API, mais pas y enrôler un contact sans déclencher l'événement
// timeline correspondant (list.subscribe, contact.created, custom_event…). Cet
// endpoint permet à un agent (ou Robert) d'injecter un/des contact(s) au node
// d'entrée d'une automation live, en réutilisant la fonction SQL canonique
// automation_enroll_contact (cf. AutomationRepository.EnrollContact) — zéro
// réimplémentation de l'executor.

import (
	"fmt"
	"net/mail"
	"strings"
)

// VeridianEnrollContactsRequest is the payload for POST /api/automations.enroll.
type VeridianEnrollContactsRequest struct {
	WorkspaceID   string   `json:"workspace_id"`
	AutomationID  string   `json:"automation_id"`
	ContactEmails []string `json:"contact_emails"`
}

// VeridianEnrollResult reports the outcome for a single contact email.
type VeridianEnrollResult struct {
	Email  string `json:"email"`
	Status string `json:"status"` // "enrolled", "already_active", or "error"
	Error  string `json:"error,omitempty"`
}

// VeridianEnrollContactsResponse aggregates the per-contact outcomes.
type VeridianEnrollContactsResponse struct {
	Enrolled int                    `json:"enrolled"`      // newly enrolled contacts
	Skipped  int                    `json:"skipped"`       // already active (idempotent no-op)
	Failed   int                    `json:"failed"`        // per-contact errors
	Results  []VeridianEnrollResult `json:"results"`       // one entry per requested email
}

// maxEnrollBatch caps the number of contacts enrolled in a single call to keep the
// request bounded (each contact triggers one SQL round-trip + an existence check).
const maxEnrollBatch = 1000

// Validate checks the enroll request and returns a normalized copy of the emails
// (trimmed, lowercased, deduplicated, syntactically valid).
func (r *VeridianEnrollContactsRequest) Validate() ([]string, error) {
	if r.WorkspaceID == "" {
		return nil, fmt.Errorf("workspace_id is required")
	}
	if r.AutomationID == "" {
		return nil, fmt.Errorf("automation_id is required")
	}
	if len(r.ContactEmails) == 0 {
		return nil, fmt.Errorf("contact_emails is required (at least one email)")
	}
	if len(r.ContactEmails) > maxEnrollBatch {
		return nil, fmt.Errorf("too many contacts: %d (max %d per call)", len(r.ContactEmails), maxEnrollBatch)
	}

	seen := make(map[string]struct{}, len(r.ContactEmails))
	normalized := make([]string, 0, len(r.ContactEmails))
	for _, raw := range r.ContactEmails {
		email := strings.ToLower(strings.TrimSpace(raw))
		if email == "" {
			return nil, fmt.Errorf("contact_emails contains an empty email")
		}
		// Require a bare address (no display name, no angle brackets).
		addr, err := mail.ParseAddress(email)
		if err != nil || addr.Address != email {
			return nil, fmt.Errorf("invalid email format: %q", raw)
		}
		if _, dup := seen[email]; dup {
			continue
		}
		seen[email] = struct{}{}
		normalized = append(normalized, email)
	}

	return normalized, nil
}
