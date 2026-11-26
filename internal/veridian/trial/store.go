// Package trial stocke le statut d'essai envoyé par le Hub avec le plan (CONTRAT-BILLING v2 §3 :
// trial_status, trial_ends_at). Notifuse le garde et le rend dans la découverte by-email ; il n'en applique
// aucune lecture seule (VERIDIAN_TRIAL_READONLY_ENFORCED, coupé par défaut, décision de Robert). Fiche 82.
package trial

import (
	"context"
	"database/sql"
	"time"

	"github.com/lib/pq"

	"github.com/Notifuse/notifuse/internal/domain"
	"github.com/Notifuse/notifuse/internal/veridian/tenantlink"
)

// Store : statut d'essai sur la table système veridian_plan (colonnes V72).
type Store struct{ DB *sql.DB }

var _ domain.TrialStore = (*Store)(nil)
var _ domain.HubTenantStore = (*Store)(nil)

// SetHubTenant écrit l'id de tenant du Hub (V73) par l'unique écriture du lien (tenantlink), jamais un re-pointage.
// sql.ErrNoRows si le workspace n'a pas de plan ; *domain.HubTenantConflict si le lien contredit celui en place.
func (s *Store) SetHubTenant(ctx context.Context, workspaceID, hubTenantID string) error {
	_, err := (&tenantlink.Store{DB: s.DB}).Link(ctx, workspaceID, hubTenantID, false)
	return err
}

// HubTenantOf rend le tenant du Hub relié à l'espace (tenantlink), "" sans lien ni plan.
func (s *Store) HubTenantOf(ctx context.Context, workspaceID string) (string, error) {
	return (&tenantlink.Store{DB: s.DB}).HubTenantOf(ctx, workspaceID)
}

// SetTrial écrit le statut d'essai d'un workspace. sql.ErrNoRows si le workspace n'a pas de plan.
func (s *Store) SetTrial(ctx context.Context, workspaceID, status string, endsAt *time.Time) error {
	res, err := s.DB.ExecContext(ctx, `
		UPDATE veridian_plan
		SET trial_status = $2, trial_ends_at = $3, updated_at = NOW()
		WHERE workspace_id = $1`, workspaceID, status, endsAt)
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return sql.ErrNoRows
	}
	return nil
}

// GetTrials rend le statut d'essai des workspaces donnés ; un workspace qui n'en a jamais reçu est absent.
func (s *Store) GetTrials(ctx context.Context, workspaceIDs []string) (map[string]domain.TenantTrial, error) {
	out := map[string]domain.TenantTrial{}
	if len(workspaceIDs) == 0 {
		return out, nil
	}
	rows, err := s.DB.QueryContext(ctx, `
		SELECT workspace_id, trial_status, trial_ends_at
		FROM veridian_plan
		WHERE workspace_id = ANY($1) AND trial_status IS NOT NULL`, pq.Array(workspaceIDs))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var id, status string
		var ends sql.NullTime
		if err := rows.Scan(&id, &status, &ends); err != nil {
			return nil, err
		}
		t := domain.TenantTrial{Status: status}
		if ends.Valid {
			e := ends.Time.UTC()
			t.EndsAt = &e
		}
		out[id] = t
	}
	return out, rows.Err()
}
