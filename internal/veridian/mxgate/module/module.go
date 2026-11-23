// Package module branche le récepteur de courrier (capacité « mx », fiche 84). Sans domaine ouvert, rien n'écoute.
// Importé à blanc par internal/app/veridian_mod_mx.go.
package module

import (
	"context"
	"crypto/tls"
	"fmt"
	"net"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/Notifuse/notifuse/internal/service/queue"
	"github.com/Notifuse/notifuse/internal/veridian/imapgate"
	"github.com/Notifuse/notifuse/internal/veridian/mailbox"
	mailboxmodule "github.com/Notifuse/notifuse/internal/veridian/mailbox/module"
	"github.com/Notifuse/notifuse/internal/veridian/mxgate"
	"github.com/Notifuse/notifuse/internal/veridian/platform"
	"github.com/Notifuse/notifuse/internal/veridian/relayguard"
	"github.com/Notifuse/notifuse/pkg/ratelimiter"
)

// Settings : réglages, par variables d'environnement.
type Settings struct {
	Domains []string // VERIDIAN_MX_DOMAINS : domaines reçus, vide = éteint
	Port    int
	Host    string // nom annoncé, celui du MX
	PROXYv2 bool
}

// ReadSettings lit les réglages.
func ReadSettings(get func(string) string) (Settings, error) {
	s := Settings{Port: 2525, Host: get("VERIDIAN_MX_HOST"), PROXYv2: get("VERIDIAN_MX_PROXY_PROTOCOL") == "true"}
	for _, d := range strings.Split(get("VERIDIAN_MX_DOMAINS"), ",") {
		if d = strings.TrimSpace(d); d != "" {
			s.Domains = append(s.Domains, d)
		}
	}
	if v := get("VERIDIAN_MX_PORT"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 || n > 65535 {
			return s, fmt.Errorf("VERIDIAN_MX_PORT invalide")
		}
		s.Port = n
	}
	if len(s.Domains) > 0 && s.Host == "" {
		return s, fmt.Errorf("VERIDIAN_MX_HOST requis avec VERIDIAN_MX_DOMAINS")
	}
	return s, nil
}

// zen adapte le vérificateur Spamhaus, verdict gardé 10 min par IP. PBL compris : une plage « grand public » ne livre pas à un MX.
type zen struct {
	check queue.RelayChecker
	mu    sync.Mutex
	cache map[string]verdict
}

type verdict struct {
	listed bool
	detail string
	at     time.Time
}

func (z *zen) Listed(ctx context.Context, ip string) (bool, string) {
	p := net.ParseIP(ip)
	if p == nil || p.To4() == nil || p.IsPrivate() || p.IsLoopback() {
		return false, ""
	}
	z.mu.Lock()
	v, ok := z.cache[ip]
	z.mu.Unlock()
	if ok && time.Since(v.at) < 10*time.Minute {
		return v.listed, v.detail
	}
	c := z.check.CheckRelay(ctx, ip)
	if c.State == queue.RelayUnverified {
		return false, "" // mesure non aboutie : on reçoit
	}
	v = verdict{listed: c.State == queue.RelayListed || c.Warning != "", detail: c.Detail + c.Warning, at: time.Now()}
	z.mu.Lock()
	if len(z.cache) > 20000 {
		z.cache = map[string]verdict{}
	}
	z.cache[ip] = v
	z.mu.Unlock()
	return v.listed, v.detail
}

type gateHost interface {
	RateLimiter() *ratelimiter.RateLimiter
	RelayTLSConfig() (*tls.Config, error)
}

var (
	server   *mxgate.Server
	settings Settings
)

func init() {
	platform.Register(platform.Capability{Name: "mx", Wire: wire, Start: start, Stop: stop})
}

func wire(h platform.Host) error {
	server = nil
	if h.WorkspaceRepository() == nil {
		return nil // assemblage de test
	}
	set, err := ReadSettings(os.Getenv)
	if err != nil {
		return err
	}
	settings = set
	if len(set.Domains) == 0 {
		return nil
	}
	gh, ok := h.(gateHost)
	if !ok || gh.RateLimiter() == nil || h.IMAPPoller() == nil {
		return fmt.Errorf("récepteur de courrier demandé sans limiteur ou sans consommateurs")
	}
	tc, err := gh.RelayTLSConfig()
	if err != nil {
		h.Logger().WithField("error", err.Error()).Warn("MX gate: no TLS certificate, STARTTLS not offered")
		tc = nil
	}
	gh.RateLimiter().SetPolicy(mxgate.LimitIP, mxgate.PerIP, mxgate.LimitWindow)
	server, err = mxgate.New(mxgate.Config{Hostname: set.Host, TLSConfig: tc, Directory: mxgate.NewProfiles(h.WorkspaceRepository(), set.Domains),
		Ingest: mailbox.NewIngestor(mailboxmodule.NewStore(h.WorkspaceRepository()), h.Logger(), nil), Dispatch: h.IMAPPoller(),
		Blocklist: &zen{check: relayguard.NewZEN(), cache: map[string]verdict{}}, Limiter: gh.RateLimiter(), Logger: h.Logger()})
	return err
}

func start(_ context.Context, h platform.Host) error {
	if server == nil {
		return nil
	}
	ln, err := net.Listen("tcp", fmt.Sprintf(":%d", settings.Port))
	if err != nil {
		return err
	}
	if settings.PROXYv2 {
		ln = imapgate.ProxyListener(ln)
	}
	h.Logger().WithField("domains", strings.Join(settings.Domains, ",")).Info("MX gate: listening")
	return server.Serve(ln)
}

func stop(context.Context) error {
	if server == nil {
		return nil
	}
	return server.Close()
}
