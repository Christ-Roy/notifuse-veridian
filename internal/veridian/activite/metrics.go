package activite

import (
	"fmt"
	"io"
	"net/http"
	"sort"
	"sync"
	"time"
)

// Metrics : l'état de la file au format Prometheus. Aucune étiquette par espace (le palier gratuit de Grafana Cloud est compté en
// séries) : le détail d'un espace se lit dans les journaux (workspace_id).
type Metrics struct {
	mu          sync.Mutex
	enabled     bool
	pending     int
	withPending int
	failing     int
	oldestAge   time.Duration
	lastPass    time.Time
	alert       bool
	events      map[string]int64
	lots        map[string]int64
}

// NewMetrics : compteurs à zéro.
func NewMetrics() *Metrics {
	m := &Metrics{events: map[string]int64{}, lots: map[string]int64{}}
	for _, k := range []string{"accepte", "doublon", "ignore", OutcomeRefused, OutcomeExpired, OutcomeOverflow, OutcomeOrphan} {
		m.events[k] = 0
	}
	for _, v := range []Verdict{Accepted, Refused, Unauthorized, Retry} {
		m.lots[v.String()] = 0
	}
	return m
}

func (m *Metrics) SetEnabled(on bool) { m.mu.Lock(); m.enabled = on; m.mu.Unlock() }
func (m *Metrics) SetAlert(on bool)   { m.mu.Lock(); m.alert = on; m.mu.Unlock() }

// SetPass : l'état relevé à la fin d'un passage.
func (m *Metrics) SetPass(pending, withPending, failing int, oldestAge time.Duration, at time.Time) {
	m.mu.Lock()
	m.pending, m.withPending, m.failing, m.oldestAge, m.lastPass = pending, withPending, failing, oldestAge, at
	m.mu.Unlock()
}

func (m *Metrics) AddEvents(result string, n int) {
	if n <= 0 {
		return
	}
	m.mu.Lock()
	m.events[result] += int64(n)
	m.mu.Unlock()
}

func (m *Metrics) AddLot(v Verdict) { m.mu.Lock(); m.lots[v.String()]++; m.mu.Unlock() }

func b2i(b bool) int {
	if b {
		return 1
	}
	return 0
}

// Write : l'exposition texte Prometheus.
func (m *Metrics) Write(w io.Writer) {
	m.mu.Lock()
	defer m.mu.Unlock()
	g := func(name, help string, v interface{}) {
		fmt.Fprintf(w, "# HELP %s %s\n# TYPE %s gauge\n%s %v\n", name, help, name, name, v)
	}
	g("veridian_activite_enabled", "Interrupteur de l'envoi au Hub (VERIDIAN_ACTIVITE_ENABLED).", b2i(m.enabled))
	g("veridian_activite_pending_events", "Evenements en attente dans la file, tous espaces.", m.pending)
	g("veridian_activite_oldest_pending_seconds", "Age du plus vieil evenement non envoye.", int64(m.oldestAge.Seconds()))
	g("veridian_activite_workspaces_pending", "Espaces qui ont des evenements en attente.", m.withPending)
	g("veridian_activite_workspaces_failing", "Espaces dont le dernier lot n'a pas ete remis (401, 429, 5xx, 404).", m.failing)
	g("veridian_activite_lag_alert", "1 quand l'envoi est allume et que le retard depasse le seuil.", b2i(m.alert))
	last := int64(0)
	if !m.lastPass.IsZero() {
		last = m.lastPass.Unix()
	}
	g("veridian_activite_last_pass_timestamp_seconds", "Fin du dernier passage du worker.", last)
	c := func(name, help, label string, vals map[string]int64) {
		fmt.Fprintf(w, "# HELP %s %s\n# TYPE %s counter\n", name, help, name)
		keys := make([]string, 0, len(vals))
		for k := range vals {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			fmt.Fprintf(w, "%s{%s=%q} %d\n", name, label, k, vals[k])
		}
	}
	c("veridian_activite_events_total", "Evenements par issue (accepte, doublon, ignore par le Hub ; refuse, perime, debordement, orphelin ici).", "resultat", m.events)
	c("veridian_activite_lots_total", "Lots postes au Hub par verdict.", "resultat", m.lots)
}

// Handler : GET /metrics.
func (m *Metrics) Handler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		w.Header().Set("Content-Type", "text/plain; version=0.0.4; charset=utf-8")
		w.Header().Set("Cache-Control", "no-store")
		m.Write(w)
	})
}
