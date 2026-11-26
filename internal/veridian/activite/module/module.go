// Package module branche l'activité Mail vers le Hub (capacité « activite », CONTRAT-HUB §5.25, fiche 98) : le worker de la file, les
// métriques et l'hôte des clics. Importé à blanc par internal/app/veridian_mod_activite.go.
package module

import (
	"context"
	"database/sql"
	"errors"
	"net"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/Notifuse/notifuse/internal/domain"
	"github.com/Notifuse/notifuse/internal/veridian/activite"
	"github.com/Notifuse/notifuse/internal/veridian/organisation"
	"github.com/Notifuse/notifuse/internal/veridian/platform"
)

// Settings : par variables d'environnement (job deploy/notifuse*.nomad.hcl).
type Settings struct {
	// Enabled : VERIDIAN_ACTIVITE_ENABLED=true. Éteint, les déclencheurs remplissent la file (bornée), rien ne part.
	Enabled bool
	// MetricsPort : VERIDIAN_ACTIVITE_METRICS_PORT, écouteur à part (tailnet seul par le job) ; 0 = pas d'écouteur.
	MetricsPort int
	// AlertAfter : VERIDIAN_ACTIVITE_ALERT_AFTER (durée Go), défaut 15 min.
	AlertAfter time.Duration
	// Only : VERIDIAN_ACTIVITE_WORKSPACES, espaces séparés par des virgules ; vide = tous.
	Only map[string]bool
}

// Load lit les réglages.
func Load(get func(string) string) Settings {
	s := Settings{Enabled: strings.EqualFold(strings.TrimSpace(get("VERIDIAN_ACTIVITE_ENABLED")), "true")}
	if n, err := strconv.Atoi(strings.TrimSpace(get("VERIDIAN_ACTIVITE_METRICS_PORT"))); err == nil && n > 0 && n < 65536 {
		s.MetricsPort = n
	}
	if d, err := time.ParseDuration(strings.TrimSpace(get("VERIDIAN_ACTIVITE_ALERT_AFTER"))); err == nil && d > 0 {
		s.AlertAfter = d
	}
	for _, w := range strings.Split(get("VERIDIAN_ACTIVITE_WORKSPACES"), ",") {
		if w = strings.TrimSpace(w); w != "" {
			if s.Only == nil {
				s.Only = map[string]bool{}
			}
			s.Only[w] = true
		}
	}
	return s
}

type workspaces struct{ repo domain.WorkspaceRepository }

func (w workspaces) IDs(ctx context.Context) ([]string, error) {
	l, err := w.repo.List(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]string, 0, len(l))
	for _, ws := range l {
		if ws != nil && ws.ID != "" {
			out = append(out, ws.ID)
		}
	}
	return out, nil
}

func (w workspaces) DB(ctx context.Context, id string) (*sql.DB, error) {
	return w.repo.GetConnection(ctx, id)
}

var (
	worker  *activite.Worker
	metrics *http.Server
)

func init() {
	platform.Register(platform.Capability{Name: "activite", Wire: wire, Start: start, Stop: stop})
}

func wire(h platform.Host) error {
	if h.WorkspaceRepository() == nil || h.Config() == nil {
		return nil // assemblage de test
	}
	s := Load(os.Getenv)
	ws := workspaces{repo: h.WorkspaceRepository()}
	worker = &activite.Worker{WS: ws, Sender: &activite.Sender{Client: activite.NewClient()}, Log: h.Logger(), Metrics: activite.NewMetrics(),
		Enabled: s.Enabled, EventsURL: organisation.HubEventsURL(h.Config().HubBaseURL), AlertAfter: s.AlertAfter, Only: s.Only}
	worker.Metrics.SetEnabled(s.Enabled)
	log := h.Logger()
	activite.SetClickRecorder(func(ctx context.Context, wsID, mid, host string) {
		db, err := ws.DB(ctx, wsID)
		if err != nil || db == nil {
			return
		}
		if err := activite.SetClickHost(ctx, db, mid, host); err != nil {
			log.WithFields(map[string]interface{}{"workspace_id": wsID, "error": err.Error()}).Debug("Activité Mail : hôte du clic non posé")
		}
	})
	if s.MetricsPort > 0 {
		mux := http.NewServeMux()
		mux.Handle("/metrics", worker.Metrics.Handler())
		metrics = &http.Server{Addr: ":" + strconv.Itoa(s.MetricsPort), Handler: mux, ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 10 * time.Second, WriteTimeout: 10 * time.Second}
	}
	log.WithFields(map[string]interface{}{"enabled": s.Enabled, "only": len(s.Only), "metrics_port": s.MetricsPort, "lot_url": worker.EventsURL + "/lot"}).Info("Activité Mail : capacité câblée")
	return nil
}

func start(ctx context.Context, h platform.Host) error {
	if worker == nil {
		return nil
	}
	if metrics != nil {
		ln, err := net.Listen("tcp", metrics.Addr)
		if err != nil {
			h.Logger().WithField("error", err.Error()).Error("Activité Mail : écouteur des métriques non ouvert")
		} else {
			go func() {
				if err := metrics.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
					h.Logger().WithField("error", err.Error()).Error("Activité Mail : écouteur des métriques arrêté")
				}
			}()
		}
	}
	worker.Run(ctx)
	return nil
}

func stop(ctx context.Context) error {
	activite.SetClickRecorder(nil)
	if metrics != nil {
		return metrics.Shutdown(ctx)
	}
	return nil
}
