package service

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Notifuse/notifuse/internal/domain"
	"github.com/Notifuse/notifuse/internal/domain/mocks"
	"github.com/Notifuse/notifuse/internal/service/broadcast"
	"github.com/golang/mock/gomock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Veridian fork — lot 0 (2026-10-10). Ces tests ont été écrits CONTRE l'ancien
// code (nœud webhook sans garde, secret en clair) : chacun doit y échouer.
// Voir docs/claude/61-webhook-node-ssrf-secret.md pour la matrice.

const lotZeroPassphrase = "lot0-test-passphrase-32-chars!!!"

// trustWebhookTestServer fait accepter au nœud webhook d'un exécuteur le
// serveur HTTPS local d'un test. Il remplace le client gardé (qui refuse
// justement 127.0.0.1) : réservé aux tests de comportement (statuts, JSON...),
// jamais à ceux de la garde.
func trustWebhookTestServer(executor *AutomationExecutor, server *httptest.Server) {
	if w, ok := executor.nodeExecutors[domain.NodeTypeWebhook].(*WebhookNodeExecutor); ok {
		w.httpClient = server.Client()
	}
}

// guardEnv branche la VRAIE garde (résolution, refus, épinglage) sur un
// serveur HTTPS local : la connexion finale, une fois la garde passée, est
// redirigée vers le serveur du test, et le certificat du serveur est reconnu.
type guardEnv struct {
	server *httptest.Server
	port   string
	mu     sync.Mutex
	dialed []string // adresses ip:port réellement ouvertes APRÈS la garde
	hits   int32    // requêtes reçues par le serveur
}

func (g *guardEnv) dialedAddrs() []string {
	g.mu.Lock()
	defer g.mu.Unlock()
	return append([]string(nil), g.dialed...)
}

func newGuardEnv(t *testing.T, handler func(w http.ResponseWriter, r *http.Request, g *guardEnv)) *guardEnv {
	t.Helper()
	g := &guardEnv{}
	g.server = httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&g.hits, 1)
		handler(w, r, g)
	}))
	t.Cleanup(g.server.Close)
	_, g.port, _ = net.SplitHostPort(g.server.Listener.Addr().String())

	pool := x509.NewCertPool()
	pool.AddCert(g.server.Certificate())

	origTLS, origDial := outboundTLSConfig, ssrfFinalDial
	// ServerName fixe : le certificat de httptest vaut pour example.com, quel
	// que soit le nom de test (rebind.test, ...) que la garde résout.
	outboundTLSConfig = &tls.Config{RootCAs: pool, ServerName: "example.com"}
	ssrfFinalDial = func(ctx context.Context, base *net.Dialer, network, addr string) (net.Conn, error) {
		g.mu.Lock()
		g.dialed = append(g.dialed, addr)
		g.mu.Unlock()
		return base.DialContext(ctx, network, g.server.Listener.Addr().String())
	}
	t.Cleanup(func() { outboundTLSConfig, ssrfFinalDial = origTLS, origDial })
	return g
}

func newWebhookExecutorForTest(t *testing.T) *WebhookNodeExecutor {
	t.Helper()
	ctrl := gomock.NewController(t)
	t.Cleanup(ctrl.Finish)
	e := NewWebhookNodeExecutor(setupMockLoggerForNodeExecutor(ctrl)) // constructeur de PRODUCTION
	e.SetSecretKey(lotZeroPassphrase)
	return e
}

func runWebhookNode(e *WebhookNodeExecutor, cfg map[string]interface{}) (*NodeExecutionResult, error) {
	return e.Execute(context.Background(), NodeExecutionParams{
		WorkspaceID: "ws1",
		Node:        &domain.AutomationNode{ID: "wh1", Type: domain.NodeTypeWebhook, Config: cfg},
		Contact:     &domain.ContactAutomation{ID: "ca1", ContactEmail: "lead@example.org"},
		ContactData: &domain.Contact{Email: "lead@example.org"},
		Automation:  &domain.Automation{ID: "auto1", Name: "Auto"},
	})
}

func TestOutboundGuard_BlockedRanges(t *testing.T) {
	blocked := []string{
		"127.0.0.1", "10.1.2.3", "172.16.0.1", "172.31.255.254", "192.168.0.1",
		"169.254.169.254", "169.254.0.1",
		"100.64.0.1", "100.127.255.254", // CGNAT / Tailscale
		"0.0.0.0", "224.0.0.1", "255.255.255.255", "240.0.0.1",
		"198.18.0.1", "192.0.0.1", "192.0.2.1", "198.51.100.7", "203.0.113.9",
		"::1", "::", "fc00::1", "fd00::1", "fe80::1", "fec0::1", "::7f00:1",
		"::ffff:127.0.0.1", "::ffff:169.254.169.254",
		"64:ff9b::7f00:1",    // NAT64 vers 127.0.0.1
		"64:ff9b::a9fe:a9fe", // NAT64 vers 169.254.169.254
		"2002:7f00:1::",      // 6to4 vers 127.0.0.1
		"2002:a00:1::",       // 6to4 vers 10.0.0.1
	}
	for _, s := range blocked {
		ip := net.ParseIP(s)
		require.NotNil(t, ip, s)
		assert.True(t, isBlockedWebhookIP(ip), "%s doit être refusée", s)
	}
	for _, s := range []string{"93.184.216.34", "8.8.8.8", "1.1.1.1", "100.63.255.255", "100.128.0.1", "2606:4700:4700::1111", "64:ff9b::808:808"} {
		assert.False(t, isBlockedWebhookIP(net.ParseIP(s)), "%s est publique", s)
	}
}

func TestWebhookNode_SSRF_LiteralTargetsRefused(t *testing.T) {
	g := newGuardEnv(t, func(w http.ResponseWriter, r *http.Request, g *guardEnv) { w.WriteHeader(200) })
	e := newWebhookExecutorForTest(t)

	targets := []string{
		"https://127.0.0.1:" + g.port + "/hook", // serveur réellement joignable : ne doit PAS être touché
		"https://10.0.0.5/hook",
		"https://172.16.3.4/hook",
		"https://192.168.1.10/hook",
		"https://169.254.169.254/latest/meta-data/",
		"https://100.64.0.1/hook",
		"https://100.100.100.100/hook",
		"https://[::1]/hook",
		"https://[fd00::1]/hook",
		"https://[fe80::1]/hook",
		"https://[::ffff:127.0.0.1]/hook",
		"https://[64:ff9b::7f00:1]/hook",
	}
	for _, target := range targets {
		t.Run(target, func(t *testing.T) {
			before := atomic.LoadInt32(&g.hits)
			_, err := runWebhookNode(e, map[string]interface{}{"url": target})
			require.Error(t, err)
			assert.True(t,
				strings.Contains(err.Error(), "disallowed address") || strings.Contains(err.Error(), "refusing to connect"),
				"erreur inattendue: %v", err)
			assert.Equal(t, before, atomic.LoadInt32(&g.hits), "le serveur interne ne doit recevoir AUCUNE requête")
		})
	}
	assert.Empty(t, g.dialedAddrs(), "aucune connexion ne doit être ouverte vers une cible interne")
}

func TestWebhookNode_SSRF_HostnameResolvingToInternalRefused(t *testing.T) {
	g := newGuardEnv(t, func(w http.ResponseWriter, r *http.Request, g *guardEnv) { w.WriteHeader(200) })
	withResolverOverride(t, map[string][]net.IP{
		"localhost":        {net.ParseIP("127.0.0.1")},
		"to-loopback.test": {net.ParseIP("127.0.0.1")},
		"to-metadata.test": {net.ParseIP("169.254.169.254")},
		"to-tailnet.test":  {net.ParseIP("100.88.202.29")}, // prod de la flotte
		"to-private.test":  {net.ParseIP("10.0.0.5")},
		"to-ula.test":      {net.ParseIP("fd12:3456::1")},
		"to-v6ll.test":     {net.ParseIP("fe80::1")},
		"to-nat64.test":    {net.ParseIP("64:ff9b::7f00:1")},
	})
	e := newWebhookExecutorForTest(t)
	for _, host := range []string{"localhost", "to-loopback.test", "to-metadata.test", "to-tailnet.test", "to-private.test", "to-ula.test", "to-v6ll.test", "to-nat64.test"} {
		t.Run(host, func(t *testing.T) {
			_, err := runWebhookNode(e, map[string]interface{}{"url": "https://" + host + ":" + g.port + "/hook"})
			require.Error(t, err)
			assert.Contains(t, err.Error(), "refusing to connect")
		})
	}
	assert.Empty(t, g.dialedAddrs())
	assert.EqualValues(t, 0, atomic.LoadInt32(&g.hits))
}

func TestWebhookNode_PublicTargetAccepted_PositiveControl(t *testing.T) {
	// Sans ce contrôle positif, « tout est refusé » passerait pour « la garde marche ».
	g := newGuardEnv(t, func(w http.ResponseWriter, r *http.Request, g *guardEnv) {
		w.Write([]byte(`{"ok":true}`))
	})
	e := newWebhookExecutorForTest(t)
	res, err := runWebhookNode(e, map[string]interface{}{"url": "https://example.com:" + g.port + "/hook"})
	require.NoError(t, err)
	assert.Equal(t, 200, res.Output["status_code"])
	assert.Equal(t, []string{"93.184.216.34:" + g.port}, g.dialedAddrs(), "connexion épinglée sur l'IP publique résolue")
}

func TestWebhookNode_RefusesPlainHTTP(t *testing.T) {
	var hit int32
	plain := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { atomic.AddInt32(&hit, 1) }))
	defer plain.Close()
	e := newWebhookExecutorForTest(t)
	_, err := runWebhookNode(e, map[string]interface{}{"url": plain.URL})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "https")
	assert.EqualValues(t, 0, atomic.LoadInt32(&hit))

	_, err = runWebhookNode(e, map[string]interface{}{"url": "https://user:pass@example.com/hook"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "credentials")
}

func TestWebhookNode_SSRF_RedirectRevalidated(t *testing.T) {
	withResolverOverride(t, map[string][]net.IP{
		"internal.evil.test": {net.ParseIP("10.0.0.5")},
		"tailnet.evil.test":  {net.ParseIP("100.108.136.89")}, // bastion
	})
	cases := map[string]struct {
		location string
		want     string
	}{
		"vers un nom interne":    {"https://internal.evil.test/x", "refusing to connect"},
		"vers la métadonnée":     {"https://169.254.169.254/latest/meta-data/", "refusing to connect"},
		"vers le tailnet":        {"https://tailnet.evil.test/x", "refusing to connect"},
		"vers loopback v6":       {"https://[::1]/x", "refusing to connect"},
		"vers ULA":               {"https://[fd00::1]/x", "refusing to connect"},
		"vers http (rétrograde)": {"http://example.com/x", "https is required"},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			g := newGuardEnv(t, func(w http.ResponseWriter, r *http.Request, g *guardEnv) {
				http.Redirect(w, r, tc.location, http.StatusTemporaryRedirect)
			})
			e := newWebhookExecutorForTest(t)
			_, err := runWebhookNode(e, map[string]interface{}{"url": "https://example.com:" + g.port + "/start"})
			require.Error(t, err)
			assert.Contains(t, err.Error(), tc.want)
			assert.EqualValues(t, 1, atomic.LoadInt32(&g.hits), "seul le premier saut est servi")
			assert.Len(t, g.dialedAddrs(), 1, "aucune connexion vers la cible de la redirection")
		})
	}
}

func TestWebhookNode_SSRF_RedirectLoopBounded(t *testing.T) {
	g := newGuardEnv(t, func(w http.ResponseWriter, r *http.Request, g *guardEnv) {
		http.Redirect(w, r, r.URL.Path+"x", http.StatusFound)
	})
	e := newWebhookExecutorForTest(t)
	_, err := runWebhookNode(e, map[string]interface{}{"url": "https://example.com:" + g.port + "/r"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "stopped after 3 redirects")
	assert.LessOrEqual(t, atomic.LoadInt32(&g.hits), int32(4))
}

func TestWebhookNode_SSRF_DNSRebindingDefeated(t *testing.T) {
	// Le nom répond une IP publique au premier appel, puis 127.0.0.1.
	// La garde résout UNE fois par connexion et épingle l'IP vérifiée.
	var lookups int32
	orig := lookupIPAddrFn
	lookupIPAddrFn = func(ctx context.Context, host string) ([]net.IP, error) {
		if host != "rebind.test" {
			return orig(ctx, host)
		}
		if atomic.AddInt32(&lookups, 1) == 1 {
			return []net.IP{net.ParseIP("93.184.216.34")}, nil
		}
		return []net.IP{net.ParseIP("127.0.0.1")}, nil
	}
	t.Cleanup(func() { lookupIPAddrFn = orig })

	t.Run("première résolution publique : épinglée, une seule résolution", func(t *testing.T) {
		atomic.StoreInt32(&lookups, 0)
		g := newGuardEnv(t, func(w http.ResponseWriter, r *http.Request, g *guardEnv) { w.Write([]byte(`{}`)) })
		_, err := runWebhookNode(newWebhookExecutorForTest(t), map[string]interface{}{"url": "https://rebind.test:" + g.port + "/h"})
		require.NoError(t, err)
		assert.EqualValues(t, 1, atomic.LoadInt32(&lookups), "une seule résolution entre contrôle et connexion")
		assert.Equal(t, []string{"93.184.216.34:" + g.port}, g.dialedAddrs())
	})

	t.Run("le nom bascule vers loopback : refusé", func(t *testing.T) {
		atomic.StoreInt32(&lookups, 1) // le prochain appel renverra 127.0.0.1
		g := newGuardEnv(t, func(w http.ResponseWriter, r *http.Request, g *guardEnv) { w.Write([]byte(`{}`)) })
		_, err := runWebhookNode(newWebhookExecutorForTest(t), map[string]interface{}{"url": "https://rebind.test:" + g.port + "/h"})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "refusing to connect")
		assert.Empty(t, g.dialedAddrs())
	})

	t.Run("réponse mixte public + interne : seule l'IP publique est utilisée", func(t *testing.T) {
		withResolverOverride(t, map[string][]net.IP{
			"mixed.test": {net.ParseIP("10.0.0.5"), net.ParseIP("93.184.216.34"), net.ParseIP("127.0.0.1")},
		})
		g := newGuardEnv(t, func(w http.ResponseWriter, r *http.Request, g *guardEnv) { w.Write([]byte(`{}`)) })
		_, err := runWebhookNode(newWebhookExecutorForTest(t), map[string]interface{}{"url": "https://mixed.test:" + g.port + "/h"})
		require.NoError(t, err)
		assert.Equal(t, []string{"93.184.216.34:" + g.port}, g.dialedAddrs())
	})
}

func TestWebhookNode_ResponseAndTimeBounded(t *testing.T) {
	t.Run("réponse tronquée à 10 Ko", func(t *testing.T) {
		g := newGuardEnv(t, func(w http.ResponseWriter, r *http.Request, g *guardEnv) {
			_, _ = w.Write([]byte(strings.Repeat("x", 2<<20)))
		})
		res, err := runWebhookNode(newWebhookExecutorForTest(t), map[string]interface{}{"url": "https://example.com:" + g.port + "/big"})
		require.NoError(t, err)
		raw := res.Output["response"].(map[string]interface{})["raw"].(string)
		assert.LessOrEqual(t, len(raw), webhookNodeMaxResponseBytes)
	})
	t.Run("receveur lent : délai borné", func(t *testing.T) {
		g := newGuardEnv(t, func(w http.ResponseWriter, r *http.Request, g *guardEnv) { time.Sleep(2 * time.Second) })
		client := NewTenantOutboundClient(200 * time.Millisecond)
		start := time.Now()
		_, err := client.Get("https://example.com:" + g.port + "/slow")
		require.Error(t, err)
		assert.Less(t, time.Since(start), 1500*time.Millisecond)
	})
	t.Run("client de production plafonné à 10 s", func(t *testing.T) {
		e := NewWebhookNodeExecutor(nil)
		assert.Equal(t, TenantOutboundTimeout, e.httpClient.Timeout)
		assert.LessOrEqual(t, TenantOutboundTimeout, 10*time.Second)
	})
}

// verifyStandardWebhook est le code d'un RECEVEUR (celui que la fiche 61
// documente), écrit sans réutiliser signPayload.
func verifyStandardWebhook(secret string, h http.Header, body []byte, now time.Time, tolerance time.Duration) error {
	id, ts, sig := h.Get("webhook-id"), h.Get("webhook-timestamp"), h.Get("webhook-signature")
	if id == "" || ts == "" || sig == "" {
		return fmt.Errorf("en-têtes de signature manquants")
	}
	sec, err := strconv.ParseInt(ts, 10, 64)
	if err != nil {
		return err
	}
	if d := now.Sub(time.Unix(sec, 0)); d > tolerance || d < -tolerance {
		return fmt.Errorf("horodatage hors tolérance")
	}
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(id + "." + ts + "."))
	mac.Write(body)
	want := "v1," + base64.StdEncoding.EncodeToString(mac.Sum(nil))
	for _, cand := range strings.Fields(sig) {
		if hmac.Equal([]byte(cand), []byte(want)) {
			return nil
		}
	}
	return fmt.Errorf("signature invalide")
}

func TestWebhookNode_SignedWithHMAC_Verifiable(t *testing.T) {
	const clearSecret = "s3cret-recepteur-9f2c"
	node := &domain.AutomationNode{ID: "wh1", Type: domain.NodeTypeWebhook, Config: map[string]interface{}{
		"url": "https://example.com/hook", "secret": clearSecret,
	}}
	require.NoError(t, domain.ApplyWebhookNodeSecretsOnSave([]*domain.AutomationNode{node}, nil, lotZeroPassphrase))

	var gotHeaders http.Header
	var gotBody []byte
	g := newGuardEnv(t, func(w http.ResponseWriter, r *http.Request, g *guardEnv) {
		gotHeaders = r.Header.Clone()
		gotBody, _ = io.ReadAll(r.Body)
		w.Write([]byte(`{}`))
	})
	node.Config["url"] = "https://example.com:" + g.port + "/hook"
	_, err := runWebhookNode(newWebhookExecutorForTest(t), node.Config)
	require.NoError(t, err)

	require.NoError(t, verifyStandardWebhook(clearSecret, gotHeaders, gotBody, time.Now(), 5*time.Minute))
	assert.Error(t, verifyStandardWebhook("autre-secret", gotHeaders, gotBody, time.Now(), 5*time.Minute), "mauvais secret refusé")
	assert.Error(t, verifyStandardWebhook(clearSecret, gotHeaders, append(append([]byte{}, gotBody...), ' '), time.Now(), 5*time.Minute), "corps modifié refusé")
	assert.Error(t, verifyStandardWebhook(clearSecret, gotHeaders, gotBody, time.Now().Add(time.Hour), 5*time.Minute), "rejeu tardif refusé")

	assert.Empty(t, gotHeaders.Get("Authorization"), "le secret ne voyage plus en clair dans Authorization")
	for k, v := range gotHeaders {
		assert.NotContains(t, strings.Join(v, ","), clearSecret, "le secret ne doit apparaître dans aucun en-tête (%s)", k)
	}
	assert.NotContains(t, string(gotBody), clearSecret)
}

func TestWebhookNode_NoSecret_NotSigned_AndEncryptedSecretNeedsKey(t *testing.T) {
	var gotHeaders http.Header
	g := newGuardEnv(t, func(w http.ResponseWriter, r *http.Request, g *guardEnv) {
		gotHeaders = r.Header.Clone()
		w.Write([]byte(`{}`))
	})
	_, err := runWebhookNode(newWebhookExecutorForTest(t), map[string]interface{}{"url": "https://example.com:" + g.port + "/h"})
	require.NoError(t, err)
	assert.Empty(t, gotHeaders.Get("webhook-signature"))

	// secret chiffré mais pas de passphrase côté exécuteur : échec fermé, rien n'est envoyé.
	node := &domain.AutomationNode{ID: "wh1", Type: domain.NodeTypeWebhook, Config: map[string]interface{}{"url": "https://example.com/h", "secret": "abc"}}
	require.NoError(t, domain.ApplyWebhookNodeSecretsOnSave([]*domain.AutomationNode{node}, nil, lotZeroPassphrase))
	node.Config["url"] = "https://example.com:" + g.port + "/h"
	before := atomic.LoadInt32(&g.hits)
	e := newWebhookExecutorForTest(t)
	e.SetSecretKey("")
	_, err = runWebhookNode(e, node.Config)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "encryption key")
	assert.Equal(t, before, atomic.LoadInt32(&g.hits))
}

// ---------------------------------------------------------------------------
// Service d'automations : le secret n'est ni stocké en clair ni renvoyé.
// ---------------------------------------------------------------------------

func newSecretAutomationService(t *testing.T) (*AutomationService, *mocks.MockAutomationRepository) {
	t.Helper()
	ctrl := gomock.NewController(t)
	t.Cleanup(ctrl.Finish)
	repo := mocks.NewMockAutomationRepository(ctrl)
	auth := mocks.NewMockAuthService(ctrl)
	auth.EXPECT().AuthenticateUserForWorkspace(gomock.Any(), "ws1").Return(context.Background(), &domain.User{}, &domain.UserWorkspace{
		WorkspaceID: "ws1", Permissions: domain.FullPermissions,
	}, nil).AnyTimes()
	svc := NewAutomationService(repo, auth, setupMockLoggerForNodeExecutor(ctrl), AutomationLifecycleDependencies{SecretKey: lotZeroPassphrase})
	return svc, repo
}

func automationWithWebhook(cfg map[string]interface{}) *domain.Automation {
	a := createTestAutomationService("auto-1", "ws1")
	a.RootNodeID = "wh1"
	a.Nodes = []*domain.AutomationNode{{ID: "wh1", AutomationID: "auto-1", Type: domain.NodeTypeWebhook, Config: cfg}}
	return a
}

func jsonOf(t *testing.T, v interface{}) string {
	t.Helper()
	b, err := json.Marshal(v)
	require.NoError(t, err)
	return string(b)
}

func TestAutomationService_WebhookSecret_EncryptedAtRest_NeverReturned(t *testing.T) {
	const clearSecret = "ultra-secret-value-42"

	t.Run("create: chiffré en base, absent de la réponse", func(t *testing.T) {
		svc, repo := newSecretAutomationService(t)
		var stored string
		repo.EXPECT().Create(gomock.Any(), "ws1", gomock.Any()).DoAndReturn(func(_ context.Context, _ string, a *domain.Automation) error {
			stored = jsonOf(t, a.Nodes) // ce que la base reçoit
			return nil
		})
		a := automationWithWebhook(map[string]interface{}{"url": "https://hooks.example.com/x", "secret": clearSecret})
		require.NoError(t, svc.Create(context.Background(), "ws1", a))

		assert.NotContains(t, stored, clearSecret, "jamais en clair au repos")
		assert.Contains(t, stored, "secret_encrypted")
		resp := jsonOf(t, a)
		assert.NotContains(t, resp, clearSecret)
		assert.NotContains(t, resp, "secret_encrypted")
		assert.Contains(t, resp, `"has_secret":true`)
	})

	t.Run("get et list: redaction, has_secret", func(t *testing.T) {
		svc, repo := newSecretAutomationService(t)
		mk := func() *domain.Automation {
			enc := &domain.AutomationNode{ID: "wh1", Type: domain.NodeTypeWebhook, Config: map[string]interface{}{"url": "https://h.example.com/x", "secret": clearSecret}}
			require.NoError(t, domain.ApplyWebhookNodeSecretsOnSave([]*domain.AutomationNode{enc}, nil, lotZeroPassphrase))
			legacy := &domain.AutomationNode{ID: "wh2", Type: domain.NodeTypeWebhook, Config: map[string]interface{}{"url": "https://h.example.com/y", "secret": "legacy-clear-secret"}}
			none := &domain.AutomationNode{ID: "wh3", Type: domain.NodeTypeWebhook, Config: map[string]interface{}{"url": "https://h.example.com/z"}}
			return &domain.Automation{ID: "auto-1", Nodes: []*domain.AutomationNode{enc, legacy, none}}
		}
		repo.EXPECT().GetByID(gomock.Any(), "ws1", "auto-1").Return(mk(), nil)
		repo.EXPECT().List(gomock.Any(), "ws1", gomock.Any()).Return([]*domain.Automation{mk()}, 1, nil)

		got, err := svc.Get(context.Background(), "ws1", "auto-1")
		require.NoError(t, err)
		list, _, err := svc.List(context.Background(), "ws1", domain.AutomationFilter{})
		require.NoError(t, err)
		for _, resp := range []string{jsonOf(t, got), jsonOf(t, list)} {
			assert.NotContains(t, resp, clearSecret)
			assert.NotContains(t, resp, "legacy-clear-secret")
			assert.NotContains(t, resp, "secret_encrypted")
			assert.NotContains(t, resp, `"secret"`)
			assert.Equal(t, 2, strings.Count(resp, `"has_secret":true`))
			assert.Equal(t, 1, strings.Count(resp, `"has_secret":false`))
		}
	})

	t.Run("update sans secret: l'ancien est conservé (chiffré)", func(t *testing.T) {
		svc, repo := newSecretAutomationService(t)
		prev := &domain.AutomationNode{ID: "wh1", Type: domain.NodeTypeWebhook, Config: map[string]interface{}{"url": "https://h.example.com/x", "secret": clearSecret}}
		require.NoError(t, domain.ApplyWebhookNodeSecretsOnSave([]*domain.AutomationNode{prev}, nil, lotZeroPassphrase))
		prevEnc := prev.Config["secret_encrypted"].(string)

		repo.EXPECT().GetByID(gomock.Any(), "ws1", "auto-1").Return(&domain.Automation{ID: "auto-1", Nodes: []*domain.AutomationNode{prev}}, nil)
		var storedCfg map[string]interface{}
		repo.EXPECT().Update(gomock.Any(), "ws1", gomock.Any()).DoAndReturn(func(_ context.Context, _ string, a *domain.Automation) error {
			storedCfg = a.Nodes[0].Config
			return nil
		})
		// Le client renvoie ce que l'API lui a donné (has_secret) + une nouvelle URL,
		// et tente d'injecter un secret_encrypted de son cru.
		a := automationWithWebhook(map[string]interface{}{"url": "https://h.example.com/nouvelle", "has_secret": true, "secret_encrypted": "deadbeef", "secret": ""})
		require.NoError(t, svc.Update(context.Background(), "ws1", a))

		assert.Equal(t, prevEnc, storedCfg["secret_encrypted"], "ancien secret conservé, le secret_encrypted du client est ignoré")
		assert.Equal(t, "https://h.example.com/nouvelle", storedCfg["url"])
		assert.NotContains(t, jsonOf(t, a), prevEnc)
		sec, err := domain.ResolveWebhookNodeSecret(storedCfg, lotZeroPassphrase)
		require.NoError(t, err)
		assert.Equal(t, clearSecret, sec)
	})

	t.Run("update avec le masque: ancien conservé; avec un nouveau: remplacé; clear_secret: supprimé", func(t *testing.T) {
		for name, tc := range map[string]struct {
			incoming map[string]interface{}
			want     string
			none     bool
		}{
			"masque":      {map[string]interface{}{"url": "https://h.example.com/x", "secret": domain.WebhookSecretMask}, clearSecret, false},
			"nouveau":     {map[string]interface{}{"url": "https://h.example.com/x", "secret": "autre-secret"}, "autre-secret", false},
			"suppression": {map[string]interface{}{"url": "https://h.example.com/x", "clear_secret": true}, "", true},
		} {
			t.Run(name, func(t *testing.T) {
				svc, repo := newSecretAutomationService(t)
				prev := &domain.AutomationNode{ID: "wh1", Type: domain.NodeTypeWebhook, Config: map[string]interface{}{"url": "https://h.example.com/x", "secret": clearSecret}}
				require.NoError(t, domain.ApplyWebhookNodeSecretsOnSave([]*domain.AutomationNode{prev}, nil, lotZeroPassphrase))
				repo.EXPECT().GetByID(gomock.Any(), "ws1", "auto-1").Return(&domain.Automation{ID: "auto-1", Nodes: []*domain.AutomationNode{prev}}, nil)
				var storedCfg map[string]interface{}
				repo.EXPECT().Update(gomock.Any(), "ws1", gomock.Any()).DoAndReturn(func(_ context.Context, _ string, a *domain.Automation) error {
					storedCfg = a.Nodes[0].Config
					return nil
				})
				require.NoError(t, svc.Update(context.Background(), "ws1", automationWithWebhook(tc.incoming)))
				assert.NotContains(t, jsonOf(t, storedCfg), `"secret":`)
				if tc.none {
					assert.NotContains(t, storedCfg, "secret_encrypted")
					return
				}
				sec, err := domain.ResolveWebhookNodeSecret(storedCfg, lotZeroPassphrase)
				require.NoError(t, err)
				assert.Equal(t, tc.want, sec)
			})
		}
	})

	t.Run("update d'un nœud hérité (secret en clair): chiffré au passage", func(t *testing.T) {
		svc, repo := newSecretAutomationService(t)
		prev := &domain.AutomationNode{ID: "wh1", Type: domain.NodeTypeWebhook, Config: map[string]interface{}{"url": "https://h.example.com/x", "secret": "legacy-clear"}}
		repo.EXPECT().GetByID(gomock.Any(), "ws1", "auto-1").Return(&domain.Automation{ID: "auto-1", Nodes: []*domain.AutomationNode{prev}}, nil)
		var stored string
		repo.EXPECT().Update(gomock.Any(), "ws1", gomock.Any()).DoAndReturn(func(_ context.Context, _ string, a *domain.Automation) error {
			stored = jsonOf(t, a.Nodes)
			return nil
		})
		require.NoError(t, svc.Update(context.Background(), "ws1", automationWithWebhook(map[string]interface{}{"url": "https://h.example.com/x", "has_secret": true})))
		assert.NotContains(t, stored, "legacy-clear")
		assert.Contains(t, stored, "secret_encrypted")
	})

	t.Run("sans passphrase serveur: refus plutôt que stockage en clair", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		defer ctrl.Finish()
		repo := mocks.NewMockAutomationRepository(ctrl)
		auth := mocks.NewMockAuthService(ctrl)
		auth.EXPECT().AuthenticateUserForWorkspace(gomock.Any(), "ws1").Return(context.Background(), &domain.User{}, &domain.UserWorkspace{WorkspaceID: "ws1", Permissions: domain.FullPermissions}, nil)
		svc := NewAutomationService(repo, auth, setupMockLoggerForNodeExecutor(ctrl))
		err := svc.Create(context.Background(), "ws1", automationWithWebhook(map[string]interface{}{"url": "https://h.example.com/x", "secret": "abc"}))
		require.Error(t, err)
		assert.Contains(t, err.Error(), "encryption key")
	})
}

func TestAutomationService_WebhookURL_RefusedAtSave(t *testing.T) {
	for _, bad := range []string{
		"http://hooks.example.com/x", "https://127.0.0.1/x", "https://169.254.169.254/x", "https://100.64.1.1/x",
		"https://[fd00::1]/x", "https://u:p@hooks.example.com/x", "ftp://hooks.example.com/x",
	} {
		t.Run(bad, func(t *testing.T) {
			svc, _ := newSecretAutomationService(t) // aucune attente sur repo.Create : un appel ferait échouer le test
			err := svc.Create(context.Background(), "ws1", automationWithWebhook(map[string]interface{}{"url": bad}))
			require.Error(t, err)
			assert.Contains(t, err.Error(), "invalid webhook node")
		})
	}
}

// ---------------------------------------------------------------------------
// Autres appels sortants pilotés par un locataire.
// ---------------------------------------------------------------------------

func TestTenantOutboundClient_ProductionWiringRefusesInternal(t *testing.T) {
	var hit int32
	internal := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { atomic.AddInt32(&hit, 1) }))
	defer internal.Close()
	client := NewTenantOutboundClient(2 * time.Second)

	for _, u := range []string{internal.URL, "https://169.254.169.254/", "https://[::1]/", "http://example.com/"} {
		_, err := client.Get(u)
		require.Error(t, err, u)
	}
	assert.EqualValues(t, 0, atomic.LoadInt32(&hit))

	// proxy d'environnement : ignoré
	t.Setenv("HTTPS_PROXY", "http://127.0.0.1:1")
	_, err := NewTenantOutboundClient(2 * time.Second).Get(internal.URL)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "refusing to connect")
}

func TestDataFeed_TenantURLGuarded(t *testing.T) {
	var hit int32
	internal := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&hit, 1)
		w.Write([]byte(`{"leak":"yes"}`))
	}))
	defer internal.Close()
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	fetcher := broadcast.NewDataFeedFetcherWithClient(setupMockLoggerForNodeExecutor(ctrl), NewTenantOutboundClient(30*time.Second))
	out, err := fetcher.FetchGlobal(context.Background(), &domain.GlobalFeedSettings{Enabled: true, URL: internal.URL}, &domain.GlobalFeedRequestPayload{})
	require.Error(t, err)
	assert.Nil(t, out)
	assert.EqualValues(t, 0, atomic.LoadInt32(&hit))
}

func TestFirecrawl_DefaultClientRefusesInternalBaseURL(t *testing.T) {
	var hit int32
	internal := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { atomic.AddInt32(&hit, 1) }))
	defer internal.Close()
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	svc := NewFirecrawlService(setupMockLoggerForNodeExecutor(ctrl))
	_, err := svc.Scrape(context.Background(), &domain.FirecrawlSettings{APIKey: "k", BaseURL: internal.URL}, "https://example.com", nil)
	require.Error(t, err)
	assert.EqualValues(t, 0, atomic.LoadInt32(&hit))
}

func TestConfirmSNSSubscription_Guarded(t *testing.T) {
	for _, bad := range []string{
		"http://sns.us-east-1.amazonaws.com/?x=1", // pas HTTPS
		"https://169.254.169.254/latest/meta-data/",
		"https://127.0.0.1/",
		"https://evil.example.com/sns.amazonaws.com",
		"https://sns.us-east-1.amazonaws.com.evil.example.com/",
	} {
		resp, err := confirmSNSSubscription(bad)
		require.Error(t, err, bad)
		assert.Nil(t, resp)
	}
}

// roundTripperFunc permet à un test de remplacer le transport d'un client.
type roundTripperFunc func(*http.Request) (*http.Response, error)

func (f roundTripperFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
