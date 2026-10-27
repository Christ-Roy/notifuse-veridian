package domain

// === Veridian — file d'envoi : lister et annuler (2026-10-27) ===
// Lecture paginée des entrées de file d'une automation (statut, classe, profil,
// nœud) et annulation explicite d'une entrée ou d'un lot. Une annulation SUPPRIME
// la ligne de email_queue (une entrée envoyée l'est aussi : la file ne garde que
// du travail à faire) et écrit une ligne de journal dans veridian_send_decisions
// (outcome « cancelled ») dans la MÊME transaction. Aucune suppression silencieuse.

import (
	"context"
	"errors"
	"strings"
)

const (
	VeridianQueueEntriesDefaultLimit = 50
	VeridianQueueEntriesMaxLimit     = 500

	VeridianQueueCancelDefaultLimit = 100
	VeridianQueueCancelMaxLimit     = 500
	// VeridianQueueCancelReasonMaxLen borne le motif (il entre dans le journal, 200 caractères au total).
	VeridianQueueCancelReasonMaxLen = 120

	// VeridianReasonOperatorCancel : motif de journal d'une annulation opérateur.
	VeridianReasonOperatorCancel = "operator_cancel"
	// VeridianOutcomeCancelled : issue de journal d'une entrée annulée.
	VeridianOutcomeCancelled = "cancelled"
)

// veridianQueueLiveStatuses : statuts d'une entrée encore dans la file.
var veridianQueueLiveStatuses = map[string]bool{"pending": true, "processing": true, "failed": true, "paused": true}

// veridianQueueCancellableStatuses : une entrée « processing » est en vol : on ne l'annule pas.
var veridianQueueCancellableStatuses = map[string]bool{"pending": true, "failed": true, "paused": true}

// VeridianQueueEntriesFilter : critères de lecture de la file pour une automation.
type VeridianQueueEntriesFilter struct {
	WorkspaceID  string
	AutomationID string
	NodeID       string
	ProfileID    string
	Class        string
	Status       string
	Cursor       string
	Limit        int
}

// Validate applique les défauts et les bornes.
func (f *VeridianQueueEntriesFilter) Validate() error {
	if strings.TrimSpace(f.WorkspaceID) == "" {
		return errors.New("workspace_id is required")
	}
	if strings.TrimSpace(f.AutomationID) == "" {
		return errors.New("automation_id is required (la file se lit par automation)")
	}
	if f.Status != "" && !veridianQueueLiveStatuses[f.Status] {
		return errors.New("invalid status (use pending, processing, failed or paused)")
	}
	if f.Limit == 0 {
		f.Limit = VeridianQueueEntriesDefaultLimit
	}
	if f.Limit < 1 || f.Limit > VeridianQueueEntriesMaxLimit {
		return errors.New("limit must be between 1 and 500")
	}
	if f.Cursor != "" {
		if _, _, err := DecodeVeridianEnrollmentCursor(f.Cursor); err != nil {
			return err
		}
	}
	return nil
}

// VeridianQueueEntry : une entrée de file, telle qu'un agent la lit.
type VeridianQueueEntry struct {
	ID           string  `json:"id"`
	Status       string  `json:"status"`
	ContactEmail string  `json:"contact_email"`
	AutomationID string  `json:"automation_id"`
	NodeID       string  `json:"node_id,omitempty"`
	Reason       string  `json:"reason"`
	ReasonDetail string  `json:"reason_detail,omitempty"`
	ProfileID    string  `json:"profile_id,omitempty"`
	Class        string  `json:"class,omitempty"`
	CreatedAt    string  `json:"created_at"`
	NextRetryAt  *string `json:"next_retry_at,omitempty"`
}

// VeridianQueueEntriesPage : une page d'entrées et le curseur de la suivante.
type VeridianQueueEntriesPage struct {
	Entries    []*VeridianQueueEntry `json:"entries"`
	NextCursor string                `json:"next_cursor"`
}

// VeridianQueueCancelRequest : annulation d'entrées par identifiants ou par filtre borné.
type VeridianQueueCancelRequest struct {
	WorkspaceID  string   `json:"workspace_id"`
	AutomationID string   `json:"automation_id,omitempty"`
	NodeID       string   `json:"node_id,omitempty"`
	ProfileID    string   `json:"profile_id,omitempty"`
	Class        string   `json:"class,omitempty"`
	Status       string   `json:"status,omitempty"`
	EntryIDs     []string `json:"entry_ids,omitempty"`
	Reason       string   `json:"reason"`
	Limit        int      `json:"limit,omitempty"`
	DryRun       bool     `json:"dry_run,omitempty"`
}

// Validate : un motif obligatoire, au moins un périmètre (entrées ou automation), borné.
func (r *VeridianQueueCancelRequest) Validate() error {
	if strings.TrimSpace(r.WorkspaceID) == "" {
		return errors.New("workspace_id is required")
	}
	r.Reason = strings.TrimSpace(r.Reason)
	if r.Reason == "" {
		return errors.New("reason is required (il entre dans le journal)")
	}
	if len([]rune(r.Reason)) > VeridianQueueCancelReasonMaxLen {
		return errors.New("reason is too long (120 characters max)")
	}
	if strings.TrimSpace(r.AutomationID) == "" && len(r.EntryIDs) == 0 {
		return errors.New("automation_id or entry_ids is required (jamais toute la file)")
	}
	if r.Status != "" && !veridianQueueCancellableStatuses[r.Status] {
		return errors.New("invalid status (use pending, failed or paused ; processing n'est pas annulable)")
	}
	if r.Limit == 0 {
		r.Limit = VeridianQueueCancelDefaultLimit
	}
	if r.Limit < 1 || r.Limit > VeridianQueueCancelMaxLimit {
		return errors.New("limit must be between 1 and 500")
	}
	return nil
}

// VeridianQueueCancelledEntry : une entrée retirée (ou qui le serait en dry-run).
type VeridianQueueCancelledEntry struct {
	ID           string `json:"id"`
	ContactEmail string `json:"contact_email"`
	AutomationID string `json:"automation_id,omitempty"`
	NodeID       string `json:"node_id,omitempty"`
}

// VeridianQueueCancelResult : bilan d'une annulation. Matched = entrées éligibles
// avant la limite ; Cancelled = retirées ; si Matched > Cancelled, il en reste.
type VeridianQueueCancelResult struct {
	DryRun    bool                           `json:"dry_run"`
	Matched   int                            `json:"matched"`
	Cancelled int                            `json:"cancelled"`
	Entries   []*VeridianQueueCancelledEntry `json:"entries"`
}

// VeridianQueueEntriesRepository lit et annule les entrées (Postgres).
type VeridianQueueEntriesRepository interface {
	ListEntries(ctx context.Context, f VeridianQueueEntriesFilter) (*VeridianQueueEntriesPage, error)
	// CancelEntries supprime les entrées éligibles et journalise chacune, dans une seule transaction.
	// actor (courriel) et motif entrent dans le détail du journal.
	CancelEntries(ctx context.Context, req VeridianQueueCancelRequest, actor string) (*VeridianQueueCancelResult, error)
}

// VeridianQueueEntriesService : auth, droits, validation, puis repository.
type VeridianQueueEntriesService interface {
	ListEntries(ctx context.Context, f VeridianQueueEntriesFilter) (*VeridianQueueEntriesPage, error)
	CancelEntries(ctx context.Context, req VeridianQueueCancelRequest) (*VeridianQueueCancelResult, error)
}
