package app

import (
	"context"
	"errors"
	"net/http"
	"sync"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/Notifuse/notifuse/config"
	domainmocks "github.com/Notifuse/notifuse/internal/domain/mocks"
	veridianmig "github.com/Notifuse/notifuse/internal/migrations/veridian"
	"github.com/Notifuse/notifuse/internal/veridian/platform"
	"github.com/Notifuse/notifuse/pkg/logger"
	"github.com/golang/mock/gomock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newWiringApp() *App {
	ctx, cancel := context.WithCancel(context.Background())
	return &App{
		config:         &config.Config{},
		logger:         logger.NewLoggerWithLevel("error"),
		mux:            http.NewServeMux(),
		shutdownCtx:    ctx,
		shutdownCancel: cancel,
	}
}

func TestWireVeridian_NoCapabilityNoMigrationIsNoop(t *testing.T) {
	a := newWiringApp()
	require.NoError(t, wireVeridian(a, veridianmig.NewRegistry(), platform.NewRegistry()))
}

func TestWireVeridian_WiresInNameOrderWithHost(t *testing.T) {
	a := newWiringApp()
	caps := platform.NewRegistry()
	var order []string
	for _, n := range []string{"b", "a", "c"} {
		n := n
		require.NoError(t, caps.Register(platform.Capability{Name: n, Wire: func(h platform.Host) error {
			order = append(order, n)
			assert.Same(t, a.config, h.Config())
			assert.Same(t, a.mux, h.Mux())
			h.Mux().HandleFunc("/veridian-test/"+n, func(http.ResponseWriter, *http.Request) {})
			return nil
		}}))
	}
	require.NoError(t, wireVeridian(a, veridianmig.NewRegistry(), caps))
	assert.Equal(t, []string{"a", "b", "c"}, order)
}

func TestWireVeridian_WireErrorAndPanicAreFatal(t *testing.T) {
	for name, wire := range map[string]func(platform.Host) error{
		"error": func(platform.Host) error { return errors.New("boom") },
		"panic": func(platform.Host) error { panic("kaboom") },
	} {
		caps := platform.NewRegistry()
		require.NoError(t, caps.Register(platform.Capability{Name: "bad", Wire: wire}))
		err := wireVeridian(newWiringApp(), veridianmig.NewRegistry(), caps)
		require.Error(t, err, name)
		assert.Contains(t, err.Error(), `"bad"`, name)
	}
}

func TestWireVeridian_StartAfterServerCreatedStopOnShutdown(t *testing.T) {
	a := newWiringApp()
	var mu sync.Mutex
	started, stopped := false, false
	caps := platform.NewRegistry()
	require.NoError(t, caps.Register(platform.Capability{
		Name: "loop",
		Wire: func(platform.Host) error { return nil },
		Start: func(ctx context.Context, _ platform.Host) error {
			mu.Lock()
			started = true
			mu.Unlock()
			return nil
		},
		Stop: func(context.Context) error { mu.Lock(); stopped = true; mu.Unlock(); return nil },
	}))
	require.NoError(t, wireVeridian(a, veridianmig.NewRegistry(), caps))

	time.Sleep(500 * time.Millisecond)
	mu.Lock()
	assert.False(t, started, "Start ne doit pas tourner avant la création du serveur")
	mu.Unlock()

	a.serverMu.Lock()
	a.server = &http.Server{}
	a.serverMu.Unlock()
	require.Eventually(t, func() bool { mu.Lock(); defer mu.Unlock(); return started }, 3*time.Second, 50*time.Millisecond)

	a.shutdownCancel()
	require.Eventually(t, func() bool { mu.Lock(); defer mu.Unlock(); return stopped }, 3*time.Second, 50*time.Millisecond)
}

func TestVeridianHost_JWTSecretFailsWhenUnset(t *testing.T) {
	a := newWiringApp()
	h := &veridianHost{a: a}
	_, err := h.JWTSecret()()
	require.Error(t, err)
	a.config.Security.JWTSecret = []byte("0123456789abcdef0123456789abcdef")
	s, err := h.JWTSecret()()
	require.NoError(t, err)
	assert.Equal(t, a.config.Security.JWTSecret, s)
}

// L0 jalon 2 : veridianInitRepositories construit les dépôts Veridian et décore l'historique de messages
// (le décorateur incrémente le quota du plan ; sans lui le paywall ne bloque jamais, bug du 20/05/2026).
func TestVeridianInitRepositories_BuildsReposAndDecoratesMessageHistory(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	db, _, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()

	a := newWiringApp()
	a.db = db
	original := domainmocks.NewMockMessageHistoryRepository(ctrl)
	a.messageHistoryRepo = original

	a.veridianInitRepositories()

	assert.NotNil(t, a.veridianPlanRepo)
	assert.NotNil(t, a.veridianIdempotencyRepo)
	assert.NotNil(t, a.veridianAPIKeyGraceRepo)
	assert.NotNil(t, a.agentInstallTokenRepo)
	assert.NotNil(t, a.veridianFrozenMemberRepo)
	assert.NotNil(t, a.veridianIMAPUIDSeenRepo)
	assert.NotNil(t, a.veridianContactReplyRepo)
	assert.NotSame(t, original, a.messageHistoryRepo, "l'historique doit être décoré (quota)")
}

// Une migration enregistrée ne doit pas faire paniquer un assemblage sans base système (tests d'assemblage).
func TestWireVeridian_RegisteredMigrationWithoutDBDoesNotPanic(t *testing.T) {
	migs := veridianmig.NewRegistry()
	require.NoError(t, migs.Register(veridianmig.Spec{Number: 63, Lot: "L1", Name: "t", Workspace: []string{"CREATE TABLE IF NOT EXISTS t (id INT)"}}))
	a := newWiringApp()
	a.db = nil
	require.NoError(t, wireVeridian(a, migs, platform.NewRegistry()))
}

// WithMockDB : les tests d'assemblage (sqlmock sans attente) ne doivent exécuter aucune requête de migration.
func TestWireVeridian_MockDBRunsNoMigrationQuery(t *testing.T) {
	migs := veridianmig.NewRegistry()
	require.NoError(t, migs.Register(veridianmig.Spec{Number: 63, Lot: "L1", Name: "t", Workspace: []string{"CREATE TABLE IF NOT EXISTS t (id INT)"}}))
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()
	a := newWiringApp()
	WithMockDB(db)(a)
	require.True(t, a.veridianMockDB)
	require.NoError(t, wireVeridian(a, migs, platform.NewRegistry()))
	assert.NoError(t, mock.ExpectationsWereMet())
}
