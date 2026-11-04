// Package relaycert gère le certificat TLS du relais SMTP de soumission (fiche 71) DANS l'application :
// émission DNS-01 (Cloudflare) par ACME, stockage chiffré par SECRET_KEY, renouvellement avant échéance,
// rechargement à chaud (GetCertificate) sans redémarrage, et verdict d'alerte.
//
// Précédence (décidée par main) : un certificat fourni par fichier ou variable (SMTP_BRIDGE_TLS_CERT_BASE64)
// prime sur celui géré ici. Ce paquet n'est actif que si VERIDIAN_RELAY_CERT_MODE=managed.
package relaycert

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"github.com/Notifuse/notifuse/pkg/logger"
)

// Seuils (décision main) : renouvellement 30 jours avant échéance ; alerte en WARN sous 30 jours,
// en ERROR sous 7 jours. Backoff exponentiel plafonné après un échec de renouvellement.
const (
	DefaultRenewBefore = 30 * 24 * time.Hour
	AlertWarnBefore    = 30 * 24 * time.Hour
	AlertErrorBefore   = 7 * 24 * time.Hour
	DefaultCheckEvery  = time.Hour
	maxBackoff         = 6 * time.Hour
	baseBackoff        = 5 * time.Minute
)

// DefaultDirectory : annuaire ACME de production Let's Encrypt (surchargé par les réglages pour les tests).
const DefaultDirectory = "https://acme-v02.api.letsencrypt.org/directory"

// ErrNoCertificate : aucun certificat chargé (handshake refusé proprement, pas de panic).
var ErrNoCertificate = errors.New("relaycert: aucun certificat chargé")

// Bundle est le matériel émis : chaîne PEM, clé PEM, clé de compte ACME (pour réutiliser le compte).
type Bundle struct {
	CertPEM       []byte    `json:"cert_pem"`
	KeyPEM        []byte    `json:"key_pem"`
	AccountKeyPEM []byte    `json:"account_key_pem"`
	NotAfter      time.Time `json:"not_after"`
}

// Holder porte le certificat courant. Lecture sans verrou (atomic), écriture à chaque renouvellement.
type Holder struct {
	v atomic.Pointer[tls.Certificate]
}

// Set remplace le certificat servi. Les prochains handshakes utilisent le nouveau, sans redémarrage.
func (h *Holder) Set(c tls.Certificate) { h.v.Store(&c) }

// GetCertificate est branché dans tls.Config.GetCertificate du bridge.
func (h *Holder) GetCertificate(*tls.ClientHelloInfo) (*tls.Certificate, error) {
	c := h.v.Load()
	if c == nil {
		return nil, ErrNoCertificate
	}
	return c, nil
}

// Store persiste le bundle chiffré (SECRET_KEY) ; Load le relit. Absence = (nil, nil).
type Store interface {
	Load(ctx context.Context) (*Bundle, error)
	Save(ctx context.Context, b *Bundle) error
}

// Issuer émet un certificat pour un nom (DNS-01). accountKeyPEM vide = nouveau compte.
type Issuer interface {
	Obtain(ctx context.Context, domain, email string, accountKeyPEM []byte) (*Bundle, error)
}

// Logger est le logger de l'application (pkg/logger).
type Logger = logger.Logger

// Config du gestionnaire.
type Config struct {
	Domain      string
	Email       string
	RenewBefore time.Duration // 0 = DefaultRenewBefore
	CheckEvery  time.Duration // 0 = DefaultCheckEvery
}

// Status est le verdict lisible (santé, logs). Alert vide quand tout va bien.
type Status struct {
	Domain      string    `json:"domain"`
	NotAfter    time.Time `json:"not_after,omitempty"`
	LastRenewed time.Time `json:"last_renewed,omitempty"`
	LastError   string    `json:"last_error,omitempty"`
	Failures    int       `json:"failures"`
	Alert       string    `json:"alert,omitempty"`
}

// Manager orchestre chargement, décision de renouvellement, émission et verdict.
type Manager struct {
	cfg    Config
	store  Store
	issuer Issuer
	holder *Holder
	logger Logger
	now    func() time.Time

	mu          sync.Mutex
	status      Status
	bundle      *Bundle
	failures    int
	nextAttempt time.Time
}

// NewManager construit le gestionnaire. holder reçoit chaque certificat chargé ou émis.
func NewManager(cfg Config, store Store, issuer Issuer, holder *Holder, logger Logger) (*Manager, error) {
	if cfg.Domain == "" || cfg.Email == "" {
		return nil, errors.New("relaycert: domaine et email requis")
	}
	if cfg.RenewBefore <= 0 {
		cfg.RenewBefore = DefaultRenewBefore
	}
	if cfg.CheckEvery <= 0 {
		cfg.CheckEvery = DefaultCheckEvery
	}
	return &Manager{cfg: cfg, store: store, issuer: issuer, holder: holder, logger: logger,
		now: time.Now, status: Status{Domain: cfg.Domain}}, nil
}

// Status rend le verdict courant (copie).
func (m *Manager) Status() Status {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.status
}

// Load relit le bundle persistant et le pose dans le holder. Sans bundle, rien n'est émis ici.
func (m *Manager) Load(ctx context.Context) error {
	b, err := m.store.Load(ctx)
	if err != nil {
		return fmt.Errorf("relaycert: lecture du stockage: %w", err)
	}
	if b == nil {
		return nil
	}
	if err := m.install(b); err != nil {
		return err
	}
	return nil
}

// install vérifie le bundle (paire cert/clé valide) puis le pose dans le holder.
func (m *Manager) install(b *Bundle) error {
	pair, err := tls.X509KeyPair(b.CertPEM, b.KeyPEM)
	if err != nil {
		return fmt.Errorf("relaycert: paire cert/clé invalide: %w", err)
	}
	leaf, err := x509.ParseCertificate(pair.Certificate[0])
	if err != nil {
		return fmt.Errorf("relaycert: certificat illisible: %w", err)
	}
	if err := leaf.VerifyHostname(m.cfg.Domain); err != nil {
		return fmt.Errorf("relaycert: le certificat ne couvre pas %s: %w", m.cfg.Domain, err)
	}
	b.NotAfter = leaf.NotAfter
	m.holder.Set(pair)
	m.mu.Lock()
	m.bundle = b
	m.status.NotAfter = leaf.NotAfter
	m.mu.Unlock()
	return nil
}

// Tick décide et agit : renouvelle si absent ou à moins de RenewBefore de l'échéance, sous backoff.
// Rend (true) si un nouveau certificat a été émis et posé.
func (m *Manager) Tick(ctx context.Context) (bool, error) {
	now := m.now()
	m.mu.Lock()
	need := m.bundle == nil || m.bundle.NotAfter.Sub(now) < m.cfg.RenewBefore
	wait := now.Before(m.nextAttempt)
	m.mu.Unlock()

	if !need {
		m.checkAlert(now)
		return false, nil
	}
	if wait {
		m.checkAlert(now)
		return false, nil
	}

	var acctKey []byte
	m.mu.Lock()
	if m.bundle != nil {
		acctKey = m.bundle.AccountKeyPEM
	}
	m.mu.Unlock()

	b, err := m.issuer.Obtain(ctx, m.cfg.Domain, m.cfg.Email, acctKey)
	if err == nil && b == nil {
		err = errors.New("relaycert: l'émetteur n'a rendu aucun certificat")
	}
	if err == nil {
		err = m.install(b)
	}
	if err == nil {
		err = m.store.Save(ctx, b)
	}
	if err != nil {
		m.mu.Lock()
		m.failures++
		m.status.Failures = m.failures
		m.status.LastError = err.Error()
		backoff := baseBackoff << min(m.failures-1, 10)
		if backoff > maxBackoff {
			backoff = maxBackoff
		}
		m.nextAttempt = now.Add(backoff)
		m.mu.Unlock()
		m.logger.WithFields(map[string]interface{}{
			"domain": m.cfg.Domain, "failures": m.failures, "next_attempt_in": backoff.String(), "error": err.Error(),
		}).Error("relaycert: échec du renouvellement du certificat du relais")
		m.checkAlert(now)
		return false, err
	}

	m.mu.Lock()
	m.failures = 0
	m.status.Failures = 0
	m.status.LastError = ""
	m.status.LastRenewed = now
	m.nextAttempt = time.Time{}
	m.mu.Unlock()
	m.logger.WithFields(map[string]interface{}{
		"domain": m.cfg.Domain, "not_after": b.NotAfter.UTC().Format(time.RFC3339),
	}).Info("relaycert: certificat du relais émis et posé à chaud")
	return true, nil
}

// checkAlert pose le verdict d'échéance et le journalise (WARN sous 30 j, ERROR sous 7 j). Silencieux sinon.
func (m *Manager) checkAlert(now time.Time) {
	m.mu.Lock()
	notAfter := m.status.NotAfter
	m.mu.Unlock()
	if notAfter.IsZero() {
		return
	}
	left := notAfter.Sub(now)
	alert := ""
	switch {
	case left < AlertErrorBefore:
		alert = "expiration imminente"
	case left < AlertWarnBefore:
		alert = "échéance proche"
	}
	m.mu.Lock()
	m.status.Alert = alert
	m.mu.Unlock()
	if alert == "" {
		return
	}
	fields := map[string]interface{}{"domain": m.cfg.Domain, "not_after": notAfter.UTC().Format(time.RFC3339), "days_left": int(left.Hours() / 24)}
	if left < AlertErrorBefore {
		m.logger.WithFields(fields).Error("relaycert: " + alert + " (certificat du relais)")
	} else {
		m.logger.WithFields(fields).Warn("relaycert: " + alert + " (certificat du relais)")
	}
}

// Start charge le bundle puis vérifie à intervalle jusqu'à l'annulation de ctx. Pas d'émission au
// démarrage hors absence de certificat : la décision passe par Tick, donc par le backoff.
func (m *Manager) Start(ctx context.Context) {
	if err := m.Load(ctx); err != nil {
		m.logger.WithField("error", err.Error()).Error("relaycert: chargement du certificat du relais")
	}
	go func() {
		t := time.NewTicker(m.cfg.CheckEvery)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				_, _ = m.Tick(ctx)
			}
		}
	}()
}

// pemBlock encode un bloc PEM (certificat, clé).
func pemBlock(typ string, der []byte) []byte {
	return pem.EncodeToMemory(&pem.Block{Type: typ, Bytes: der})
}
