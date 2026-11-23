// Package mxgate reçoit le courrier entrant des boîtes louées (fiche 84) : serveur SMTP sans authentification, pour les
// seules adresses des profils d'envoi des domaines ouverts. Le 250 n'est rendu qu'une fois le mail rangé en base : base ou
// application arrêtée, l'expéditeur réessaie (tout serveur de mail le fait pendant des jours), rien n'est perdu et rien n'est
// en attente chez nous. Aucun relayage : une adresse inconnue est refusée au RCPT TO.
package mxgate

import (
	"context"
	"crypto/rand"
	"crypto/tls"
	"encoding/binary"
	"errors"
	"io"
	"net"
	"strings"
	"time"

	"github.com/Notifuse/notifuse/internal/domain"
	"github.com/Notifuse/notifuse/internal/veridian/mailbox"
	"github.com/Notifuse/notifuse/pkg/logger"
	"github.com/emersion/go-smtp"
)

// Bornes.
const (
	MaxMessageBytes = 25 << 20
	MaxRecipients   = 20
	LimitIP         = "mx_mail_ip" // messages par IP
	PerIP           = 120
	LimitWindow     = 10 * time.Minute
	dataTimeout     = 60 * time.Second
	// SourceID : boîte source portée par un mail reçu directement (pas d'intégration IMAP).
	SourceID = "mx"
)

// Target : la boîte qui possède une adresse.
type Target struct {
	WorkspaceID string
	ProfileID   string
	Address     string
	Owners      mailbox.Owners
}

// ErrUnknown : adresse que Notifuse ne reçoit pas. ErrAmbiguous : adresse portée par plusieurs espaces, jamais devinée.
var (
	ErrUnknown   = errors.New("adresse inconnue")
	ErrAmbiguous = errors.New("adresse portée par plusieurs espaces")
)

// Directory retrouve la boîte d'une adresse.
type Directory interface {
	Lookup(ctx context.Context, address string) (*Target, error)
}

// Ingestor range un mail reçu.
type Ingestor interface {
	IngestRaw(ctx context.Context, ws string, box mailbox.BoxInfo, ref mailbox.MailboxRef, raw []byte, at time.Time, truncated bool) (*mailbox.Message, bool, error)
}

// Dispatcher remet le mail aux mêmes consommateurs qu'un mail lu en IMAP (arrêt sur réponse, copies au client). Idempotents.
type Dispatcher interface {
	DispatchMessage(msg *domain.VeridianIMAPMessage) bool
}

// Blocklist dit si l'IP du client est sur une liste noire ; une mesure non aboutie laisse passer (l'expéditeur honnête d'abord).
type Blocklist interface {
	Listed(ctx context.Context, ip string) (bool, string)
}

// Limiter : limiteur de débit.
type Limiter interface {
	Allow(namespace, key string) bool
}

// Config du récepteur.
type Config struct {
	Addr      string
	Hostname  string
	TLSConfig *tls.Config // STARTTLS proposé si présent
	Directory Directory
	Ingest    Ingestor
	Dispatch  Dispatcher
	Blocklist Blocklist
	Limiter   Limiter
	Logger    logger.Logger
}

// Server : récepteur.
type Server struct {
	cfg  Config
	smtp *smtp.Server
}

// New construit le récepteur.
func New(cfg Config) (*Server, error) {
	if cfg.Directory == nil || cfg.Ingest == nil || cfg.Limiter == nil || cfg.Logger == nil || cfg.Hostname == "" {
		return nil, errors.New("récepteur de courrier : dépendance manquante")
	}
	s := &Server{cfg: cfg}
	srv := smtp.NewServer(s)
	srv.Addr, srv.Domain, srv.TLSConfig = cfg.Addr, cfg.Hostname, cfg.TLSConfig
	srv.ReadTimeout, srv.WriteTimeout = 60*time.Second, 60*time.Second
	srv.MaxMessageBytes, srv.MaxRecipients = MaxMessageBytes, MaxRecipients
	srv.ErrorLog = quiet{}
	s.smtp = srv
	return s, nil
}

// quiet : les coupures de connexion sont le bruit de fond d'un port 25.
type quiet struct{}

func (quiet) Printf(string, ...interface{}) {}
func (quiet) Println(...interface{})        {}

// Serve sert l'écoute donnée.
func (s *Server) Serve(ln net.Listener) error { return s.smtp.Serve(ln) }

// ListenAndServe écoute cfg.Addr.
func (s *Server) ListenAndServe() error {
	ln, err := net.Listen("tcp", s.cfg.Addr)
	if err != nil {
		return err
	}
	return s.Serve(ln)
}

// Close coupe les connexions.
func (s *Server) Close() error { return s.smtp.Close() }

// NewSession implémente smtp.Backend.
func (s *Server) NewSession(c *smtp.Conn) (smtp.Session, error) {
	ip := "?"
	if c != nil && c.Conn() != nil {
		if h, _, err := net.SplitHostPort(c.Conn().RemoteAddr().String()); err == nil {
			ip = h
		}
	}
	return &session{s: s, ip: ip}, nil
}

type session struct {
	s       *Server
	ip      string
	checked bool
	from    string
	started bool
	to      []*Target
}

func refuse(code, a, b, c int, msg string) error {
	return &smtp.SMTPError{Code: code, EnhancedCode: smtp.EnhancedCode{a, b, c}, Message: msg}
}

func (c *session) Reset()        { c.from, c.started, c.to = "", false, nil }
func (c *session) Logout() error { return nil }

func (c *session) Mail(from string, _ *smtp.MailOptions) error {
	if !c.s.cfg.Limiter.Allow(LimitIP, c.ip) {
		return refuse(451, 4, 7, 1, "trop de messages, reessayez plus tard")
	}
	if c.s.cfg.Blocklist != nil && !c.checked {
		ctx, cancel := context.WithTimeout(context.Background(), 12*time.Second)
		listed, detail := c.s.cfg.Blocklist.Listed(ctx, c.ip)
		cancel()
		if listed {
			c.s.cfg.Logger.WithFields(map[string]interface{}{"remote_ip": c.ip, "detail": detail}).Info("MX gate: sender IP on a blocklist, refused")
			return refuse(554, 5, 7, 1, "adresse IP sur liste noire Spamhaus")
		}
		c.checked = true
	}
	c.from, c.started = from, true
	return nil
}

func (c *session) Rcpt(to string, _ *smtp.RcptOptions) error {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	t, err := c.s.cfg.Directory.Lookup(ctx, to)
	switch {
	case errors.Is(err, ErrUnknown):
		return refuse(550, 5, 1, 1, "adresse inconnue")
	case err != nil:
		c.s.cfg.Logger.WithField("error", err.Error()).Error("MX gate: recipient lookup failed")
		return refuse(451, 4, 3, 0, "service indisponible, reessayez")
	}
	c.to = append(c.to, t)
	return nil
}

func (c *session) Data(r io.Reader) error {
	raw, err := io.ReadAll(r)
	if err != nil {
		return err // taille dépassée ou coupure : go-smtp rend son code
	}
	if len(c.to) == 0 {
		return refuse(554, 5, 5, 1, "aucun destinataire valide")
	}
	at := time.Now().UTC()
	full := trace(c.ip, c.s.cfg.Hostname, c.from, at, raw)
	ctx, cancel := context.WithTimeout(context.Background(), dataTimeout)
	defer cancel()
	// Une boîte en échec fait réessayer tout le message : les boîtes déjà servies dédoublonnent par Message-ID.
	for _, t := range c.to {
		if err := c.deliver(ctx, t, full, at); err != nil {
			c.s.cfg.Logger.WithFields(map[string]interface{}{"workspace_id": t.WorkspaceID, "profile_id": t.ProfileID, "error": err.Error()}).Error("MX gate: delivery failed, sender will retry")
			return refuse(451, 4, 3, 0, "service indisponible, reessayez")
		}
	}
	return nil
}

func (c *session) deliver(ctx context.Context, t *Target, raw []byte, at time.Time) error {
	ref := mailbox.MailboxRef{IntegrationID: SourceID + ":" + t.ProfileID, Folder: "MX", UIDValidity: uint32(at.Unix()), UID: randUID()}
	box := mailbox.BoxInfo{IntegrationID: ref.IntegrationID, Address: t.Address, ProfileID: t.ProfileID, Folder: ref.Folder, Owners: t.Owners}
	m, inserted, err := c.s.cfg.Ingest.IngestRaw(ctx, t.WorkspaceID, box, ref, raw, at, false)
	if err != nil {
		return err
	}
	if m == nil || !inserted || m.DuplicateOf != "" || c.s.cfg.Dispatch == nil {
		return nil
	}
	ok := c.s.cfg.Dispatch.DispatchMessage(&domain.VeridianIMAPMessage{UID: ref.UID, UIDValidity: ref.UIDValidity, Folder: ref.Folder,
		WorkspaceID: t.WorkspaceID, IntegrationID: ref.IntegrationID, MessageID: strings.Trim(m.MessageID, "<>"), InReplyTo: strings.Trim(m.InReplyTo, "<>"),
		References: domain.VeridianReferencesFromRaw(raw), From: m.From, To: m.To, Subject: m.Subject, Date: at, RawBody: raw})
	if !ok {
		// Le mail est rangé ; un consommateur en échec se rejoue par le réconciliateur (réponse rattachée), pas en faisant
		// renvoyer le mail par l'expéditeur.
		c.s.cfg.Logger.WithFields(map[string]interface{}{"workspace_id": t.WorkspaceID, "message_id": m.ID}).Error("MX gate: a consumer failed, message stored")
	}
	return nil
}

// trace pose l'enveloppe et le Received, comme tout serveur de réception (RFC 5321).
func trace(ip, host, from string, at time.Time, raw []byte) []byte {
	clean := func(s string) string {
		return strings.Map(func(r rune) rune {
			if r < 32 || r == 127 {
				return -1
			}
			return r
		}, s)
	}
	h := "Return-Path: <" + clean(from) + ">\r\nReceived: from [" + clean(ip) + "] by " + host + " with ESMTP; " + at.Format(time.RFC1123Z) + "\r\n"
	return append([]byte(h), raw...)
}

func randUID() uint32 {
	var b [4]byte
	_, _ = rand.Read(b[:])
	return binary.BigEndian.Uint32(b[:])>>1 | 1
}
