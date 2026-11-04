// Package relaycert gère le certificat TLS du relais SMTP de soumission (fiche 71) DANS l'application :
// émission DNS-01 (Cloudflare) par ACME, stockage chiffré par SECRET_KEY, renouvellement avant échéance,
// rechargement à chaud (GetCertificate) sans redémarrage, et état écrit dans les réglages.
//
// Précédence (décidée par main) : un certificat fourni par fichier ou variable (SMTP_BRIDGE_TLS_CERT_BASE64)
// prime sur celui géré ici. La configuration (interrupteur, contact ACME, annuaire, jeton) est lue dans les
// réglages à chaque décision (relaysettings) : pas de redémarrage pour la changer.
package relaycert

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"fmt"
	"math/big"
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
	DefaultStartupWait = time.Minute
	maxBackoff         = 6 * time.Hour
	baseBackoff        = 5 * time.Minute
	maxErrorLen        = 300
)

// ErrNoCertificate : aucun certificat chargé (handshake refusé proprement, pas de panic).
var ErrNoCertificate = errors.New("relaycert: aucun certificat chargé")

// Bundle est le matériel émis : chaîne PEM, clé PEM, clé de compte ACME (pour réutiliser le compte).
// Directory note l'annuaire d'émission : un changement d'annuaire force une nouvelle émission.
type Bundle struct {
	CertPEM       []byte    `json:"cert_pem"`
	KeyPEM        []byte    `json:"key_pem"`
	AccountKeyPEM []byte    `json:"account_key_pem"`
	NotAfter      time.Time `json:"not_after"`
	Directory     string    `json:"directory"`
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

// State est l'état publié dans les réglages (lecture seule côté API) après chaque décision.
type State struct {
	NotAfter    time.Time
	Serial      string
	LastRenewed time.Time
	LastError   string
}

// Store persiste le bundle chiffré et l'état lisible ; Load relit le bundle. Absence = (nil, nil).
type Store interface {
	Load(ctx context.Context) (*Bundle, error)
	Save(ctx context.Context, b *Bundle) error
	SaveState(ctx context.Context, st State) error
}

// Issuer émet un certificat pour un nom (DNS-01). accountKeyPEM vide = nouveau compte.
type Issuer interface {
	Obtain(ctx context.Context, domain, email string, accountKeyPEM []byte) (*Bundle, error)
}

// Params est la configuration effective lue à chaque décision.
type Params struct {
	Managed      bool
	Email        string
	Directory    string
	DirectoryURL string
	CFToken      string
}

// ParamsFunc lit la configuration courante (réglages, repli sur l'environnement).
type ParamsFunc func(ctx context.Context) (Params, error)

// IssuerFunc construit l'émetteur pour une configuration donnée.
type IssuerFunc func(p Params) Issuer

// Logger est le logger de l'application (pkg/logger).
type Logger = logger.Logger

// Config du gestionnaire.
type Config struct {
	Domain      string
	RenewBefore time.Duration // 0 = DefaultRenewBefore
	CheckEvery  time.Duration // 0 = DefaultCheckEvery
	StartupWait time.Duration // 0 = DefaultStartupWait : délai avant la première décision (pas d'émission au démarrage)
}

// Status est le verdict lisible (santé, logs). Alert vide quand tout va bien.
type Status struct {
	Domain      string    `json:"domain"`
	NotAfter    time.Time `json:"not_after,omitempty"`
	Serial      string    `json:"serial,omitempty"`
	LastRenewed time.Time `json:"last_renewed,omitempty"`
	LastError   string    `json:"last_error,omitempty"`
	Failures    int       `json:"failures"`
	Alert       string    `json:"alert,omitempty"`
}

// Manager orchestre chargement, décision de renouvellement, émission et verdict.
type Manager struct {
	cfg    Config
	store  Store
	params ParamsFunc
	issuer IssuerFunc
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
func NewManager(cfg Config, store Store, params ParamsFunc, issuer IssuerFunc, holder *Holder, logger Logger) (*Manager, error) {
	if cfg.Domain == "" {
		return nil, errors.New("relaycert: domaine requis")
	}
	if store == nil || params == nil || issuer == nil || holder == nil {
		return nil, errors.New("relaycert: stockage, réglages, émetteur et holder requis")
	}
	if cfg.RenewBefore <= 0 {
		cfg.RenewBefore = DefaultRenewBefore
	}
	if cfg.CheckEvery <= 0 {
		cfg.CheckEvery = DefaultCheckEvery
	}
	if cfg.StartupWait <= 0 {
		cfg.StartupWait = DefaultStartupWait
	}
	return &Manager{cfg: cfg, store: store, params: params, issuer: issuer, holder: holder, logger: logger,
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
	return m.install(b)
}

// install vérifie le bundle (paire cert/clé valide, nom couvert) puis le pose dans le holder.
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
	m.status.Serial = serialHex(leaf.SerialNumber)
	m.mu.Unlock()
	return nil
}

// Tick décide et agit : renouvelle si absent, à moins de RenewBefore de l'échéance, ou émis sous un autre
// annuaire ; sous backoff après échec. Rend (true) si un nouveau certificat a été émis et posé.
func (m *Manager) Tick(ctx context.Context) (bool, error) {
	now := m.now()
	p, err := m.params(ctx)
	if err != nil {
		return false, m.fail(ctx, now, fmt.Errorf("relaycert: lecture de la configuration: %w", err))
	}
	if !p.Managed {
		m.checkAlert(now)
		return false, nil
	}

	m.mu.Lock()
	cur := m.bundle
	need := cur == nil || cur.NotAfter.Sub(now) < m.cfg.RenewBefore || cur.Directory != p.Directory
	wait := now.Before(m.nextAttempt)
	var acctKey []byte
	if cur != nil && cur.Directory == p.Directory {
		acctKey = cur.AccountKeyPEM // un compte ACME n'existe que chez son annuaire
	}
	m.mu.Unlock()

	if !need || wait {
		m.checkAlert(now)
		return false, nil
	}
	switch {
	case p.Email == "":
		return false, m.fail(ctx, now, errors.New("relaycert: adresse de contact ACME absente (réglage veridian_relay_acme_email)"))
	case p.CFToken == "":
		return false, m.fail(ctx, now, errors.New("relaycert: jeton DNS Cloudflare absent (réglage encrypted_veridian_relay_cf_token)"))
	case p.DirectoryURL == "":
		return false, m.fail(ctx, now, errors.New("relaycert: annuaire ACME absent"))
	}

	b, err := m.issuer(p).Obtain(ctx, m.cfg.Domain, p.Email, acctKey)
	if err == nil && b == nil {
		err = errors.New("relaycert: l'émetteur n'a rendu aucun certificat")
	}
	if err == nil {
		b.Directory = p.Directory
		err = m.install(b)
	}
	if err == nil {
		err = m.store.Save(ctx, b)
	}
	if err != nil {
		return false, m.fail(ctx, now, err)
	}

	m.mu.Lock()
	m.failures = 0
	m.status.Failures = 0
	m.status.LastError = ""
	m.status.LastRenewed = now
	m.nextAttempt = time.Time{}
	st := State{NotAfter: m.status.NotAfter, Serial: m.status.Serial, LastRenewed: now}
	m.mu.Unlock()
	m.saveState(ctx, st)
	m.logger.WithFields(map[string]interface{}{
		"domain": m.cfg.Domain, "directory": p.Directory, "not_after": b.NotAfter.UTC().Format(time.RFC3339),
	}).Info("relaycert: certificat du relais émis et posé à chaud")
	return true, nil
}

// fail enregistre un échec : compteur, backoff exponentiel plafonné, état publié, journal, alerte.
func (m *Manager) fail(ctx context.Context, now time.Time, err error) error {
	msg := err.Error()
	if len(msg) > maxErrorLen {
		msg = msg[:maxErrorLen]
	}
	m.mu.Lock()
	m.failures++
	m.status.Failures = m.failures
	m.status.LastError = msg
	backoff := baseBackoff << min(m.failures-1, 10)
	if backoff > maxBackoff {
		backoff = maxBackoff
	}
	m.nextAttempt = now.Add(backoff)
	st := State{NotAfter: m.status.NotAfter, Serial: m.status.Serial, LastRenewed: m.status.LastRenewed, LastError: msg}
	failures := m.failures
	m.mu.Unlock()
	m.logger.WithFields(map[string]interface{}{
		"domain": m.cfg.Domain, "failures": failures, "next_attempt_in": backoff.String(), "error": msg,
	}).Error("relaycert: échec du renouvellement du certificat du relais")
	m.saveState(ctx, st)
	m.checkAlert(now)
	return err
}

// saveState publie l'état dans les réglages. Un échec d'écriture est journalisé, sans effet sur le relais.
func (m *Manager) saveState(ctx context.Context, st State) {
	if err := m.store.SaveState(ctx, st); err != nil {
		m.logger.WithField("error", err.Error()).Warn("relaycert: état non publié dans les réglages")
	}
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

// Start charge le bundle puis décide à intervalle jusqu'à l'annulation de ctx. La première décision a lieu
// après StartupWait : un redémarrage en boucle n'émet donc jamais en rafale.
func (m *Manager) Start(ctx context.Context) {
	if err := m.Load(ctx); err != nil {
		m.logger.WithField("error", err.Error()).Error("relaycert: chargement du certificat du relais")
	}
	go func() {
		first := time.NewTimer(m.cfg.StartupWait)
		defer first.Stop()
		select {
		case <-ctx.Done():
			return
		case <-first.C:
			_, _ = m.Tick(ctx)
		}
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

// serialHex rend le numéro de série en hexadécimal majuscules (forme usuelle des autorités).
func serialHex(n *big.Int) string {
	if n == nil {
		return ""
	}
	return fmt.Sprintf("%X", n)
}

// pemBlock encode un bloc PEM (certificat, clé).
func pemBlock(typ string, der []byte) []byte {
	return pem.EncodeToMemory(&pem.Block{Type: typ, Bytes: der})
}
