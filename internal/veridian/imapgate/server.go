// Package imapgate sert la boîte unifiée d'un profil en IMAP (fiche 84) : Thunderbird, téléphone ou CRM se connectent à
// Notifuse avec un accès de boîte, quel que soit le fournisseur derrière (Postfix interne, Google, Infomaniak). Rien
// n'est relu chez le fournisseur : le client voit les messages de l'API.
package imapgate

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"net"
	"sync"
	"time"

	"github.com/Notifuse/notifuse/internal/veridian/boxaccess"
	"github.com/Notifuse/notifuse/internal/veridian/mailbox"
	"github.com/Notifuse/notifuse/pkg/logger"
	"github.com/emersion/go-imap/v2"
	"github.com/emersion/go-imap/v2/imapserver"
)

// Bornes du serveur.
const (
	MaxConns          = 400
	MaxConnsPerAccess = 20
	// Essais de connexion par fenêtre de 10 min (seuls les échecs restent comptés).
	LimitIP      = "box_login_ip"
	LimitAddress = "box_login_address"
	TriesPerIP   = 40
	TriesPerAddr = 15
	LimitWindow  = 10 * time.Minute

	// refreshEvery : âge maximal de la vue d'un dossier avant relecture ; idleEvery : période de veille.
	refreshEvery = 3 * time.Second
	idleEvery    = 5 * time.Second
	aliveEvery   = 5 * time.Second
	opTimeout    = 30 * time.Second
)

// Authenticator vérifie un accès de boîte.
type Authenticator interface {
	Verify(ctx context.Context, address, secret string) (*boxaccess.Access, error)
	Alive(ctx context.Context, id string) (*boxaccess.Access, error)
	Touch(ctx context.Context, id string) error
}

// Limiter : limiteur de débit de l'application.
type Limiter interface {
	Allow(namespace, key string) bool
	Reset(namespace, key string)
}

// Boxes : la boîte unifiée.
type Boxes interface {
	BoxIndex(ctx context.Context, ws, profileID string, folder mailbox.BoxFolder) ([]mailbox.BoxEntry, error)
	BoxMessages(ctx context.Context, ws, profileID string, ids []string) (map[string]*mailbox.Message, error)
	BoxSetFlags(ctx context.Context, ws, profileID, id string, flags []string, hidden *bool) error
	BoxPurge(ctx context.Context, ws, profileID, id string, flags []string) error
	BoxSentKnown(ctx context.Context, ws, profileID string, messageIDs []string) (bool, error)
}

// Config du serveur. TLSConfig nil n'est accepté qu'avec BehindTLSProxy (TLS terminé par l'entrée publique, réseau privé).
type Config struct {
	Addr           string
	TLSConfig      *tls.Config
	BehindTLSProxy bool
	// ProxyProtocol : l'entrée publique annonce l'adresse du client (PROXY v2) ; une connexion sans en-tête est coupée.
	ProxyProtocol bool
	Auth          Authenticator
	Boxes         Boxes
	Limiter       Limiter
	Logger        logger.Logger
}

// Server : serveur IMAP de boîtes.
type Server struct {
	cfg  Config
	imap *imapserver.Server

	mu     sync.Mutex
	conns  int
	perAcc map[string]int

	sizeMu sync.Mutex
	sizes  map[string]int64
}

// New construit le serveur. Sans TLS et sans entrée TLS devant, refus : jamais de mot de passe en clair.
func New(cfg Config) (*Server, error) {
	if cfg.TLSConfig == nil && !cfg.BehindTLSProxy {
		return nil, errors.New("serveur IMAP : TLS requis")
	}
	if cfg.Auth == nil || cfg.Boxes == nil || cfg.Limiter == nil || cfg.Logger == nil {
		return nil, errors.New("serveur IMAP : dépendance manquante")
	}
	s := &Server{cfg: cfg, perAcc: map[string]int{}, sizes: map[string]int64{}}
	s.imap = imapserver.New(&imapserver.Options{
		NewSession: func(c *imapserver.Conn) (imapserver.Session, *imapserver.GreetingData, error) {
			return s.newSession(remoteIP(c.NetConn())), nil, nil
		},
		Caps:         imap.CapSet{imap.CapIMAP4rev1: {}, imap.CapUIDPlus: {}, imap.CapMove: {}, imap.CapNamespace: {}, imap.CapESearch: {}},
		InsecureAuth: cfg.BehindTLSProxy, // TLS déjà terminé par l'entrée publique ; sinon la session est elle-même en TLS
		Logger:       imapLogger{cfg.Logger},
	})
	return s, nil
}

// Serve sert l'écoute donnée (TLS posé ici si configuré) jusqu'à Close.
func (s *Server) Serve(ln net.Listener) error {
	if s.cfg.ProxyProtocol {
		ln = newProxyListener(ln)
	}
	ln = &limitListener{Listener: ln, s: s}
	if s.cfg.TLSConfig != nil {
		ln = tls.NewListener(ln, s.cfg.TLSConfig)
	}
	return s.imap.Serve(ln)
}

// ListenAndServe écoute cfg.Addr.
func (s *Server) ListenAndServe() error {
	ln, err := net.Listen("tcp", s.cfg.Addr)
	if err != nil {
		return fmt.Errorf("serveur IMAP : écoute %s: %w", s.cfg.Addr, err)
	}
	return s.Serve(ln)
}

// Close coupe les connexions.
func (s *Server) Close() error { return s.imap.Close() }

// limitListener borne le nombre de connexions simultanées.
type limitListener struct {
	net.Listener
	s *Server
}

type limitConn struct {
	net.Conn
	once sync.Once
	done func()
}

func (c *limitConn) Close() error {
	c.once.Do(c.done)
	return c.Conn.Close()
}

func (l *limitListener) Accept() (net.Conn, error) {
	for {
		c, err := l.Listener.Accept()
		if err != nil {
			return nil, err
		}
		l.s.mu.Lock()
		full := l.s.conns >= MaxConns
		if !full {
			l.s.conns++
		}
		l.s.mu.Unlock()
		if full {
			_ = c.Close()
			continue
		}
		return &limitConn{Conn: c, done: func() { l.s.mu.Lock(); l.s.conns--; l.s.mu.Unlock() }}, nil
	}
}

func (s *Server) acquire(accessID string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.perAcc[accessID] >= MaxConnsPerAccess {
		return false
	}
	s.perAcc[accessID]++
	return true
}

func (s *Server) release(accessID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.perAcc[accessID]--; s.perAcc[accessID] <= 0 {
		delete(s.perAcc, accessID)
	}
}

// size garde la taille des messages reconstitués (le rendu est stable, la mesurer une fois suffit).
func (s *Server) size(id string) (int64, bool) {
	s.sizeMu.Lock()
	defer s.sizeMu.Unlock()
	n, ok := s.sizes[id]
	return n, ok
}

func (s *Server) putSize(id string, n int64) {
	s.sizeMu.Lock()
	defer s.sizeMu.Unlock()
	if len(s.sizes) > 100000 {
		s.sizes = map[string]int64{}
	}
	s.sizes[id] = n
}

func remoteIP(c net.Conn) string {
	if c == nil || c.RemoteAddr() == nil {
		return "?"
	}
	h, _, err := net.SplitHostPort(c.RemoteAddr().String())
	if err != nil {
		return c.RemoteAddr().String()
	}
	return h
}

// imapLogger : les erreurs de protocole (client coupé, TLS raté) sont du bruit de fond d'un port ouvert.
type imapLogger struct{ l logger.Logger }

func (i imapLogger) Printf(format string, args ...interface{}) {
	i.l.WithField("detail", fmt.Sprintf(format, args...)).Debug("IMAP gate: protocol event")
}
