package app

import (
	"context"
	"errors"
	"net/http"
	"sync"
	"testing"
	"time"

	"github.com/Notifuse/notifuse/config"
	veridianmig "github.com/Notifuse/notifuse/internal/migrations/veridian"
	"github.com/Notifuse/notifuse/internal/veridian/platform"
	"github.com/Notifuse/notifuse/pkg/logger"
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
