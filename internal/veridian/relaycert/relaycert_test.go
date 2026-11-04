package relaycert

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"errors"
	"math/big"
	"testing"
	"time"

	"github.com/Notifuse/notifuse/internal/domain"
	"github.com/Notifuse/notifuse/pkg/logger"
)

const testDomain = "relais-smtp.example.test"

// makeBundle fabrique un certificat auto-signé valide pour testDomain, échéance notAfter.
func makeBundle(t *testing.T, notAfter time.Time) *Bundle {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(time.Now().UnixNano()),
		Subject:      pkix.Name{CommonName: testDomain},
		DNSNames:     []string{testDomain},
		NotBefore:    notAfter.Add(-90 * 24 * time.Hour),
		NotAfter:     notAfter,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	keyDER, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	return &Bundle{
		CertPEM:   pemEncode("CERTIFICATE", der),
		KeyPEM:    pemEncode("EC PRIVATE KEY", keyDER),
		NotAfter:  notAfter,
		Directory: "test",
	}
}

type memStore struct {
	b      *Bundle
	saved  int
	states []State
}

func (m *memStore) Load(context.Context) (*Bundle, error) { return m.b, nil }
func (m *memStore) Save(_ context.Context, b *Bundle) error {
	m.b = b
	m.saved++
	return nil
}
func (m *memStore) SaveState(_ context.Context, st State) error {
	m.states = append(m.states, st)
	return nil
}

type fakeIssuer struct {
	calls int
	fail  bool
	next  *Bundle
	last  Params
}

func (f *fakeIssuer) Obtain(context.Context, string, string, []byte) (*Bundle, error) {
	f.calls++
	if f.fail {
		return nil, errors.New("acme: échec simulé")
	}
	return f.next, nil
}

// testParams : configuration complète (géré, annuaire de test) ; les tests la modifient au besoin.
func testParams() Params {
	return Params{Managed: true, Email: "ops@example.test", Directory: "test", DirectoryURL: "https://acme.example.test/dir", CFToken: "jeton-de-test"}
}

func newTestManager(t *testing.T, store Store, issuer Issuer, now time.Time) (*Manager, *Holder) {
	t.Helper()
	return newTestManagerWith(t, store, issuer, now, testParams())
}

func newTestManagerWith(t *testing.T, store Store, issuer Issuer, now time.Time, p Params) (*Manager, *Holder) {
	t.Helper()
	h := &Holder{}
	fi, _ := issuer.(*fakeIssuer)
	params := func(context.Context) (Params, error) { return p, nil }
	factory := func(pp Params) Issuer {
		if fi != nil {
			fi.last = pp
		}
		return issuer
	}
	m, err := NewManager(Config{Domain: testDomain}, store, params, factory, h, logger.NewLogger())
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}
	m.now = func() time.Time { return now }
	return m, h
}

func TestHolder_GetCertificate(t *testing.T) {
	h := &Holder{}
	if _, err := h.GetCertificate(nil); !errors.Is(err, ErrNoCertificate) {
		t.Fatalf("sans certificat : ErrNoCertificate attendu, got %v", err)
	}
	first := makeBundle(t, time.Now().Add(100*24*time.Hour))
	p1, _ := tls.X509KeyPair(first.CertPEM, first.KeyPEM)
	h.Set(p1)
	c1, _ := h.GetCertificate(nil)
	second := makeBundle(t, time.Now().Add(200*24*time.Hour))
	p2, _ := tls.X509KeyPair(second.CertPEM, second.KeyPEM)
	h.Set(p2)
	c2, _ := h.GetCertificate(nil)
	if c1 == c2 {
		t.Fatal("après Set, GetCertificate doit rendre le nouveau certificat (rechargement à chaud)")
	}
}

func TestNewManager(t *testing.T) {
	params := func(context.Context) (Params, error) { return testParams(), nil }
	factory := func(Params) Issuer { return &fakeIssuer{} }
	if _, err := NewManager(Config{}, &memStore{}, params, factory, &Holder{}, logger.NewLogger()); err == nil {
		t.Fatal("domaine absent doit être refusé")
	}
	if _, err := NewManager(Config{Domain: testDomain}, &memStore{}, nil, factory, &Holder{}, logger.NewLogger()); err == nil {
		t.Fatal("réglages absents doivent être refusés")
	}
	m, err := NewManager(Config{Domain: testDomain}, &memStore{}, params, factory, &Holder{}, logger.NewLogger())
	if err != nil || m.cfg.RenewBefore != DefaultRenewBefore || m.cfg.CheckEvery != DefaultCheckEvery || m.cfg.StartupWait != DefaultStartupWait {
		t.Fatalf("défauts attendus (30 j, 1 h, 1 min) : %v %+v", err, m.cfg)
	}
}

// Fail closed : sans contact, sans jeton ou désactivé, aucun appel réseau.
func TestManager_Tick_NoIssuanceWithoutConfig(t *testing.T) {
	now := time.Now()
	cases := map[string]func(p *Params){
		"désactivé":       func(p *Params) { p.Managed = false },
		"email absent":    func(p *Params) { p.Email = "" },
		"jeton absent":    func(p *Params) { p.CFToken = "" },
		"annuaire absent": func(p *Params) { p.DirectoryURL = "" },
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			p := testParams()
			mutate(&p)
			issuer := &fakeIssuer{next: makeBundle(t, now.Add(90*24*time.Hour))}
			m, _ := newTestManagerWith(t, &memStore{}, issuer, now, p)
			renewed, _ := m.Tick(context.Background())
			if renewed || issuer.calls != 0 {
				t.Fatalf("%s : aucune émission attendue (renewed=%v, appels=%d)", name, renewed, issuer.calls)
			}
		})
	}
}

// Changer d'annuaire (test vers production) force une émission : le certificat de test n'est jamais servi
// comme certificat de production, et le compte ACME n'est pas réutilisé d'un annuaire à l'autre.
func TestManager_Tick_DirectoryChangeForcesIssuance(t *testing.T) {
	now := time.Now()
	issuer := &fakeIssuer{next: makeBundle(t, now.Add(90*24*time.Hour))}
	p := testParams()
	p.Directory = "production"
	stored := makeBundle(t, now.Add(90*24*time.Hour)) // émis sous test
	m, _ := newTestManagerWith(t, &memStore{b: stored}, issuer, now, p)
	_ = m.Load(context.Background())
	if renewed, err := m.Tick(context.Background()); !renewed || err != nil || issuer.calls != 1 {
		t.Fatalf("annuaire changé : émission attendue (renewed=%v err=%v calls=%d)", renewed, err, issuer.calls)
	}
}

// Le verdict est publié dans les réglages après chaque décision : série et dernière erreur (jamais de jeton).
func TestManager_Tick_PublishesState(t *testing.T) {
	now := time.Now()
	store := &memStore{}
	issuer := &fakeIssuer{fail: true}
	m, _ := newTestManager(t, store, issuer, now)
	_, _ = m.Tick(context.Background())
	if len(store.states) != 1 || store.states[0].LastError == "" {
		t.Fatalf("échec publié attendu : %+v", store.states)
	}
	issuer.fail = false
	issuer.next = makeBundle(t, now.Add(90*24*time.Hour))
	m.now = func() time.Time { return now.Add(maxBackoff + time.Minute) }
	if _, err := m.Tick(context.Background()); err != nil {
		t.Fatalf("succès attendu : %v", err)
	}
	last := store.states[len(store.states)-1]
	if last.LastError != "" || last.Serial == "" || last.NotAfter.IsZero() || last.LastRenewed.IsZero() {
		t.Fatalf("succès publié attendu avec série et échéance : %+v", last)
	}
}

// Au démarrage, aucune décision (donc aucune émission) avant le délai : un redémarrage en boucle n émet pas en rafale.
func TestManager_Start_WaitsStartupBeforeFirstDecision(t *testing.T) {
	issuer := &fakeIssuer{next: makeBundle(t, time.Now().Add(90*24*time.Hour))}
	m, _ := newTestManager(t, &memStore{}, issuer, time.Now())
	m.cfg.StartupWait = time.Hour
	m.cfg.CheckEvery = time.Millisecond
	ctx, cancel := context.WithCancel(context.Background())
	m.Start(ctx)
	time.Sleep(30 * time.Millisecond)
	cancel()
	if issuer.calls != 0 {
		t.Fatalf("aucune décision avant le délai de démarrage attendue, appels=%d", issuer.calls)
	}
}

func TestManager_Load(t *testing.T) {
	now := time.Now()
	b := makeBundle(t, now.Add(90*24*time.Hour))
	m, h := newTestManager(t, &memStore{b: b}, &fakeIssuer{}, now)
	if err := m.Load(context.Background()); err != nil {
		t.Fatalf("Load: %v", err)
	}
	if _, err := h.GetCertificate(nil); err != nil {
		t.Fatalf("après Load, le certificat doit être servi : %v", err)
	}
	wrong := makeBundle(t, now.Add(90*24*time.Hour))
	wrong.CertPEM = makeBundle(t, now.Add(90*24*time.Hour)).CertPEM
	wrong.KeyPEM = b.KeyPEM
	m2, _ := newTestManager(t, &memStore{b: wrong}, &fakeIssuer{}, now)
	if err := m2.Load(context.Background()); err == nil {
		t.Fatal("paire cert/clé incohérente doit être refusée")
	}
}

func TestManager_Tick_IssuesWhenMissing(t *testing.T) {
	now := time.Now()
	store := &memStore{}
	issuer := &fakeIssuer{next: makeBundle(t, now.Add(90*24*time.Hour))}
	m, h := newTestManager(t, store, issuer, now)
	renewed, err := m.Tick(context.Background())
	if err != nil || !renewed {
		t.Fatalf("sans certificat : émission attendue, renewed=%v err=%v", renewed, err)
	}
	if _, err := h.GetCertificate(nil); err != nil || store.saved != 1 {
		t.Fatalf("certificat posé et stocké attendu (saved=%d, err=%v)", store.saved, err)
	}
}

func TestManager_Tick_NoRenewalWhenFresh(t *testing.T) {
	now := time.Now()
	issuer := &fakeIssuer{next: makeBundle(t, now.Add(90*24*time.Hour))}
	m, _ := newTestManager(t, &memStore{b: makeBundle(t, now.Add(90*24*time.Hour))}, issuer, now)
	_ = m.Load(context.Background())
	if renewed, _ := m.Tick(context.Background()); renewed || issuer.calls != 0 {
		t.Fatalf("certificat frais (90 j) : aucune émission attendue, calls=%d", issuer.calls)
	}
}

func TestManager_Tick_RenewsInsideThreshold(t *testing.T) {
	now := time.Now()
	issuer := &fakeIssuer{next: makeBundle(t, now.Add(90*24*time.Hour))}
	m, _ := newTestManager(t, &memStore{b: makeBundle(t, now.Add(10*24*time.Hour))}, issuer, now)
	_ = m.Load(context.Background())
	if renewed, err := m.Tick(context.Background()); !renewed || err != nil || issuer.calls != 1 {
		t.Fatalf("échéance à 10 j (< 30 j) : renouvellement attendu, renewed=%v err=%v calls=%d", renewed, err, issuer.calls)
	}
}

func TestManager_Tick_BackoffAfterFailure(t *testing.T) {
	now := time.Now()
	issuer := &fakeIssuer{fail: true}
	m, _ := newTestManager(t, &memStore{}, issuer, now)
	if _, err := m.Tick(context.Background()); err == nil {
		t.Fatal("émission en échec : erreur attendue")
	}
	if _, _ = m.Tick(context.Background()); issuer.calls != 1 {
		t.Fatalf("pendant le backoff : aucune nouvelle tentative attendue, calls=%d", issuer.calls)
	}
	st := m.Status()
	if st.Failures != 1 || st.LastError == "" {
		t.Fatalf("échec visible dans le verdict attendu : %+v", st)
	}
	m.now = func() time.Time { return now.Add(maxBackoff + time.Minute) }
	_, _ = m.Tick(context.Background())
	if issuer.calls != 2 {
		t.Fatalf("après le backoff : nouvelle tentative attendue, calls=%d", issuer.calls)
	}
}

func TestManager_Status_ClearsAfterSuccess(t *testing.T) {
	now := time.Now()
	issuer := &fakeIssuer{fail: true}
	m, _ := newTestManager(t, &memStore{}, issuer, now)
	_, _ = m.Tick(context.Background())
	issuer.fail = false
	issuer.next = makeBundle(t, now.Add(90*24*time.Hour))
	m.now = func() time.Time { return now.Add(maxBackoff + time.Minute) }
	if renewed, err := m.Tick(context.Background()); !renewed || err != nil {
		t.Fatalf("succès après échec attendu : renewed=%v err=%v", renewed, err)
	}
	if st := m.Status(); st.Failures != 0 || st.LastError != "" || st.LastRenewed.IsZero() {
		t.Fatalf("verdict remis à zéro après succès attendu : %+v", st)
	}
}

func TestManager_Alert(t *testing.T) {
	now := time.Now()
	b := makeBundle(t, now.Add(5*24*time.Hour))
	issuer := &fakeIssuer{fail: true}
	m, _ := newTestManager(t, &memStore{b: b}, issuer, now)
	_ = m.Load(context.Background())
	// Échéance à 5 j : renouvellement tenté (sous 30 j) et en échec, l'alerte doit être posée.
	_, _ = m.Tick(context.Background())
	if st := m.Status(); st.Alert != "expiration imminente" {
		t.Fatalf("alerte « expiration imminente » attendue à 5 j, got %q", st.Alert)
	}
}

func TestManager_Start_StopsOnCancel(t *testing.T) {
	m, _ := newTestManager(t, &memStore{}, &fakeIssuer{}, time.Now())
	m.cfg.CheckEvery = 5 * time.Millisecond
	m.cfg.StartupWait = time.Millisecond
	ctx, cancel := context.WithCancel(context.Background())
	m.Start(ctx)
	time.Sleep(20 * time.Millisecond)
	cancel()
}

func TestSettingsStore_RoundTripEncrypted(t *testing.T) {
	repo := &fakeSettings{values: map[string]string{}}
	s, err := NewSettingsStore(repo, "secret-key-for-tests-0123456789abcdef")
	if err != nil {
		t.Fatal(err)
	}
	b := makeBundle(t, time.Now().Add(90*24*time.Hour))
	if err := s.Save(context.Background(), b); err != nil {
		t.Fatalf("Save: %v", err)
	}
	if stored := repo.values[settingKey]; stored == "" || stored == string(b.KeyPEM) {
		t.Fatal("le stockage ne doit contenir que du chiffré, jamais la clé en clair")
	}
	got, err := s.Load(context.Background())
	if err != nil || got == nil || string(got.KeyPEM) != string(b.KeyPEM) {
		t.Fatalf("relecture attendue identique : err=%v", err)
	}
}

func TestNewSettingsStore_RequiresPassphrase(t *testing.T) {
	if _, err := NewSettingsStore(&fakeSettings{values: map[string]string{}}, ""); err == nil {
		t.Fatal("SECRET_KEY vide doit être refusé")
	}
}

func TestLegoIssuer_Obtain_RequiresDirectory(t *testing.T) {
	if _, err := (LegoIssuer{CFToken: "x"}).Obtain(context.Background(), testDomain, "a@b.c", nil); err == nil {
		t.Fatal("autorité ACME absente doit être refusée sans appel réseau")
	}
}

func TestLegoIssuer_Obtain_RequiresToken(t *testing.T) {
	if _, err := (LegoIssuer{DirectoryURL: "https://acme.example.test/dir"}).Obtain(context.Background(), testDomain, "a@b.c", nil); err == nil {
		t.Fatal("jeton Cloudflare absent doit être refusé avant tout appel")
	}
}

func TestSettingsStore_SaveStatePublishesReadOnlyKeys(t *testing.T) {
	repo := &fakeSettings{values: map[string]string{}}
	s, err := NewSettingsStore(repo, "secret-key-for-tests-0123456789abcdef")
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 11, 4, 10, 0, 0, 0, time.UTC)
	if err := s.SaveState(context.Background(), State{NotAfter: now, Serial: "ABC", LastRenewed: now, LastError: "boom"}); err != nil {
		t.Fatalf("SaveState: %v", err)
	}
	if repo.values["veridian_relay_cert_serial"] != "ABC" || repo.values["veridian_relay_cert_not_after"] != "2026-11-04T10:00:00Z" ||
		repo.values["veridian_relay_cert_last_error"] != "boom" {
		t.Fatalf("état publié inattendu : %v", repo.values)
	}
}

// fakeSettings implémente domain.SettingRepository en mémoire.
type fakeSettings struct{ values map[string]string }

func (f *fakeSettings) Get(_ context.Context, key string) (*domain.Setting, error) {
	v, ok := f.values[key]
	if !ok {
		return nil, errors.New("absent")
	}
	return &domain.Setting{Key: key, Value: v}, nil
}
func (f *fakeSettings) Set(_ context.Context, key, value string) error {
	f.values[key] = value
	return nil
}
func (f *fakeSettings) Delete(_ context.Context, key string) error      { delete(f.values, key); return nil }
func (f *fakeSettings) List(context.Context) ([]*domain.Setting, error) { return nil, nil }
func (f *fakeSettings) SetLastCronRun(context.Context) error            { return nil }
func (f *fakeSettings) GetLastCronRun(context.Context) (*time.Time, error) {
	return nil, nil
}

func pemEncode(typ string, der []byte) []byte {
	return pemBlock(typ, der)
}

// La précédence « certificat fixe > géré » ne gouverne que ce qui est SERVI : le gestionnaire émet et
// stocke quand même, même si le holder sert déjà un autre certificat (fixe). Sinon le relais prod ne
// disposerait jamais du certificat géré avant de retirer le fixe (trou TLS).
func TestManager_Tick_IssuesAndStoresWhenHolderAlreadyServesFixedCertificate(t *testing.T) {
	now := time.Now()
	store := &memStore{}
	issuer := &fakeIssuer{next: makeBundle(t, now.Add(90*24*time.Hour))}
	m, h := newTestManager(t, store, issuer, now)
	fixed := makeBundle(t, now.Add(200*24*time.Hour))
	pair, err := tls.X509KeyPair(fixed.CertPEM, fixed.KeyPEM)
	if err != nil {
		t.Fatal(err)
	}
	h.Set(pair) // un certificat fixe est déjà servi
	renewed, err := m.Tick(context.Background())
	if err != nil || !renewed {
		t.Fatalf("émission attendue malgré un certificat fixe servi : renewed=%v err=%v", renewed, err)
	}
	if issuer.calls != 1 || store.saved != 1 {
		t.Fatalf("émission et stockage attendus une fois (appels=%d, sauvegardes=%d)", issuer.calls, store.saved)
	}
}
