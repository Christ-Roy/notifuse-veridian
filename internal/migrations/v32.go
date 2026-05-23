package migrations

import (
	"context"
	"fmt"

	"github.com/Notifuse/notifuse/config"
	"github.com/Notifuse/notifuse/internal/domain"
)

// V32Migration combines two additive ALTER TABLE on the system `users` table :
//
//  1. **Veridian patch** : `users.veridian_managed` BOOLEAN flag + backfill
//     for the per-tenant api_key users created by the Veridian Hub
//     (`veridian-api-<tenant>@notifuse.*`). These users power the
//     Hub→Notifuse magic-link flow — removing one via Team Settings silently
//     breaks that tenant's "Open Notifuse" button. The flag is consumed by:
//       - `internal/service/workspace_service.go::RemoveMember` → returns 403
//       - `internal/service/workspace_service.go::GetWorkspaceMembersWithEmail` →
//         filters them out of the Team Settings listing
//       - `internal/service/veridian_service.go::Provision` → calls
//         `userRepo.MarkVeridianManaged` right after `CreateAPIKey`
//     Backfill is narrow on purpose (`type = 'api_key' AND email LIKE
//     'veridian-api-%@notifuse.%'`) so a human user who happens to match the
//     pattern is not locked out.
//
//  2. **Upstream v32.0** : `users.language` VARCHAR(10) NOT NULL DEFAULT 'en'.
//     Drives both console UI locale and the language of system emails
//     (magic code, workspace invitation, circuit-breaker alert). Existing
//     rows default to 'en'.
//
// Both ALTER TABLE are idempotent (`IF NOT EXISTS`) — safe to re-run.
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
	// === Veridian patch === users.veridian_managed.
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

	// === Upstream v32.0 === users.language.
	if _, err := db.ExecContext(ctx, `
		ALTER TABLE users
		ADD COLUMN IF NOT EXISTS language VARCHAR(10) NOT NULL DEFAULT 'en'
	`); err != nil {
		return fmt.Errorf("add users.language: %w", err)
	}
	return nil
}

func (m *V32Migration) UpdateWorkspace(_ context.Context, _ *config.Config, _ *domain.Workspace, _ DBExecutor) error {
	return nil
}

func init() {
	Register(&V32Migration{})
}
