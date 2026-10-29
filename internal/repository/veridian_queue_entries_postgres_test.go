package repository

import (
	"context"
	"testing"

	"github.com/Notifuse/notifuse/internal/domain"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Vrai PostgreSQL jetable (VERIDIAN_TEST_POSTGRES_DSN) : le SQL de la file et de
// l'annulation transactionnelle (suppression + journal) ne se prouve pas en mock.

func TestVeridianQueueEntries_ListFiltersAndPaginatesRealSQL(t *testing.T) {
	db := lot1TestDB(t)
	s := lot1Seed{db: db}
	s.automation(t, "autoQ", "Sequence Q")
	s.automation(t, "autoR", "Sequence R")
	s.queue(t, lot1Queue{id: "p1", sourceID: "autoQ", email: "p1@x.fr", class: "google"})
	s.queue(t, lot1Queue{id: "p2", sourceID: "autoQ", email: "p2@x.fr", class: "google"})
	s.queue(t, lot1Queue{id: "p3", sourceID: "autoQ", email: "p3@x.fr", class: "microsoft"})
	s.queue(t, lot1Queue{id: "p4", sourceID: "autoQ", email: "p4@x.fr", status: "paused", class: "google"})
	s.queue(t, lot1Queue{id: "r1", sourceID: "autoR", email: "r@x.fr", class: "google"})

	repo := NewVeridianQueueEntriesRepository(nil, db)
	ctx := context.Background()

	byClass, err := repo.ListEntries(ctx, domain.VeridianQueueEntriesFilter{WorkspaceID: "ws", AutomationID: "autoQ", Class: "google", Limit: 50})
	require.NoError(t, err)
	assert.Len(t, byClass.Entries, 3, "classe google : p1, p2, p4 (paused)")

	byStatus, err := repo.ListEntries(ctx, domain.VeridianQueueEntriesFilter{WorkspaceID: "ws", AutomationID: "autoQ", Status: "paused", Limit: 50})
	require.NoError(t, err)
	require.Len(t, byStatus.Entries, 1)
	assert.Equal(t, "p4", byStatus.Entries[0].ID)

	seen := []string{}
	cursor := ""
	for i := 0; i < 10; i++ {
		page, err := repo.ListEntries(ctx, domain.VeridianQueueEntriesFilter{WorkspaceID: "ws", AutomationID: "autoQ", Cursor: cursor, Limit: 2})
		require.NoError(t, err)
		for _, e := range page.Entries {
			seen = append(seen, e.ID)
		}
		if page.NextCursor == "" {
			break
		}
		cursor = page.NextCursor
	}
	assert.Len(t, seen, 4, "4 entrees de autoQ, sans doublon ni trou, sans celles de autoR")
	assert.NotContains(t, seen, "r1")
}

func TestVeridianQueueEntries_CancelDryRunChangesNothingRealSQL(t *testing.T) {
	db := lot1TestDB(t)
	s := lot1Seed{db: db}
	s.automation(t, "autoD", "Sequence D")
	s.queue(t, lot1Queue{id: "d1", sourceID: "autoD", email: "d1@x.fr"})
	s.queue(t, lot1Queue{id: "d2", sourceID: "autoD", email: "d2@x.fr", status: "paused"})
	repo := NewVeridianQueueEntriesRepository(nil, db)
	ctx := context.Background()

	res, err := repo.CancelEntries(ctx, domain.VeridianQueueCancelRequest{WorkspaceID: "ws", AutomationID: "autoD", Reason: "essai", Limit: 10, DryRun: true}, "robert@x.fr")
	require.NoError(t, err)
	assert.True(t, res.DryRun)
	assert.Equal(t, 2, res.Matched)
	assert.Equal(t, 0, res.Cancelled)

	var n int
	require.NoError(t, db.QueryRow(`SELECT COUNT(*) FROM email_queue WHERE source_id='autoD'`).Scan(&n))
	assert.Equal(t, 2, n, "le dry-run ne supprime rien")
}

func TestVeridianQueueEntries_CancelDeletesJournalsAndSkipsProcessingRealSQL(t *testing.T) {
	db := lot1TestDB(t)
	s := lot1Seed{db: db}
	s.automation(t, "autoC", "Sequence C")
	s.queue(t, lot1Queue{id: "c1", sourceID: "autoC", email: "c1@x.fr"})
	s.queue(t, lot1Queue{id: "c2", sourceID: "autoC", email: "c2@x.fr", status: "failed"})
	s.queue(t, lot1Queue{id: "c3", sourceID: "autoC", email: "c3@x.fr", status: "processing"})
	repo := NewVeridianQueueEntriesRepository(nil, db)
	ctx := context.Background()

	// Un lot borné par identifiants, dont une entrée en vol : elle n'est pas annulée.
	res, err := repo.CancelEntries(ctx, domain.VeridianQueueCancelRequest{WorkspaceID: "ws", EntryIDs: []string{"c1", "c3"}, Reason: "retrait client", Limit: 10}, "robert@x.fr")
	require.NoError(t, err)
	assert.Equal(t, 1, res.Cancelled, "seule c1 est annulable (c3 est en cours d'envoi)")
	require.Len(t, res.Entries, 1)
	assert.Equal(t, "c1", res.Entries[0].ID)

	var remaining int
	require.NoError(t, db.QueryRow(`SELECT COUNT(*) FROM email_queue WHERE source_id='autoC'`).Scan(&remaining))
	assert.Equal(t, 2, remaining, "c1 supprimee, c2 et c3 restent")

	// Journal : une ligne « cancelled » par entree retiree, avec l'acteur et le motif.
	var outcome, reason, detail string
	require.NoError(t, db.QueryRow(`SELECT outcome, reason, detail FROM veridian_send_decisions WHERE entry_id='c1'`).Scan(&outcome, &reason, &detail))
	assert.Equal(t, "cancelled", outcome)
	assert.Equal(t, "operator_cancel", reason)
	assert.Contains(t, detail, "robert@x.fr")
	assert.Contains(t, detail, "retrait client")

	// La lecture du journal (decisions) retrouve l'annulation.
	var journaled int
	require.NoError(t, db.QueryRow(`SELECT COUNT(*) FROM veridian_send_decisions WHERE entry_id='c3'`).Scan(&journaled))
	assert.Equal(t, 0, journaled, "rien n'est journalise pour une entree non annulee")

	// Lot par automation, borne : Matched dit qu'il reste des entrees.
	res, err = repo.CancelEntries(ctx, domain.VeridianQueueCancelRequest{WorkspaceID: "ws", AutomationID: "autoC", Reason: "lot", Limit: 1}, "robert@x.fr")
	require.NoError(t, err)
	assert.Equal(t, 1, res.Matched)
	assert.Equal(t, 1, res.Cancelled)
}

// L3 : la liste de la file porte la priorité de chaque entrée.
func TestVeridianQueueEntries_ListCarriesThePriority(t *testing.T) {
	db := lot1TestDB(t)
	s := lot1Seed{db: db}
	s.automation(t, "autoP", "Sequence P")
	s.queue(t, lot1Queue{id: "pa", sourceID: "autoP", email: "pa@x.fr", priority: 2})
	s.queue(t, lot1Queue{id: "pb", sourceID: "autoP", email: "pb@x.fr"})
	repo := NewVeridianQueueEntriesRepository(nil, db)
	page, err := repo.ListEntries(context.Background(), domain.VeridianQueueEntriesFilter{WorkspaceID: "ws", AutomationID: "autoP", Limit: 50})
	require.NoError(t, err)
	got := map[string]int{}
	for _, e := range page.Entries {
		got[e.ID] = e.Priority
	}
	assert.Equal(t, map[string]int{"pa": 2, "pb": 5}, got)
}
