package policy

import (
	"database/sql"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/Notifuse/notifuse/internal/domain"
	"github.com/Notifuse/notifuse/internal/service"
	"github.com/Notifuse/notifuse/internal/veridian/platform"
	"github.com/Notifuse/notifuse/pkg/logger"
)

// fakeHost n'implémente que ce que wire() lit avant de vérifier ses dépendances.
type fakeHost struct {
	platform.Host
	db *sql.DB
}

func (f fakeHost) DB() *sql.DB                                     { return f.db }
func (f fakeHost) Logger() logger.Logger                           { return noopLog{} }
func (f fakeHost) WorkspaceRepository() domain.WorkspaceRepository { return nil }
func (f fakeHost) AuthService() *service.AuthService               { return nil }

func TestWire_SansDependancesEchoueVitePartoutSaufAssemblageDeTest(t *testing.T) {
	// Assemblage de test (pas de base système) : la capacité reste inactive sans erreur.
	assert.NoError(t, wire(fakeHost{}))

	// Application réelle (base système présente) : une dépendance absente est un défaut d'assemblage.
	db, _, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()
	err = wire(fakeHost{db: db})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "absent")
}

func TestCapabilityEstEnregistree(t *testing.T) {
	names := []string{}
	for _, c := range platform.Default.List() {
		names = append(names, c.Name)
	}
	assert.Contains(t, names, "policy")
}
