package iafils

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/Notifuse/notifuse/internal/domain"
)

const (
	hubUser   = "11111111-1111-4111-8111-111111111111"
	hubTenant = "22222222-2222-4222-8222-222222222222"
)

// Vecteur tiré du code du Hub (veridian-hub/lib/delegation/jeton.ts, verifierAppel) :
//
//	createHmac('sha256', secret).update(`${ts}.${nonce}.${corps}`).digest('hex')
//
// calculé par Node le 26/11/2026 avec ces entrées. Si le Go diverge d'un octet, le Hub répondrait 401 invalid_signature.
func TestSignEtat_VecteurDuHub(t *testing.T) {
	const (
		secret = "secret-de-test-vecteur"
		ts     = "1790000000123"
		nonce  = "0123456789abcdef0123456789abcdef"
		corps  = `{"hub_user_id":"11111111-1111-4111-8111-111111111111","tenant_id":"22222222-2222-4222-8222-222222222222"}`
		attend = "4f26cf651be0b481a854d00a1e1845bf8710d3e8de1ce82c8d9de08df360912f"
	)
	assert.Equal(t, attend, SignEtat(secret, ts, nonce, []byte(corps)))
	// Une mesure qui ne bouge pas quand ses entrées bougent ne mesure rien.
	assert.NotEqual(t, attend, SignEtat(secret, ts, nonce, []byte(corps+" ")))
	assert.NotEqual(t, attend, SignEtat(secret, ts, "0123456789abcdef0123456789abcdee", []byte(corps)))
	assert.NotEqual(t, attend, SignEtat(secret, "1790000000124", nonce, []byte(corps)))
	assert.NotEqual(t, attend, SignEtat("autre", ts, nonce, []byte(corps)))
}

// hubFactice rejoue les contrôles de verifierAppel : app admise, ts à 13 chiffres dans la fenêtre, nonce 16-128 [A-Za-z0-9_-], signature
// hex de 64, HMAC sur ts.nonce.corps, nonce à usage unique. Rend l'état demandé et compte les appels.
type hubFactice struct {
	secret  string
	reponse func() (int, string)
	appels  int32
	mu      sync.Mutex
	nonces  map[string]bool
}

var (
	nonceRE = regexp.MustCompile(`^[A-Za-z0-9_-]{16,128}$`)
	tsRE    = regexp.MustCompile(`^\d{13}$`)
	sigRE   = regexp.MustCompile(`^[0-9a-f]{64}$`)
)

func (h *hubFactice) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	atomic.AddInt32(&h.appels, 1)
	corps, _ := io.ReadAll(r.Body)
	app, ts, nonce, sig := r.Header.Get("x-veridian-app"), r.Header.Get("x-veridian-timestamp"), r.Header.Get("x-veridian-nonce"), r.Header.Get("x-veridian-hub-signature")
	if r.Method != http.MethodPost || r.URL.Path != "/api/ia/etat" || app != "notifuse" || !tsRE.MatchString(ts) || !nonceRE.MatchString(nonce) || !sigRE.MatchString(sig) {
		http.Error(w, `{"error":"invalid_headers"}`, 400)
		return
	}
	n, _ := strconv.ParseInt(ts, 10, 64)
	if d := time.Since(time.UnixMilli(n)); d > 5*time.Minute || d < -5*time.Minute {
		http.Error(w, `{"error":"timestamp_expired"}`, 401)
		return
	}
	mac := hmac.New(sha256.New, []byte(h.secret))
	mac.Write([]byte(ts + "." + nonce + "."))
	mac.Write(corps)
	if !hmac.Equal([]byte(hex.EncodeToString(mac.Sum(nil))), []byte(sig)) {
		http.Error(w, `{"error":"invalid_signature"}`, 401)
		return
	}
	h.mu.Lock()
	if h.nonces == nil {
		h.nonces = map[string]bool{}
	}
	rejoue := h.nonces[nonce]
	h.nonces[nonce] = true
	h.mu.Unlock()
	if rejoue {
		http.Error(w, `{"error":"nonce_replayed"}`, 401)
		return
	}
	var c struct {
		HubUserID string `json:"hub_user_id"`
		TenantID  string `json:"tenant_id"`
	}
	_ = json.Unmarshal(corps, &c)
	if c.HubUserID != hubUser || c.TenantID != hubTenant {
		http.Error(w, `{"error":"invalid_request"}`, 400)
		return
	}
	code, body := h.reponse()
	w.WriteHeader(code)
	_, _ = io.WriteString(w, body)
}

func clientVers(t *testing.T, h *hubFactice) (*EtatClient, *time.Time) {
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	now := time.Now()
	c := &EtatClient{BaseURL: srv.URL, Secret: h.secret}
	c.Now = func() time.Time { return now }
	return c, &now
}

func TestEtatClient_LesTroisEtatsEtLeSilence(t *testing.T) {
	ctx := context.Background()
	cas := map[string]struct {
		code   int
		body   string
		attend string
	}{
		"prete":             {200, `{"etat":"prete"}`, EtatPrete},
		"sans_moteur":       {200, `{"etat":"sans_moteur"}`, EtatSansMoteur},
		"eteinte":           {200, `{"etat":"eteinte"}`, EtatEteinte},
		"404 espace":        {404, `{"error":"espace_inconnu"}`, EtatIndisponible},
		"5xx":               {503, `{}`, EtatIndisponible},
		"429":               {429, `{}`, EtatIndisponible},
		"illisible":         {200, `pas du json`, EtatIndisponible},
		"état inconnu":      {200, `{"etat":"autre"}`, EtatIndisponible},
		"réponse sans état": {200, `{}`, EtatIndisponible},
	}
	for nom, c := range cas {
		t.Run(nom, func(t *testing.T) {
			h := &hubFactice{secret: "s", reponse: func() (int, string) { return c.code, c.body }}
			cl, _ := clientVers(t, h)
			assert.Equal(t, c.attend, cl.Etat(ctx, hubUser, hubTenant))
		})
	}
}

func TestEtatClient_SignatureAcceptéeParLeVerifieurDuHub_EtRefuséeAvecUnAutreSecret(t *testing.T) {
	h := &hubFactice{secret: "le-secret", reponse: func() (int, string) { return 200, `{"etat":"prete"}` }}
	bon, _ := clientVers(t, h)
	assert.Equal(t, EtatPrete, bon.Etat(context.Background(), hubUser, hubTenant), "un contrôle positif : le vérificateur accepte la bonne signature")
	mauvais, _ := clientVers(t, h)
	mauvais.Secret = "un-autre"
	assert.Equal(t, EtatIndisponible, mauvais.Etat(context.Background(), hubUser, hubTenant), "401 du Hub : l'app se tait")
	assert.EqualValues(t, 2, h.appels)
}

func TestEtatClient_Cache60sParPersonneEtOrganisation(t *testing.T) {
	ctx := context.Background()
	etat := "sans_moteur"
	h := &hubFactice{secret: "s", reponse: func() (int, string) { return 200, `{"etat":"` + etat + `"}` }}
	cl, now := clientVers(t, h)
	assert.Equal(t, EtatSansMoteur, cl.Etat(ctx, hubUser, hubTenant))
	etat = "prete"
	*now = now.Add(59 * time.Second)
	assert.Equal(t, EtatSansMoteur, cl.Etat(ctx, hubUser, hubTenant), "à 59 s : gardé")
	assert.EqualValues(t, 1, h.appels)
	*now = now.Add(2 * time.Second)
	assert.Equal(t, EtatPrete, cl.Etat(ctx, hubUser, hubTenant), "au-delà de 60 s : redemandé")
	assert.EqualValues(t, 2, h.appels)
}

func TestEtatClient_NeGardePasLesEchecs(t *testing.T) {
	ctx := context.Background()
	code := 503
	h := &hubFactice{secret: "s", reponse: func() (int, string) { return code, `{"etat":"prete"}` }}
	cl, _ := clientVers(t, h)
	assert.Equal(t, EtatIndisponible, cl.Etat(ctx, hubUser, hubTenant))
	code = 200
	assert.Equal(t, EtatPrete, cl.Etat(ctx, hubUser, hubTenant), "un 5xx ne se garde pas : la reprise est immédiate")
}

func TestEtatClient_HubInjoignableOuSansSecret(t *testing.T) {
	ctx := context.Background()
	assert.Equal(t, EtatIndisponible, (&EtatClient{BaseURL: "http://127.0.0.1:1", Secret: "s"}).Etat(ctx, hubUser, hubTenant))
	assert.Equal(t, EtatIndisponible, (&EtatClient{BaseURL: "http://x", Secret: ""}).Etat(ctx, hubUser, hubTenant))
}

type fauxEtatStore struct {
	user, tenant string
	errUser      error
}

func (f fauxEtatStore) HubUserOf(context.Context, string) (string, error) { return f.user, f.errUser }
func (f fauxEtatStore) HubTenantOf(context.Context, string) (string, error) {
	return f.tenant, nil
}

type fauxAuth struct{ err error }

func (f fauxAuth) AuthenticateUserForWorkspace(ctx context.Context, _ string) (context.Context, *domain.User, *domain.UserWorkspace, error) {
	if f.err != nil {
		return ctx, nil, nil, f.err
	}
	return ctx, &domain.User{ID: "u-app"}, &domain.UserWorkspace{}, nil
}

type fauxClient struct {
	etat    string
	demande [2]string
}

func (f *fauxClient) Etat(_ context.Context, u, t string) string {
	f.demande = [2]string{u, t}
	return f.etat
}

func appelerEtat(t *testing.T, h *EtatHandler, url string) (int, map[string]any) {
	t.Helper()
	rec := httptest.NewRecorder()
	h.Get(rec, httptest.NewRequest(http.MethodGet, url, nil))
	var out map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &out)
	assert.Equal(t, "no-store", rec.Header().Get("Cache-Control"))
	return rec.Code, out
}

// Le tenant_id rendu à la console est l'organisation du Hub liée à l'espace, jamais l'identifiant d'espace (CONTRAT-HUB §5.24).
func TestEtatHandler_TenantEstLOrganisationDuHub_JamaisLEspace(t *testing.T) {
	c := &fauxClient{etat: EtatSansMoteur}
	h := &EtatHandler{Store: fauxEtatStore{user: "AAAAAAAA-1111-4111-8111-111111111111", tenant: "BBBBBBBB-2222-4222-8222-222222222222"}, Auth: fauxAuth{}, Client: c, HubURL: "https://app.veridian.site/"}
	code, out := appelerEtat(t, h, "/api/veridian/ia/etat?workspace_id=espace-1")
	assert.Equal(t, 200, code)
	assert.Equal(t, "sans_moteur", out["etat"])
	assert.Equal(t, "bbbbbbbb-2222-4222-8222-222222222222", out["tenant_id"])
	assert.NotEqual(t, "espace-1", out["tenant_id"])
	assert.Equal(t, "https://app.veridian.site", out["hub_url"])
	assert.Equal(t, [2]string{"aaaaaaaa-1111-4111-8111-111111111111", "bbbbbbbb-2222-4222-8222-222222222222"}, c.demande)
}

func TestEtatHandler_RienAfficherSansEtatUtile(t *testing.T) {
	for nom, etat := range map[string]string{"éteinte": EtatEteinte, "indisponible": EtatIndisponible} {
		t.Run(nom, func(t *testing.T) {
			h := &EtatHandler{Store: fauxEtatStore{user: hubUser, tenant: hubTenant}, Auth: fauxAuth{}, Client: &fauxClient{etat: etat}, HubURL: "https://hub"}
			_, out := appelerEtat(t, h, "/x?workspace_id=w")
			assert.Equal(t, etat, out["etat"])
			assert.NotContains(t, out, "tenant_id")
			assert.NotContains(t, out, "hub_url")
		})
	}
	// Espace non relié, compte non relié, lecture en panne : indisponible, le Hub n'est même pas interrogé.
	for nom, st := range map[string]fauxEtatStore{"espace non relié": {user: hubUser}, "compte non relié": {tenant: hubTenant}, "base en panne": {errUser: errors.New("x"), tenant: hubTenant}} {
		t.Run(nom, func(t *testing.T) {
			c := &fauxClient{etat: EtatPrete}
			h := &EtatHandler{Store: st, Auth: fauxAuth{}, Client: c, HubURL: "https://hub"}
			_, out := appelerEtat(t, h, "/x?workspace_id=w")
			assert.Equal(t, EtatIndisponible, out["etat"])
			assert.Equal(t, [2]string{}, c.demande)
		})
	}
}

func TestEtatHandler_RefusesSansEspaceOuHorsEspace(t *testing.T) {
	h := &EtatHandler{Store: fauxEtatStore{}, Auth: fauxAuth{}, Client: &fauxClient{}}
	code, _ := appelerEtat(t, h, "/x")
	assert.Equal(t, 400, code)
	h.Auth = fauxAuth{err: errors.New("pas membre")}
	code, _ = appelerEtat(t, h, "/x?workspace_id=w")
	assert.Equal(t, 403, code)
}

func TestNonceEtTimestamp_FormatDuHub(t *testing.T) {
	n, err := newNonce()
	require.NoError(t, err)
	assert.Regexp(t, nonceRE, n)
	n2, _ := newNonce()
	assert.NotEqual(t, n, n2)
}
