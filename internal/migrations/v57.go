package migrations

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/Notifuse/notifuse/config"
	"github.com/Notifuse/notifuse/internal/domain"
	"github.com/Notifuse/notifuse/internal/service"
)

// V57Migration recompiles stored segment SQL that interpolated a
// contact_timeline metadata key. New queries bind that key as an argument, but
// existing segments keep their generated SQL until they are rebuilt.
type V57Migration struct{}

func (m *V57Migration) GetMajorVersion() float64  { return 57.0 }
func (m *V57Migration) HasSystemUpdate() bool     { return false }
func (m *V57Migration) HasWorkspaceUpdate() bool  { return true }
func (m *V57Migration) ShouldRestartServer() bool { return false }

func (m *V57Migration) UpdateSystem(_ context.Context, _ *config.Config, _ DBExecutor) error {
	return nil
}

func (m *V57Migration) UpdateWorkspace(ctx context.Context, _ *config.Config, _ *domain.Workspace, db DBExecutor) error {
	rows, err := db.QueryContext(ctx, `
		SELECT id, tree FROM segments
		WHERE generated_sql LIKE '%ct.metadata->>''%'
	`)
	if err != nil {
		return fmt.Errorf("v57: query vulnerable segments: %w", err)
	}

	type segmentTree struct {
		id   string
		tree domain.TreeNode
	}
	var pending []segmentTree
	for rows.Next() {
		var id string
		var treeJSON []byte
		if scanErr := rows.Scan(&id, &treeJSON); scanErr != nil {
			_ = rows.Close()
			return fmt.Errorf("v57: scan segment: %w", scanErr)
		}
		var tree domain.TreeNode
		if json.Unmarshal(treeJSON, &tree) != nil {
			continue
		}
		pending = append(pending, segmentTree{id: id, tree: tree})
	}
	if iterErr := rows.Err(); iterErr != nil {
		_ = rows.Close()
		return fmt.Errorf("v57: iterate segments: %w", iterErr)
	}
	if closeErr := rows.Close(); closeErr != nil {
		return fmt.Errorf("v57: close segment rows: %w", closeErr)
	}

	queryBuilder := service.NewQueryBuilder()
	for _, item := range pending {
		sqlQuery, args, buildErr := queryBuilder.BuildSQL(&item.tree)
		if buildErr != nil {
			continue
		}
		argsJSON, marshalErr := json.Marshal(args)
		if marshalErr != nil {
			continue
		}
		if _, execErr := db.ExecContext(ctx, `
			UPDATE segments SET generated_sql = $1, generated_args = $2
			WHERE id = $3
		`, sqlQuery, argsJSON, item.id); execErr != nil {
			return fmt.Errorf("v57: update segment %s: %w", item.id, execErr)
		}
	}

	return nil
}

func init() { Register(&V57Migration{}) }
