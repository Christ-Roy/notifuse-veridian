package repository

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/Notifuse/notifuse/internal/domain"
	"github.com/Notifuse/notifuse/internal/domain/mocks"
	"github.com/golang/mock/gomock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Vrai PostgreSQL jetable (VERIDIAN_TEST_POSTGRES_DSN), comme le reste de la file.
// Le SQL de pagination par curseur ne se prouve pas avec sqlmock.

func TestVeridianAutomationEnrollments_PaginatesWithCursorRealSQL(t *testing.T) {
	db := lot1TestDB(t)
	s := lot1Seed{db: db}
	s.automation(t, "autoE", "Sequence E")
	s.automation(t, "autoF", "Sequence F")
	// cinq inscrits, du plus recent au plus ancien : e1 (1h) ... e5 (5h)
	for i, email := range []string{"e1@x.fr", "e2@x.fr", "e3@x.fr", "e4@x.fr", "e5@x.fr"} {
		s.contactAutomation(t, "ce"+string(rune('1'+i)), "autoE", email, "n1", "active", time.Duration(i+1)*time.Hour)
	}
	// un inscrit d'une autre automation : ne doit jamais apparaitre
	s.contactAutomation(t, "cf1", "autoF", "f@x.fr", "n1", "active", 0)

	repo := NewVeridianAutomationEnrollmentRepositoryWithDB(db)
	ctx := context.Background()

	var seen []string
	cursor := ""
	pages := 0
	for {
		page, err := repo.ListEnrollmentsPage(ctx, domain.VeridianAutomationEnrollmentFilter{
			WorkspaceID: "ws", AutomationID: "autoE", Cursor: cursor, Limit: 2,
		}, nil)
		require.NoError(t, err)
		pages++
		require.LessOrEqual(t, pages, 5, "boucle de pagination infinie")
		for _, e := range page.Enrollments {
			seen = append(seen, e.ContactEmail)
		}
		if page.NextCursor == "" {
			break
		}
		cursor = page.NextCursor
	}
	assert.Equal(t, []string{"e1@x.fr", "e2@x.fr", "e3@x.fr", "e4@x.fr", "e5@x.fr"}, seen,
		"ordre entered_at DESC, sans doublon ni trou, sans la autre automation")
	assert.Equal(t, 3, pages, "5 lignes à 2 par page = 3 pages")
}

func TestVeridianAutomationEnrollments_StatusFilterRealSQL(t *testing.T) {
	db := lot1TestDB(t)
	s := lot1Seed{db: db}
	s.automation(t, "autoS", "Sequence S")
	s.contactAutomation(t, "s1", "autoS", "a@x.fr", "n1", "active", time.Hour)
	s.contactAutomation(t, "s2", "autoS", "b@x.fr", "n1", "sending", 2*time.Hour)
	s.contactAutomation(t, "s3", "autoS", "c@x.fr", "n1", "completed", 3*time.Hour)
	s.contactAutomation(t, "s4", "autoS", "d@x.fr", "n1", "exited", 4*time.Hour)
	s.contactAutomation(t, "s5", "autoS", "e@x.fr", "n1", "failed", 5*time.Hour)

	repo := NewVeridianAutomationEnrollmentRepositoryWithDB(db)
	ctx := context.Background()

	active, err := repo.ListEnrollmentsPage(ctx, domain.VeridianAutomationEnrollmentFilter{WorkspaceID: "ws", AutomationID: "autoS", Limit: 50},
		[]domain.ContactAutomationStatus{domain.ContactAutomationStatusActive, domain.ContactAutomationStatusSending})
	require.NoError(t, err)
	assert.Len(t, active.Enrollments, 2, "en cours = active + sending")

	failed, err := repo.ListEnrollmentsPage(ctx, domain.VeridianAutomationEnrollmentFilter{WorkspaceID: "ws", AutomationID: "autoS", Limit: 50},
		[]domain.ContactAutomationStatus{domain.ContactAutomationStatusFailed})
	require.NoError(t, err)
	require.Len(t, failed.Enrollments, 1)
	assert.Equal(t, "e@x.fr", failed.Enrollments[0].ContactEmail)
	assert.Equal(t, "n1", *failed.Enrollments[0].CurrentNodeID, "noeud courant renvoyé")
}

func TestNewVeridianAutomationEnrollmentRepository(t *testing.T) {
	// Chemin workspace : si la connexion du workspace est indisponible, l'erreur
	// remonte explicitement (jamais de panique, jamais de liste vide trompeuse).
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	wsRepo := mocks.NewMockWorkspaceRepository(ctrl)
	wsRepo.EXPECT().GetConnection(gomock.Any(), "ws").Return(nil, errors.New("base indisponible"))

	reader := NewVeridianAutomationEnrollmentRepository(wsRepo)
	require.NotNil(t, reader)
	_, err := reader.ListEnrollmentsPage(context.Background(), domain.VeridianAutomationEnrollmentFilter{WorkspaceID: "ws", AutomationID: "a", Limit: 1}, nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "base indisponible")
}
