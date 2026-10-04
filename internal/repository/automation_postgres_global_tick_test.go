package repository

import (
	"context"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/Notifuse/notifuse/internal/domain"
	"github.com/Notifuse/notifuse/internal/domain/mocks"
	"github.com/Notifuse/notifuse/internal/service"
	"github.com/golang/mock/gomock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Veridian fork — durcissement 2026-10-04 (audit sécurité, "budget de
// connexions"). AVANT ce correctif, GetScheduledContactAutomationsGlobal
// ouvrait une connexion par tenant à CHAQUE appel (donc à chaque tick du
// scheduler), pour TOUS les workspaces, dans le même budget partagé
// maxConnections que les requêtes API — un parc de tenants nombreux pouvait
// épuiser ce budget à lui seul. Les tests ci-dessous prouvent la fenêtre
// round-robin bornée, à deux niveaux : la logique pure (roundRobinWindow /
// selectGlobalScheduleBatch) et le comportement de bout en bout (nombre
// RÉEL de requêtes SQL émises par appel).

func makeTestWorkspaces(n int) []*domain.Workspace {
	ws := make([]*domain.Workspace, n)
	for i := 0; i < n; i++ {
		ws[i] = &domain.Workspace{ID: "ws" + string(rune('A'+i))}
	}
	return ws
}

func TestRoundRobinWindow(t *testing.T) {
	ws := makeTestWorkspaces(5) // wsA..wsE

	t.Run("window smaller than total wraps correctly", func(t *testing.T) {
		got := roundRobinWindow(ws, 3, 3) // offset 3, size 3 -> D, E, A
		require.Len(t, got, 3)
		assert.Equal(t, []string{"wsD", "wsE", "wsA"}, []string{got[0].ID, got[1].ID, got[2].ID})
	})

	t.Run("window covering everything returns all, no wrap duplication", func(t *testing.T) {
		got := roundRobinWindow(ws, 0, 5)
		require.Len(t, got, 5)
	})

	t.Run("n larger than total is capped to total", func(t *testing.T) {
		got := roundRobinWindow(ws, 0, 50)
		assert.Len(t, got, 5)
	})

	t.Run("n zero or negative is treated as total", func(t *testing.T) {
		got := roundRobinWindow(ws, 0, 0)
		assert.Len(t, got, 5)
	})
}

func TestDefaultMaxWorkspacesPerGlobalTick(t *testing.T) {
	cases := []struct {
		name                string
		maxConnections      int
		maxConnectionsPerDB int
		want                int
	}{
		{"typical prod-ish config (100/10 -> 10 slots, quarter=2)", 100, 10, 2},
		{"large fleet (400/4 -> 100 slots, quarter=25)", 400, 4, 25},
		{"tiny budget still reserves at least 1", 10, 10, 1},
		{"maxConnectionsPerDB zero -> no limit computed", 100, 0, 0},
		{"maxConnectionsPerDB negative -> no limit computed", 100, -1, 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := DefaultMaxWorkspacesPerGlobalTick(tc.maxConnections, tc.maxConnectionsPerDB)
			assert.Equal(t, tc.want, got)
		})
	}
}

func TestAutomationRepository_SelectGlobalScheduleBatch_RotatesAcrossCalls(t *testing.T) {
	ws := makeTestWorkspaces(10)
	repo := &AutomationRepository{maxWorkspacesPerGlobalTick: 3}

	seen := make(map[string]int)
	for tick := 0; tick < 10; tick++ {
		batch := repo.selectGlobalScheduleBatch(ws)
		assert.Len(t, batch, 3, "each tick must touch at most the configured cap")
		for _, w := range batch {
			seen[w.ID]++
		}
	}

	// Over enough ticks, EVERY workspace must have been covered at least
	// once (fairness is preserved across time, not violated).
	for _, w := range ws {
		assert.GreaterOrEqual(t, seen[w.ID], 1, "workspace %s was never scheduled across 10 ticks", w.ID)
	}
}

func TestAutomationRepository_SelectGlobalScheduleBatch_ZeroLimitMeansUnbounded(t *testing.T) {
	ws := makeTestWorkspaces(10)
	repo := &AutomationRepository{maxWorkspacesPerGlobalTick: 0}

	batch := repo.selectGlobalScheduleBatch(ws)
	assert.Len(t, batch, 10, "0 (unset) must preserve the pre-fix behavior: every workspace, every call")
}

func TestAutomationRepository_SelectGlobalScheduleBatch_LimitAboveTotalMeansUnbounded(t *testing.T) {
	ws := makeTestWorkspaces(3)
	repo := &AutomationRepository{maxWorkspacesPerGlobalTick: 50}

	batch := repo.selectGlobalScheduleBatch(ws)
	assert.Len(t, batch, 3)
}

// TestGetScheduledContactAutomationsGlobal_BoundedConnectionFootprint is the
// end-to-end proof: with 10 workspaces and a cap of 3, a SINGLE call must
// issue EXACTLY 3 SQL queries (one get-db / one query per touched
// workspace) — not 10. sqlmock enforces this: a 4th unexpected query would
// fail the test. A second call (simulating the next scheduler tick) must
// then touch the NEXT 3 (round-robin), proving coverage rotates rather than
// always hammering the same few tenants.
func TestGetScheduledContactAutomationsGlobal_BoundedConnectionFootprint(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()

	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	mockWSRepo := mocks.NewMockWorkspaceRepository(ctrl)

	ws := makeTestWorkspaces(10)
	mockWSRepo.EXPECT().List(gomock.Any()).Return(ws, nil).Times(2)

	qb := service.NewQueryBuilder()
	triggerGen := service.NewAutomationTriggerGenerator(qb)
	repo := NewAutomationRepositoryWithDB(db, triggerGen).(*AutomationRepository)
	repo.workspaceRepo = mockWSRepo
	repo.maxWorkspacesPerGlobalTick = 3

	emptyRows := func() *sqlmock.Rows {
		return sqlmock.NewRows([]string{
			"id", "automation_id", "contact_email", "current_node_id", "status",
			"exit_reason", "entered_at", "scheduled_at", "context", "retry_count", "last_error",
			"last_retry_at", "max_retries",
		})
	}

	// First tick: expect EXACTLY 3 queries (not 10).
	for i := 0; i < 3; i++ {
		mock.ExpectQuery("SELECT ca.* FROM contact_automations ca JOIN automations a").
			WillReturnRows(emptyRows())
	}

	ctx := context.Background()
	_, err = repo.GetScheduledContactAutomationsGlobal(ctx, time.Now().UTC(), 100)
	require.NoError(t, err)
	require.NoError(t, mock.ExpectationsWereMet(), "exactly 3 queries expected for a cap of 3 — a 4th would mean the bound was not enforced")

	// Second tick: the cursor must have rotated to the NEXT 3 workspaces,
	// not the same 3 again (coverage over time, not starvation of the
	// tail of the workspace list).
	for i := 0; i < 3; i++ {
		mock.ExpectQuery("SELECT ca.* FROM contact_automations ca JOIN automations a").
			WillReturnRows(emptyRows())
	}
	_, err = repo.GetScheduledContactAutomationsGlobal(ctx, time.Now().UTC(), 100)
	require.NoError(t, err)
	require.NoError(t, mock.ExpectationsWereMet())

	assert.Equal(t, 6, repo.globalScheduleCursor, "after two ticks of window 3, the cursor should have advanced to offset 6")
}
