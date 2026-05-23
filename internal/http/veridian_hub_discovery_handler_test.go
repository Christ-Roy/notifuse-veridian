package http

// === Veridian patch ===
// Tests unitaires VeridianHubDiscoveryHandler — endpoint
// GET /api/veridian/hub-discovery/me.
//
// On NE teste pas le middleware RequireAuth ici (couvert par auth_test.go).
// On injecte directement le user_id dans le context, comme si JWT-decoded.

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Notifuse/notifuse/internal/domain"
	"github.com/Notifuse/notifuse/pkg/hub_discovery"
	"github.com/Notifuse/notifuse/pkg/logger"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ─── Fakes ──────────────────────────────────────────────────────────────────

// fakeHubDiscoveryClient implemente HubDiscoveryClient pour tests.
type fakeHubDiscoveryClient struct {
	gotEmail string
	exists   bool
	tenants  []hub_discovery.DiscoveryTenant
	err      error
}

func (f *fakeHubDiscoveryClient) LookupByEmail(_ context.Context, email string) (bool, []hub_discovery.DiscoveryTenant, error) {
	f.gotEmail = email
	return f.exists, f.tenants, f.err
}

// fakeUserLookup implemente UserLookupService.
type fakeUserLookup struct {
	user *domain.User
	err  error
}

func (f *fakeUserLookup) GetUserByID(_ context.Context, _ string) (*domain.User, error) {
	return f.user, f.err
}

// ─── Helpers ────────────────────────────────────────────────────────────────

func newDiscoveryHandler(client HubDiscoveryClient, user *domain.User, userErr error) *VeridianHubDiscoveryHandler {
	return &VeridianHubDiscoveryHandler{
		client:       client,
		userService:  &fakeUserLookup{user: user, err: userErr},
		getJWTSecret: func() ([]byte, error) { return []byte("test"), nil },
		logger:       logger.NewLogger(),
	}
}

// reqWithUserCtx fabrique une GET /api/veridian/hub-discovery/me avec un
// user_id pre-injecte dans le context (comme le ferait RequireAuth).
func reqWithUserCtx(userID string) *http.Request {
	r := httptest.NewRequest(http.MethodGet, "/api/veridian/hub-discovery/me", nil)
	ctx := context.WithValue(r.Context(), domain.UserIDKey, userID)
	return r.WithContext(ctx)
}

// ─── Tests ──────────────────────────────────────────────────────────────────

func TestVeridianHubDiscovery_OK_HubAvailable_ExistsTrue(t *testing.T) {
	client := &fakeHubDiscoveryClient{
		exists: true,
		tenants: []hub_discovery.DiscoveryTenant{
			{App: "notifuse", Role: "owner"},
			{App: "prospection", Role: "member"},
		},
	}
	user := &domain.User{ID: "u-1", Email: "alice@example.com"}
	h := newDiscoveryHandler(client, user, nil)

	rec := httptest.NewRecorder()
	h.handleDiscoverMe(rec, reqWithUserCtx("u-1"))

	require.Equal(t, http.StatusOK, rec.Code)
	var resp HubDiscoveryResponse
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp))
	assert.True(t, resp.HubAvailable)
	assert.True(t, resp.Exists)
	require.Len(t, resp.Tenants, 2)
	assert.Equal(t, "notifuse", resp.Tenants[0].App)
	assert.Equal(t, "prospection", resp.Tenants[1].App)
	// L'email du JWT user est bien passe au client (pas un email query forge)
	assert.Equal(t, "alice@example.com", client.gotEmail)
}

func TestVeridianHubDiscovery_OK_HubAvailable_ExistsFalse(t *testing.T) {
	client := &fakeHubDiscoveryClient{
		exists:  false,
		tenants: []hub_discovery.DiscoveryTenant{},
	}
	user := &domain.User{ID: "u-1", Email: "ghost@example.com"}
	h := newDiscoveryHandler(client, user, nil)

	rec := httptest.NewRecorder()
	h.handleDiscoverMe(rec, reqWithUserCtx("u-1"))

	require.Equal(t, http.StatusOK, rec.Code)
	var resp HubDiscoveryResponse
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp))
	assert.True(t, resp.HubAvailable)
	assert.False(t, resp.Exists)
	assert.Empty(t, resp.Tenants)
}

func TestVeridianHubDiscovery_HubError_FailsSafe_NotBlocking(t *testing.T) {
	// Hub call erreur (5xx) -> response 200, hub_available=false, exists=false
	client := &fakeHubDiscoveryClient{
		tenants: nil,
		err:     errors.New("hub_discovery: status 503"),
	}
	user := &domain.User{ID: "u-1", Email: "alice@example.com"}
	h := newDiscoveryHandler(client, user, nil)

	rec := httptest.NewRecorder()
	h.handleDiscoverMe(rec, reqWithUserCtx("u-1"))

	// Best-effort : repond 200 meme en cas d'erreur Hub
	require.Equal(t, http.StatusOK, rec.Code)
	var resp HubDiscoveryResponse
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp))
	assert.False(t, resp.HubAvailable)
	assert.False(t, resp.Exists)
	// tenants normalise en [] et pas null (front s'attend a un array JSON)
	assert.NotNil(t, resp.Tenants)
	assert.Empty(t, resp.Tenants)
}

func TestVeridianHubDiscovery_DisabledClient_HubUnavailable(t *testing.T) {
	// Cas "HUB_API_SECRET vide" -> disabled client retourne (false, nil, nil)
	client := hub_discovery.NewClient(hub_discovery.Config{Secret: ""})
	user := &domain.User{ID: "u-1", Email: "alice@example.com"}
	h := newDiscoveryHandler(client, user, nil)

	rec := httptest.NewRecorder()
	h.handleDiscoverMe(rec, reqWithUserCtx("u-1"))

	require.Equal(t, http.StatusOK, rec.Code)
	var resp HubDiscoveryResponse
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp))
	// tenants nil (mode disabled) -> hub_available=false
	assert.False(t, resp.HubAvailable)
	assert.False(t, resp.Exists)
	// Mais on serialise toujours en [] pour le front
	assert.NotNil(t, resp.Tenants)
	assert.Empty(t, resp.Tenants)
}

func TestVeridianHubDiscovery_MissingUserID_401(t *testing.T) {
	client := &fakeHubDiscoveryClient{}
	h := newDiscoveryHandler(client, &domain.User{}, nil)

	// Pas de user_id dans le context
	r := httptest.NewRequest(http.MethodGet, "/api/veridian/hub-discovery/me", nil)
	rec := httptest.NewRecorder()
	h.handleDiscoverMe(rec, r)

	assert.Equal(t, http.StatusUnauthorized, rec.Code)
	assert.Empty(t, client.gotEmail, "client must NOT be called without user_id")
}

func TestVeridianHubDiscovery_UserLookupFails_FailsSafe(t *testing.T) {
	// DB instable : GetUserByID retourne erreur. On veut 200 best-effort
	// (hub_available=false) plutot que de bloquer le login.
	client := &fakeHubDiscoveryClient{}
	h := newDiscoveryHandler(client, nil, errors.New("db connection refused"))

	rec := httptest.NewRecorder()
	h.handleDiscoverMe(rec, reqWithUserCtx("u-1"))

	require.Equal(t, http.StatusOK, rec.Code)
	var resp HubDiscoveryResponse
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp))
	assert.False(t, resp.HubAvailable)
	assert.False(t, resp.Exists)
	// Le client Hub n'a pas du tout ete appele (pas d'email a lookup)
	assert.Empty(t, client.gotEmail)
}

func TestVeridianHubDiscovery_MethodPOST_405(t *testing.T) {
	client := &fakeHubDiscoveryClient{}
	h := newDiscoveryHandler(client, &domain.User{ID: "u-1", Email: "a@b"}, nil)

	r := httptest.NewRequest(http.MethodPost, "/api/veridian/hub-discovery/me", nil)
	ctx := context.WithValue(r.Context(), domain.UserIDKey, "u-1")
	r = r.WithContext(ctx)

	rec := httptest.NewRecorder()
	h.handleDiscoverMe(rec, r)

	assert.Equal(t, http.StatusMethodNotAllowed, rec.Code)
}

func TestVeridianHubDiscovery_TenantsArray_AlwaysSerializedAsJSONArray(t *testing.T) {
	// Le front s'attend a un array, jamais null. Verifie que meme avec
	// tenants=nil cote client, la reponse JSON contient "tenants":[].
	client := &fakeHubDiscoveryClient{exists: false, tenants: nil, err: nil}
	user := &domain.User{ID: "u-1", Email: "alice@example.com"}
	h := newDiscoveryHandler(client, user, nil)

	rec := httptest.NewRecorder()
	h.handleDiscoverMe(rec, reqWithUserCtx("u-1"))

	require.Equal(t, http.StatusOK, rec.Code)
	// Verifie directement le payload JSON brut, pas via decode
	body := rec.Body.String()
	assert.Contains(t, body, `"tenants":[]`)
	assert.NotContains(t, body, `"tenants":null`)
}

func TestNewVeridianHubDiscoveryHandler_Constructor(t *testing.T) {
	client := &fakeHubDiscoveryClient{}
	user := &fakeUserLookup{}
	getSecret := func() ([]byte, error) { return []byte("x"), nil }
	log := logger.NewLogger()

	h := NewVeridianHubDiscoveryHandler(client, user, getSecret, log)
	require.NotNil(t, h)
	assert.NotNil(t, h.client)
	assert.NotNil(t, h.userService)
	assert.NotNil(t, h.getJWTSecret)
	assert.NotNil(t, h.logger)
}

func TestVeridianHubDiscoveryHandler_RegisterRoutes(t *testing.T) {
	// Sanity check : RegisterRoutes attache bien /api/veridian/hub-discovery/me
	// sous le middleware RequireAuth. On verifie qu'une req sans
	// Authorization renvoie 401.
	mux := http.NewServeMux()
	client := &fakeHubDiscoveryClient{}
	user := &domain.User{ID: "u-1", Email: "a@b"}
	h := newDiscoveryHandler(client, user, nil)
	h.RegisterRoutes(mux)

	r := httptest.NewRequest(http.MethodGet, "/api/veridian/hub-discovery/me", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, r)

	// Pas de header Authorization -> 401 (middleware RequireAuth)
	assert.Equal(t, http.StatusUnauthorized, rec.Code)
}
