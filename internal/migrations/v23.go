package migrations

import (
	"context"
	"fmt"

	"github.com/Notifuse/notifuse/config"
	"github.com/Notifuse/notifuse/internal/domain"
)

// V23Migration adds durable, system-wide safety state for opt-in cold outreach.
// The reservation ledger is conservative by design: an attempted provider call
// consumes quota even if its final delivery outcome is unknown.
type V23Migration struct{}

func (m *V23Migration) GetMajorVersion() float64  { return 23.0 }
func (m *V23Migration) HasSystemUpdate() bool     { return true }
func (m *V23Migration) HasWorkspaceUpdate() bool  { return false }
func (m *V23Migration) ShouldRestartServer() bool { return false }

func (m *V23Migration) UpdateSystem(ctx context.Context, cfg *config.Config, db DBExecutor) error {
	statements := []struct {
		name string
		sql  string
	}{
		{
			name: "global suppressions",
			sql: `CREATE TABLE IF NOT EXISTS veridian_global_suppressions (
				email_sha256 CHAR(64) PRIMARY KEY,
				status VARCHAR(20) NOT NULL CHECK (status IN ('unsubscribed', 'bounced', 'complained')),
				source_workspace_id VARCHAR(32) NOT NULL,
				first_observed_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT CURRENT_TIMESTAMP,
				last_observed_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT CURRENT_TIMESTAMP
			)`,
		},
		{
			name: "send reservations",
			sql: `CREATE TABLE IF NOT EXISTS veridian_send_reservations (
				id BIGSERIAL PRIMARY KEY,
				workspace_id VARCHAR(32) NOT NULL,
				queue_entry_id VARCHAR(255) NOT NULL,
				message_id VARCHAR(255) NOT NULL,
				attempt INTEGER NOT NULL CHECK (attempt > 0),
				sender_email VARCHAR(255) NOT NULL,
				provider_class VARCHAR(64) NOT NULL,
				recipient_domain VARCHAR(255) NOT NULL,
				recipient_sha256 CHAR(64) NOT NULL,
				quota_date DATE NOT NULL,
				reserved_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT CURRENT_TIMESTAMP,
				UNIQUE (workspace_id, queue_entry_id, attempt)
			)`,
		},
		{
			name: "reservation workspace date index",
			sql: `CREATE INDEX IF NOT EXISTS idx_veridian_send_reservations_workspace_date
				ON veridian_send_reservations (workspace_id, quota_date)`,
		},
		{
			name: "reservation provider rate index",
			sql: `CREATE INDEX IF NOT EXISTS idx_veridian_send_reservations_provider_rate
				ON veridian_send_reservations (workspace_id, provider_class, reserved_at DESC)`,
		},
	}
	for _, statement := range statements {
		if _, err := db.ExecContext(ctx, statement.sql); err != nil {
			return fmt.Errorf("failed to create %s: %w", statement.name, err)
		}
	}
	return nil
}

func (m *V23Migration) UpdateWorkspace(ctx context.Context, cfg *config.Config, workspace *domain.Workspace, db DBExecutor) error {
	return nil
}

func init() { Register(&V23Migration{}) }
