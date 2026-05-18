package migrations

import (
	"context"
	"fmt"

	"github.com/Notifuse/notifuse/config"
	"github.com/Notifuse/notifuse/internal/domain"
)

// V32Migration adds the `users.veridian_managed` flag and backfills it for the
// per-tenant api_key users created by the Veridian Hub
// (`veridian-api-<tenant>@notifuse.*`). These users power the Hub→Notifuse
// magic-link flow — removing one via Team Settings silently breaks that
// tenant's "Open Notifuse" button. The flag is consumed by:
//   - `internal/service/workspace_service.go::RemoveMember` → returns 403
//   - `internal/service/workspace_service.go::GetWorkspaceMembersWithEmail` →
//     filters them out of the Team Settings listing
//   - `internal/service/veridian_service.go::Provision` → calls
//     `userRepo.MarkVeridianManaged` right after `CreateAPIKey`
//
// Backfill is narrow on purpose (`type = 'api_key' AND email LIKE
// 'veridian-api-%@notifuse.%'`) so a human user who happens to match the
// pattern is not locked out.
type V32Migration struct{}

func (m *V32Migration) GetMajorVersion() float64 {
	return 32.0
}

func (m *V32Migration) HasSystemUpdate() bool {
	return true
}

func (m *V32Migration) HasWorkspaceUpdate() bool {
	return false
}

func (m *V32Migration) ShouldRestartServer() bool {
	return false
}

func (m *V32Migration) UpdateSystem(ctx context.Context, _ *config.Config, db DBExecutor) error {
	if _, err := db.ExecContext(ctx, `
		ALTER TABLE users
		ADD COLUMN IF NOT EXISTS veridian_managed BOOLEAN NOT NULL DEFAULT FALSE
	`); err != nil {
		return fmt.Errorf("add users.veridian_managed: %w", err)
	}
	if _, err := db.ExecContext(ctx, `
		UPDATE users
		SET veridian_managed = TRUE
		WHERE type = 'api_key'
		  AND email LIKE 'veridian-api-%@notifuse.%'
		  AND veridian_managed = FALSE
	`); err != nil {
		return fmt.Errorf("backfill veridian_managed: %w", err)
	}
	return nil
}

func (m *V32Migration) UpdateWorkspace(_ context.Context, _ *config.Config, _ *domain.Workspace, _ DBExecutor) error {
	return nil
}

func init() {
	Register(&V32Migration{})
}
