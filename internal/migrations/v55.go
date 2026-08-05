package migrations

import (
	"context"
	"fmt"

	"github.com/Notifuse/notifuse/config"
	"github.com/Notifuse/notifuse/internal/domain"
)

// V55Migration makes cold-outreach daily provider and warmup caps atomic.
// Everything is additive and nullable/backward-compatible: V54 binaries ignore
// the new column and tables during an expand-first rolling deployment.
type V55Migration struct{}

func (m *V55Migration) GetMajorVersion() float64  { return 55.0 }
func (m *V55Migration) HasSystemUpdate() bool     { return false }
func (m *V55Migration) HasWorkspaceUpdate() bool  { return true }
func (m *V55Migration) ShouldRestartServer() bool { return false }

func (m *V55Migration) UpdateSystem(_ context.Context, _ *config.Config, _ DBExecutor) error {
	return nil
}

func (m *V55Migration) UpdateWorkspace(ctx context.Context, _ *config.Config, _ *domain.Workspace, db DBExecutor) error {
	statements := []struct {
		name string
		sql  string
	}{
		{"message provider class", `ALTER TABLE message_history ADD COLUMN IF NOT EXISTS veridian_provider_class VARCHAR(64)`},
		{"daily quota counters", `
			CREATE TABLE IF NOT EXISTS veridian_daily_quota_counters (
				workspace_id VARCHAR(255) NOT NULL,
				quota_day DATE NOT NULL,
				quota_kind VARCHAR(32) NOT NULL,
				sender_domain VARCHAR(255) NOT NULL,
				provider_class VARCHAR(64) NOT NULL DEFAULT '',
				used INTEGER NOT NULL DEFAULT 0 CHECK (used >= 0),
				created_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW(),
				updated_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW(),
				PRIMARY KEY (workspace_id, quota_day, quota_kind, sender_domain, provider_class)
			)
		`},
		{"daily quota reservations", `
			CREATE TABLE IF NOT EXISTS veridian_daily_quota_reservations (
				workspace_id VARCHAR(255) NOT NULL,
				message_id VARCHAR(255) NOT NULL,
				quota_kind VARCHAR(32) NOT NULL,
				quota_day DATE NOT NULL,
				sender_domain VARCHAR(255) NOT NULL,
				provider_class VARCHAR(64) NOT NULL DEFAULT '',
				created_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW(),
				PRIMARY KEY (workspace_id, message_id, quota_kind)
			)
		`},
		{"provider class history index", `CREATE INDEX IF NOT EXISTS idx_message_history_provider_class_sender_sent_at ON message_history(veridian_provider_class, veridian_sender_email, sent_at) WHERE veridian_provider_class IS NOT NULL AND failed_at IS NULL`},
	}

	for _, statement := range statements {
		if _, err := db.ExecContext(ctx, statement.sql); err != nil {
			return fmt.Errorf("create V55 %s: %w", statement.name, err)
		}
	}
	return nil
}

func init() { Register(&V55Migration{}) }
