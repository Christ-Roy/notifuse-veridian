package module

import (
	"database/sql"
	"strings"
	"testing"
	"time"

	"github.com/Notifuse/notifuse/config"
	"github.com/Notifuse/notifuse/internal/veridian/accountgate"
	"github.com/Notifuse/notifuse/internal/veridian/platform"
	"github.com/Notifuse/notifuse/pkg/logger"
)

type host struct {
	platform.Host
	cfg *config.Config
}

func (h host) Config() *config.Config { return h.cfg }
func (h host) DB() *sql.DB            { return nil }
func (h host) Logger() logger.Logger  { return logger.NewLoggerWithLevel("disabled") }

func TestWire_RefusesToStartWithoutAReadableDate(t *testing.T) {
	t.Cleanup(func() { accountgate.SetSince(time.Time{}) })
	prod := &config.Config{Environment: "production", HubAPISecret: "x"}
	for name, c := range map[string]struct {
		cfg   *config.Config
		value string
		fails bool
	}{
		"prod sans date":           {prod, "", true},
		"prod illisible":           {prod, "demain", true},
		"prod datée":               {prod, "2026-11-25T00:00:00Z", false},
		"staging illisible":        {&config.Config{Environment: "staging", HubAPISecret: "x"}, "off", true},
		"staging sans date":        {&config.Config{Environment: "staging", HubAPISecret: "x"}, "", false},
		"prod sans Hub, sans date": {&config.Config{Environment: "production"}, "", false},
	} {
		t.Setenv(accountgate.SinceVar, c.value)
		err := wire(host{cfg: c.cfg})
		if (err != nil) != c.fails || (err != nil && !strings.Contains(err.Error(), accountgate.SinceVar)) {
			t.Errorf("%s : %v", name, err)
		}
	}
	t.Setenv(accountgate.SinceVar, "2026-11-25T00:00:00Z")
	if err := wire(host{cfg: prod}); err != nil || accountgate.Since().Format(time.RFC3339) != "2026-11-25T00:00:00Z" {
		t.Fatal(err, accountgate.Since())
	}
}

func TestCapabilityIsRegistered(t *testing.T) {
	for _, c := range platform.Default.List() {
		if c.Name == "accountgate" && c.Wire != nil {
			return
		}
	}
	t.Fatal("la capacité accountgate n'est pas enregistrée")
}
