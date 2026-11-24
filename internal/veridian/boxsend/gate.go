package boxsend

import (
	"context"
	"errors"
	"io"
	"net"
	"strings"
	"time"

	"github.com/Notifuse/notifuse/internal/veridian/boxaccess"
	"github.com/Notifuse/notifuse/internal/veridian/smtprelay"
	"github.com/Notifuse/notifuse/pkg/smtp_bridge"
	"github.com/emersion/go-sasl"
	"github.com/emersion/go-smtp"
)

// Porte SMTP PUBLIQUE des boîtes : accès de boîte seulement, jamais une clé d'API. TLS terminé par l'entrée publique, qui annonce
// la vraie adresse du client (PROXY v2) : les essais se comptent par adresse, un tiers ne ferme pas la porte aux autres. AUTH LOGIN
// en plus de PLAIN : les vieux Outlook ne connaissent que lui.

// Essais de connexion par adresse IP, par couple boîte + adresse IP (seuls les échecs restent comptés) et par connexion. Le couple, pas
// la boîte seule : un tiers qui se trompe exprès n'enferme jamais le titulaire dehors.
const (
	LimitGateIP   = "box_smtp_gate_ip"
	LimitGateBox  = "box_smtp_gate_box"
	GateTriesIP   = 40
	GateTriesBox  = 15
	GateWindow    = 10 * time.Minute
	triesPerConn  = 3
	gateOpTimeout = 60 * time.Second
)

// GateLimiter : limiteur de débit, remise à zéro comprise.
type GateLimiter interface {
	Allow(namespace, key string) bool
	Reset(namespace, key string)
}

// Gate est le serveur SMTP public.
type Gate struct {
	snd  *Sender
	lim  GateLimiter
	smtp *smtp.Server
}

// NewGate construit la porte.
func NewGate(snd *Sender, lim GateLimiter, hostname string) (*Gate, error) {
	if snd == nil || lim == nil || hostname == "" {
		return nil, errors.New("porte SMTP des boîtes : dépendance manquante")
	}
	g := &Gate{snd: snd, lim: lim}
	srv := smtp.NewServer(g)
	srv.Domain = hostname
	srv.ReadTimeout, srv.WriteTimeout = 2*time.Minute, 2*time.Minute
	srv.MaxMessageBytes, srv.MaxRecipients = smtprelay.MaxMessageBytes, smtprelay.MaxRecipients
	srv.AllowInsecureAuth = true // TLS déjà terminé par l'entrée publique
	srv.ErrorLog = gateQuiet{}
	g.smtp = srv
	return g, nil
}

type gateQuiet struct{}

func (gateQuiet) Printf(string, ...interface{}) {}
func (gateQuiet) Println(...interface{})        {}

// Serve sert l'écoute donnée.
func (g *Gate) Serve(ln net.Listener) error { return g.smtp.Serve(ln) }

// Shutdown laisse finir les sessions en cours.
func (g *Gate) Shutdown(ctx context.Context) error { return g.smtp.Shutdown(ctx) }

// NewSession implémente smtp.Backend.
func (g *Gate) NewSession(c *smtp.Conn) (smtp.Session, error) {
	ip := "?"
	if c != nil && c.Conn() != nil {
		if h, _, err := net.SplitHostPort(c.Conn().RemoteAddr().String()); err == nil {
			ip = h
		}
	}
	return &gateSession{g: g, ip: ip}, nil
}

type gateSession struct {
	g     *Gate
	ip    string
	user  string
	tries int
	from  string
	to    []string
}

var _ smtp.AuthSession = (*gateSession)(nil)

func (s *gateSession) AuthMechanisms() []string { return []string{sasl.Plain, sasl.Login} }

func (s *gateSession) Auth(mech string) (sasl.Server, error) {
	switch strings.ToUpper(mech) {
	case sasl.Plain:
		return sasl.NewPlainServer(func(_, u, p string) error { return s.login(u, p) }), nil
	case sasl.Login:
		return &loginServer{auth: s.login}, nil
	}
	return nil, smtp.ErrAuthUnknownMechanism
}

func (s *gateSession) login(user, pass string) error {
	s.tries++
	box, from := boxaccess.Fingerprint(user), boxaccess.LimitKey(s.ip)
	log := s.g.snd.d.Logger.WithFields(map[string]interface{}{"remote_ip": s.ip, "box": box})
	if s.tries > triesPerConn || !s.g.lim.Allow(LimitGateIP, from) || !s.g.lim.Allow(LimitGateBox, box+"|"+from) {
		log.Warn("Box SMTP: too many login attempts")
		return refuse(454, 4, 7, 0, "trop d'essais, reessayez plus tard")
	}
	if LooksLikeAPIKey(pass) {
		return refuse(535, 5, 7, 8, "identifiants refuses")
	}
	ctx, cancel := context.WithTimeout(context.Background(), gateOpTimeout)
	defer cancel()
	id, err := s.g.snd.Login(ctx, user, pass)
	switch {
	case errors.Is(err, boxaccess.ErrAuth):
		log.Warn("Box SMTP: login refused")
		return refuse(535, 5, 7, 8, "identifiants refuses")
	case errors.Is(err, smtp_bridge.ErrTemporaryAuth) || err != nil:
		return refuse(454, 4, 7, 0, "service indisponible, reessayez")
	}
	s.g.lim.Reset(LimitGateIP, from)
	s.g.lim.Reset(LimitGateBox, box+"|"+from)
	log.Info("Box SMTP: gate login") // adresse réelle du client : la ligne « Box SMTP: login » du service ne la connaît pas
	s.user = id
	return nil
}

func (s *gateSession) Reset()        { s.from, s.to = "", nil }
func (s *gateSession) Logout() error { return nil }

func (s *gateSession) Mail(from string, _ *smtp.MailOptions) error {
	if s.user == "" {
		return smtp.ErrAuthRequired
	}
	s.from = from
	return nil
}

func (s *gateSession) Rcpt(to string, _ *smtp.RcptOptions) error {
	if s.user == "" {
		return smtp.ErrAuthRequired
	}
	ctx, cancel := context.WithTimeout(context.Background(), gateOpTimeout)
	defer cancel()
	if err := s.g.snd.CheckRecipient(ctx, s.user, to); err != nil {
		return err
	}
	s.to = append(s.to, to)
	return nil
}

func (s *gateSession) Data(r io.Reader) error {
	if s.user == "" {
		return smtp.ErrAuthRequired
	}
	data, err := io.ReadAll(r)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), gateOpTimeout)
	defer cancel()
	return s.g.snd.Submit(ctx, s.user, s.from, s.to, data)
}

// loginServer : mécanisme LOGIN (obsolète mais seul connu de vieux clients).
type loginServer struct {
	auth func(user, pass string) error
	step int
	user string
}

func (l *loginServer) Next(resp []byte) ([]byte, bool, error) {
	if l.step == 0 && resp == nil {
		l.step = 1
		return []byte("Username:"), false, nil
	}
	if l.step <= 1 {
		l.step, l.user = 2, string(resp)
		return []byte("Password:"), false, nil
	}
	if l.step == 2 {
		l.step = 3
		return nil, true, l.auth(l.user, string(resp))
	}
	return nil, false, sasl.ErrUnexpectedClientResponse
}
