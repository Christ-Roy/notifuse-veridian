package domain

// === Veridian — lecture des inscriptions d'automation (2026-10-27) ===
// Liste paginée (curseur keyset) des contacts inscrits à une automation, avec
// leur statut, leur nœud courant et leur prochaine date. Comble le trou relevé
// par l'architecte : aucune route ne listait les inscrits (automations:enroll
// n'écrit que, nodeExecutions lit un contact à la fois).

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"
	"time"
)

const (
	// VeridianEnrollmentDefaultLimit : taille de page par défaut.
	VeridianEnrollmentDefaultLimit = 50
	// VeridianEnrollmentMaxLimit : plafond explicite (au-delà = erreur, pas de troncature muette).
	VeridianEnrollmentMaxLimit = 500
)

// VeridianAutomationEnrollmentFilter : critères de lecture des inscriptions.
type VeridianAutomationEnrollmentFilter struct {
	WorkspaceID  string
	AutomationID string
	// Status : "" (tous), "active" (en cours : active + sending), "completed",
	// "exited", "failed". Traduit par ParseVeridianEnrollmentStatus.
	Status string
	// Cursor : curseur opaque renvoyé par la page précédente ("" = première page).
	Cursor string
	Limit  int
}

// Validate applique les défauts et les bornes. Une erreur est une entrée
// invalide (400), jamais une troncature silencieuse.
func (f *VeridianAutomationEnrollmentFilter) Validate() error {
	if strings.TrimSpace(f.WorkspaceID) == "" {
		return errors.New("workspace_id is required")
	}
	if strings.TrimSpace(f.AutomationID) == "" {
		return errors.New("automation_id is required")
	}
	if _, err := ParseVeridianEnrollmentStatus(f.Status); err != nil {
		return err
	}
	if f.Limit == 0 {
		f.Limit = VeridianEnrollmentDefaultLimit
	}
	if f.Limit < 1 || f.Limit > VeridianEnrollmentMaxLimit {
		return fmt.Errorf("limit must be between 1 and %d", VeridianEnrollmentMaxLimit)
	}
	if f.Cursor != "" {
		if _, _, err := DecodeVeridianEnrollmentCursor(f.Cursor); err != nil {
			return err
		}
	}
	return nil
}

// ParseVeridianEnrollmentStatus traduit le filtre API en statuts réels.
// "active" englobe « sending » : un contact parqué sur un nœud email en attente
// de la file est toujours en cours d'automation.
func ParseVeridianEnrollmentStatus(s string) ([]ContactAutomationStatus, error) {
	switch strings.TrimSpace(s) {
	case "":
		return nil, nil
	case "active":
		return []ContactAutomationStatus{ContactAutomationStatusActive, ContactAutomationStatusSending}, nil
	case "completed":
		return []ContactAutomationStatus{ContactAutomationStatusCompleted}, nil
	case "exited":
		return []ContactAutomationStatus{ContactAutomationStatusExited}, nil
	case "failed":
		return []ContactAutomationStatus{ContactAutomationStatusFailed}, nil
	default:
		return nil, fmt.Errorf("invalid status %q (use active, completed, exited or failed)", s)
	}
}

// EncodeVeridianEnrollmentCursor encode (entered_at, id) en curseur opaque.
func EncodeVeridianEnrollmentCursor(enteredAt time.Time, id string) string {
	raw := enteredAt.UTC().Format(time.RFC3339Nano) + "|" + id
	return base64.RawURLEncoding.EncodeToString([]byte(raw))
}

// DecodeVeridianEnrollmentCursor décode un curseur ; rejette tout ce qui n'est pas le nôtre.
func DecodeVeridianEnrollmentCursor(c string) (time.Time, string, error) {
	raw, err := base64.RawURLEncoding.DecodeString(c)
	if err != nil {
		return time.Time{}, "", errors.New("invalid cursor")
	}
	parts := strings.SplitN(string(raw), "|", 2)
	if len(parts) != 2 || parts[1] == "" {
		return time.Time{}, "", errors.New("invalid cursor")
	}
	at, err := time.Parse(time.RFC3339Nano, parts[0])
	if err != nil {
		return time.Time{}, "", errors.New("invalid cursor")
	}
	return at, parts[1], nil
}

// VeridianEnrollmentPage : une page d'inscriptions et le curseur de la suivante ("" = fin).
type VeridianEnrollmentPage struct {
	Enrollments []*ContactAutomation `json:"enrollments"`
	NextCursor  string               `json:"next_cursor"`
}

// VeridianAutomationEnrollmentReader lit les inscriptions (implémenté par le repository Postgres).
type VeridianAutomationEnrollmentReader interface {
	ListEnrollmentsPage(ctx context.Context, f VeridianAutomationEnrollmentFilter, statuses []ContactAutomationStatus) (*VeridianEnrollmentPage, error)
}

// VeridianAutomationEnrollmentService : auth (automations:read) puis lecture.
type VeridianAutomationEnrollmentService interface {
	ListEnrollments(ctx context.Context, f VeridianAutomationEnrollmentFilter) (*VeridianEnrollmentPage, error)
}
