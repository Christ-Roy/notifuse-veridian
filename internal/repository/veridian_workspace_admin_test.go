package repository

import (
	"context"
	"database/sql"
	"errors"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/Notifuse/notifuse/internal/domain"
)

func TestVeridianAdminGranted_Withdrawn(t *testing.T) {
	cases := []struct {
		name               string
		row                []bool // en vigueur, rôle tenu ; nil = jamais désigné
		err                error
		granted, withdrawn bool
	}{
		{"jamais désigné", nil, nil, false, false},
		{"désigné et en place", []bool{true, true}, nil, true, false},
		{"retiré ou rétrogradé", []bool{true, false}, nil, false, true},
		{"révoqué, encore propriétaire", []bool{false, true}, nil, false, true},
		{"révoqué et parti", []bool{false, false}, nil, false, true},
		{"base en panne", nil, errors.New("panne"), false, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			db, mock, err := sqlmock.New(sqlmock.QueryMatcherOption(sqlmock.QueryMatcherEqual))
			require.NoError(t, err)
			defer func() { _ = db.Close() }()
			var ledger domain.VeridianAdminLedger = &workspaceRepository{systemDB: db}
			for i := 0; i < 2; i++ {
				q := mock.ExpectQuery(veridianAdminStateSQL).WithArgs("ws", "u")
				switch {
				case c.err != nil:
					q.WillReturnError(c.err)
				case c.row == nil:
					q.WillReturnError(sql.ErrNoRows)
				default:
					q.WillReturnRows(sqlmock.NewRows([]string{"a", "b"}).AddRow(c.row[0], c.row[1]))
				}
			}
			granted, err := ledger.VeridianAdminGranted(context.Background(), "ws", "u")
			assert.ErrorIs(t, err, c.err)
			withdrawn, err := ledger.VeridianAdminWithdrawn(context.Background(), "ws", "u")
			assert.ErrorIs(t, err, c.err)
			assert.Equal(t, c.granted, granted)
			assert.Equal(t, c.withdrawn, withdrawn)
			assert.NoError(t, mock.ExpectationsWereMet())
		})
	}
}

// La requête ne lit qu'une désignation et le rôle tenu : une personne jamais désignée n'est ni désignée ni retirée, sans erreur.
func TestVeridianAdminWithdrawn_JamaisDesigne(t *testing.T) {
	db, mock := newMockSystemDB(t)
	mock.ExpectQuery(veridianAdminStateSQL).WithArgs("ws", "u").WillReturnError(sql.ErrNoRows)
	withdrawn, err := (&workspaceRepository{systemDB: db}).VeridianAdminWithdrawn(context.Background(), "ws", "u")
	require.NoError(t, err)
	assert.False(t, withdrawn)
}
