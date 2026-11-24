// Package module branche les boîtes louées (capacité « boites », fiche 84) : accès par API et serveur IMAP. Le SMTP passe par
// le bridge existant (veridian_wiring.go). Importé à blanc par internal/app/veridian_mod_boites.go.
package module

import (
	"context"
	"crypto/tls"
	"fmt"
	"os"
	"strconv"
	"time"

	"github.com/Notifuse/notifuse/internal/veridian/boxaccess"
	"github.com/Notifuse/notifuse/internal/veridian/boxaccess/boxapi"
	"github.com/Notifuse/notifuse/internal/veridian/boxsend"
	"github.com/Notifuse/notifuse/internal/veridian/imapgate"
	"github.com/Notifuse/notifuse/internal/veridian/mailbox/mailapi"
	mailboxmodule "github.com/Notifuse/notifuse/internal/veridian/mailbox/module"
	"github.com/Notifuse/notifuse/internal/veridian/platform"
	"github.com/Notifuse/notifuse/pkg/logger"
	"github.com/Notifuse/notifuse/pkg/ratelimiter"
	"github.com/Notifuse/notifuse/pkg/smtp_bridge"
)

// Settings : réglages du serveur IMAP, par variables d'environnement.
type Settings struct {
	Enabled  bool
	Port     int
	Host     string // nom donné au client mail
	ShowPort int    // port donné au client mail (celui de l'entrée publique)
	PROXYv2  bool   // l'entrée publique annonce l'adresse du client
	SMTPPort int    // porte SMTP des boîtes derrière l'entrée TLS publique, 0 = éteinte
	SMTPHost string // nom et port donnés au client mail
	SMTPShow int
	Proxy    bool // TLS terminé par l'entrée publique, réseau privé jusqu'ici
}

// ReadSettings lit les réglages. VERIDIAN_IMAP_ENABLED absent : rien n'écoute.
func ReadSettings(get func(string) string, defaultHost string) (Settings, error) {
	s := Settings{Enabled: get("VERIDIAN_IMAP_ENABLED") == "true", Port: 1993, Host: defaultHost, Proxy: get("VERIDIAN_IMAP_BEHIND_TLS_PROXY") == "true", PROXYv2: get("VERIDIAN_IMAP_PROXY_PROTOCOL") == "true"}
	s.SMTPHost = get("VERIDIAN_BOX_SMTP_HOST")
	for name, dst := range map[string]*int{"VERIDIAN_IMAP_PORT": &s.Port, "VERIDIAN_IMAP_PUBLIC_PORT": &s.ShowPort,
		"VERIDIAN_BOX_SMTP_PORT": &s.SMTPPort, "VERIDIAN_BOX_SMTP_PUBLIC_PORT": &s.SMTPShow} {
		v := get(name)
		if v == "" {
			continue
		}
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 || n > 65535 {
			return s, fmt.Errorf("%s invalide", name)
		}
		*dst = n
	}
	if s.ShowPort == 0 {
		s.ShowPort = s.Port
	}
	if h := get("VERIDIAN_BOX_HOST"); h != "" {
		s.Host = h
	}
	return s, nil
}

// gateHost : ce que la capacité demande en plus du contrat commun.
type gateHost interface {
	RateLimiter() *ratelimiter.RateLimiter
	RelayTLSConfig() (*tls.Config, error)
	BoxSender() *boxsend.Sender
}

var (
	server *imapgate.Server
	// smtpServer : SMTP des boîtes derrière l'entrée TLS publique. Seuls les accès de boîte y entrent, jamais une clé d'API.
	smtpServer *smtp_bridge.Server
)

// boxAuth n'accepte que les accès de boîte.
func boxAuth(snd *boxsend.Sender) smtp_bridge.AuthHandler {
	return func(username, password string) (string, error) {
		if boxsend.LooksLikeAPIKey(password) {
			return "", boxaccess.ErrAuth
		}
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		return snd.Login(ctx, username, password)
	}
}

// newSMTP construit la porte SMTP. Elle n'a pas de TLS : elle n'est jointe que par l'entrée publique qui le termine, sur le tailnet.
func newSMTP(set Settings, snd *boxsend.Sender, log logger.Logger) (*smtp_bridge.Server, error) {
	b := smtp_bridge.NewBackend(boxAuth(snd), func(userID, from string, to []string, data []byte) error {
		ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
		defer cancel()
		return snd.Submit(ctx, userID, from, to, data)
	}, log)
	b.SetRcptChecker(func(userID, to string) error {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		return snd.CheckRecipient(ctx, userID, to)
	})
	return smtp_bridge.NewServer(smtp_bridge.ServerConfig{Host: "0.0.0.0", Port: set.SMTPPort, Domain: set.SMTPHost, Mode: smtp_bridge.ModeOff, Logger: log}, b)
}

func init() {
	platform.Register(platform.Capability{Name: "boites", Wire: wire, Start: start, Stop: stop})
}

func wire(h platform.Host) error {
	server, smtpServer = nil, nil
	if h.WorkspaceRepository() == nil || h.DB() == nil || h.Mux() == nil || h.AuthService() == nil {
		return nil // assemblage de test
	}
	cfg := h.Config()
	set, err := ReadSettings(os.Getenv, cfg.SMTPBridge.Domain)
	if err != nil {
		return err
	}
	store := boxaccess.NewStore(h.DB())
	conn := boxapi.Connection{}
	if set.Enabled {
		conn.IMAP = &boxapi.Endpoint{Host: set.Host, Port: set.ShowPort, Security: "SSL/TLS"}
	}
	if cfg.SMTPBridge.Enabled {
		conn.SMTP = &boxapi.Endpoint{Host: cfg.SMTPBridge.Domain, Port: cfg.SMTPBridge.Port, Security: cfg.SMTPBridge.TLSMode}
	}
	if set.SMTPPort > 0 {
		gh, ok := h.(gateHost)
		if !ok || gh.BoxSender() == nil {
			return fmt.Errorf("SMTP de boîte demandé sans porte d'envoi")
		}
		if set.SMTPHost == "" {
			return fmt.Errorf("VERIDIAN_BOX_SMTP_HOST requis avec VERIDIAN_BOX_SMTP_PORT")
		}
		if smtpServer, err = newSMTP(set, gh.BoxSender(), h.Logger()); err != nil {
			return err
		}
		show := set.SMTPShow
		if show == 0 {
			show = set.SMTPPort
		}
		conn.SMTP = &boxapi.Endpoint{Host: set.SMTPHost, Port: show, Security: "SSL/TLS"}
	}
	(&boxapi.Handler{Store: store, Auth: mailapi.DomainAuthorizer{Auth: h.AuthService()}, WS: h.WorkspaceRepository(),
		Connection: conn, Log: h.Logger(), RequireAuth: h.RequireAuth()}).RegisterRoutes(h.Mux())

	if !set.Enabled {
		return nil
	}
	gh, ok := h.(gateHost)
	if !ok || gh.RateLimiter() == nil {
		return fmt.Errorf("serveur IMAP demandé sans limiteur")
	}
	var tc *tls.Config
	if !set.Proxy {
		if tc, err = gh.RelayTLSConfig(); err != nil || tc == nil {
			// Fermé plutôt qu'en clair : l'application démarre, l'IMAP n'écoute pas.
			h.Logger().WithField("error", fmt.Sprint(err)).Error("IMAP gate: no TLS certificate, server not started")
			return nil
		}
	}
	gh.RateLimiter().SetPolicy(imapgate.LimitIP, imapgate.TriesPerIP, imapgate.LimitWindow)
	gh.RateLimiter().SetPolicy(imapgate.LimitAddress, imapgate.TriesPerAddr, imapgate.LimitWindow)
	server, err = imapgate.New(imapgate.Config{Addr: fmt.Sprintf(":%d", set.Port), TLSConfig: tc, BehindTLSProxy: set.Proxy, ProxyProtocol: set.PROXYv2,
		Auth: &boxaccess.Guard{Store: store, WS: h.WorkspaceRepository()}, Boxes: mailboxmodule.NewStore(h.WorkspaceRepository()), Limiter: gh.RateLimiter(), Logger: h.Logger()})
	return err
}

func start(ctx context.Context, h platform.Host) error {
	if smtpServer != nil {
		go func() {
			if err := smtpServer.Start(); err != nil && ctx.Err() == nil {
				h.Logger().WithField("error", err.Error()).Error("Box SMTP: server stopped")
			}
		}()
	}
	if server == nil {
		return nil
	}
	h.Logger().Info("IMAP gate: listening")
	return server.ListenAndServe()
}

func stop(ctx context.Context) error {
	var err error
	if smtpServer != nil {
		err = smtpServer.Shutdown(ctx)
	}
	if server != nil {
		if e := server.Close(); err == nil {
			err = e
		}
	}
	return err
}
