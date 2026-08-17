package migrations

import (
	"context"
	"regexp"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/Notifuse/notifuse/config"
	"github.com/Notifuse/notifuse/internal/domain"
	"github.com/stretchr/testify/require"
)

func TestV57MigrationRecompilesTimelineMetadataSegments(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()

	tree := `{"kind":"leaf","leaf":{"source":"contact_timeline","contact_timeline":{"kind":"purchase","count_operator":"at_least","count_value":1,"filters":[{"field_name":"product_id","field_type":"string","operator":"equals","string_values":["prod_123"]}]}}}`
	mock.ExpectQuery(regexp.QuoteMeta("SELECT id, tree FROM segments WHERE generated_sql LIKE '%ct.metadata->>''%'")).
		WillReturnRows(sqlmock.NewRows([]string{"id", "tree"}).AddRow("buyers", []byte(tree)))
	mock.ExpectExec(regexp.QuoteMeta("UPDATE segments SET generated_sql = $1, generated_args = $2 WHERE id = $3")).
		WithArgs(sqlmock.AnyArg(), sqlmock.AnyArg(), "buyers").
		WillReturnResult(sqlmock.NewResult(0, 1))

	err = (&V57Migration{}).UpdateWorkspace(context.Background(), &config.Config{}, &domain.Workspace{}, db)
	require.NoError(t, err)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestV57MigrationSkipsMalformedTrees(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()

	mock.ExpectQuery(regexp.QuoteMeta("SELECT id, tree FROM segments WHERE generated_sql LIKE '%ct.metadata->>''%'")).
		WillReturnRows(sqlmock.NewRows([]string{"id", "tree"}).AddRow("broken", []byte(`{"kind":`)))

	err = (&V57Migration{}).UpdateWorkspace(context.Background(), &config.Config{}, &domain.Workspace{}, db)
	require.NoError(t, err)
	require.NoError(t, mock.ExpectationsWereMet())
}
