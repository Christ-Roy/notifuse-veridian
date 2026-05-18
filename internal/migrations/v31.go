package migrations

import (
	"context"
	"fmt"

	"github.com/Notifuse/notifuse/config"
	"github.com/Notifuse/notifuse/internal/domain"
)

// V31Migration backfills `veridian_plan` for legacy workspaces that exist
// in `workspaces` but never got a plan row. Created 2026-05-18 after the
// AttachOwner repair revealed 9 prod tenants in this state — `GET
// /api/tenants/{id}/health` returned a phantom 404 because Health() inferred
// tenant existence from planRepo.Get.
//
// Companion fix on the service side (veridian_service.Health) now treats
// missing plan as legacy (no 404) and only 404s when the workspace itself is
// absent. This migration closes the gap so legacy tenants get an actual
// `free/active` plan row and become indistinguishable from new tenants.
//
// Idempotent: LEFT JOIN ... WHERE p.workspace_id IS NULL → only inserts the
// missing rows. Safe to re-run.
type V31Migration struct{}

func (m *V31Migration) GetMajorVersion() float64 {
	return 31.0
}

func (m *V31Migration) HasSystemUpdate() bool {
	return true
}

func (m *V31Migration) HasWorkspaceUpdate() bool {
	return false
}

func (m *V31Migration) ShouldRestartServer() bool {
	return false
}

func (m *V31Migration) UpdateSystem(ctx context.Context, _ *config.Config, db DBExecutor) error {
	_, err := db.ExecContext(ctx, `
		INSERT INTO veridian_plan (
			workspace_id, plan, status, monthly_email_quota, emails_sent_this_month,
			last_reset_at, created_at, updated_at
		)
		SELECT w.id, 'free', 'active', 500, 0, NOW(), NOW(), NOW()
		FROM workspaces w
		LEFT JOIN veridian_plan p ON p.workspace_id = w.id
		WHERE p.workspace_id IS NULL
	`)
	if err != nil {
		return fmt.Errorf("backfill veridian_plan for legacy workspaces: %w", err)
	}
	return nil
}

func (m *V31Migration) UpdateWorkspace(_ context.Context, _ *config.Config, _ *domain.Workspace, _ DBExecutor) error {
	return nil
}

func init() {
	Register(&V31Migration{})
}
