package migrations

import (
	"context"
	"fmt"

	"github.com/Notifuse/notifuse/config"
	"github.com/Notifuse/notifuse/internal/domain"
)

// V56Migration attributes successful sends to the exact email integration.
// It is expand-only: older binaries ignore the nullable column and index.
type V56Migration struct{}

func (m *V56Migration) GetMajorVersion() float64  { return 56.0 }
func (m *V56Migration) HasSystemUpdate() bool     { return false }
func (m *V56Migration) HasWorkspaceUpdate() bool  { return true }
func (m *V56Migration) ShouldRestartServer() bool { return false }

func (m *V56Migration) UpdateSystem(_ context.Context, _ *config.Config, _ DBExecutor) error {
	return nil
}

func (m *V56Migration) UpdateWorkspace(ctx context.Context, _ *config.Config, _ *domain.Workspace, db DBExecutor) error {
	if _, err := db.ExecContext(ctx, `ALTER TABLE message_history ADD COLUMN IF NOT EXISTS veridian_profile_id VARCHAR(255)`); err != nil {
		return fmt.Errorf("add message history profile attribution: %w", err)
	}
	if _, err := db.ExecContext(ctx, `CREATE INDEX IF NOT EXISTS idx_message_history_profile_sent_at ON message_history(veridian_profile_id, sent_at) WHERE veridian_profile_id IS NOT NULL AND failed_at IS NULL`); err != nil {
		return fmt.Errorf("create message history profile usage index: %w", err)
	}
	return nil
}

func init() { Register(&V56Migration{}) }
