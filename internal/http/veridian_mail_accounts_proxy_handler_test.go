package http

// Tests unitaires VeridianMailAccountsProxyHandler — endpoints
// GET /api/veridian/mail-accounts/me et POST .../{accountId}/default.
//
// On NE teste pas le middleware RequireAuth ici (couvert par auth_test.go).
// On injecte directement user_id dans le context, comme post-JWT-decode.

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Notifuse/notifuse/internal/domain"
	"github.com/Notifuse/notifuse/pkg/hub_mail_accounts"
	"github.com/Notifuse/notifuse/pkg/logger"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ─── Fakes ──────────────────────────────────────────────────────────────────

type fakeMailAccountsClient struct {
	listResp    *hub_mail_accounts.ListAccountsResult
	listErr     error
	listGotUID  string
	setResp     *hub_mail_accounts.SetDefaultResult
	setErr      error
	setGotUID   string
	setGotAccID string
}

func (f *fakeMailAccountsClient) ListMailAccounts(_ context.Context, userID string) (*hub_mail_accounts.ListAccountsResult, error) {
	f.listGotUID = userID
	return f.listResp, f.listErr
}

func (f *fakeMailAccountsClient) SetDefaultAccount(_ context.Context, userID, accountID string) (*hub_mail_accounts.SetDefaultResult, error) {
	f.setGotUID = userID
	f.setGotAccID = accountID
	return f.setResp, f.setErr
}

// Reutilise fakeUserLookup defini dans veridian_hub_discovery_handler_test.go
// (meme package, evite la duplication).

func newProxyHandler(client MailAccountsClient, user *domain.User, userErr error) *VeridianMailAccountsProxyHandler {
	return &VeridianMailAccountsProxyHandler{
		client:       client,
		userService:  &fakeUserLookup{user: user, err: userErr},
		getJWTSecret: func() ([]byte, error) { return []byte("test"), nil },
		logger:       logger.NewLogger(),
	}
}

func userWithHubID(id string) *domain.User {
	hubID := id + "-hub"
	return &domain.User{ID: id, Email: id + "@example.com", HubUserID: &hubID}
}

func reqGetWithUserCtx(userID string) *http.Request {
	r := httptest.NewRequest(http.MethodGet, "/api/veridian/mail-accounts/me", nil)
	ctx := context.WithValue(r.Context(), domain.UserIDKey, userID)
	return r.WithContext(ctx)
}

func reqPostSetDefault(userID, accountID string) *http.Request {
	r := httptest.NewRequest(http.MethodPost, "/api/veridian/mail-accounts/me/"+accountID+"/default", nil)
	r.SetPathValue("accountId", accountID)
	ctx := context.WithValue(r.Context(), domain.UserIDKey, userID)
	return r.WithContext(ctx)
}

// ─── handleList ─────────────────────────────────────────────────────────────

func TestProxyList_OK_HubAvailable_Accounts(t *testing.T) {
	client := &fakeMailAccountsClient{
		listResp: &hub_mail_accounts.ListAccountsResult{
			HubAvailable: true,
			Accounts: []hub_mail_accounts.MailAccount{
				{ID: "acc1", Provider: "google", Email: "a@b.com", IsDefault: true},
				{ID: "acc2", Provider: "microsoft", Email: "x@y.com", NeedsReauth: true},
			},
		},
	}
	h := newProxyHandler(client, userWithHubID("u1"), nil)

	rec := httptest.NewRecorder()
	h.handleList(rec, reqGetWithUserCtx("u1"))

	require.Equal(t, http.StatusOK, rec.Code)
	var resp MailAccountsListResponse
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp))
	assert.True(t, resp.HubAvailable)
	require.Len(t, resp.Accounts, 2)
	assert.Equal(t, "acc1", resp.Accounts[0].ID)
	assert.Equal(t, "u1-hub", client.listGotUID, "hub_user_id passe au client, pas le user id local")
}

func TestProxyList_OK_HubAvailable_EmptyAccounts(t *testing.T) {
	client := &fakeMailAccountsClient{
		listResp: &hub_mail_accounts.ListAccountsResult{
			HubAvailable: true,
			Accounts:     []hub_mail_accounts.MailAccount{},
		},
	}
	h := newProxyHandler(client, userWithHubID("u1"), nil)

	rec := httptest.NewRecorder()
	h.handleList(rec, reqGetWithUserCtx("u1"))

	require.Equal(t, http.StatusOK, rec.Code)
	var resp MailAccountsListResponse
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp))
	assert.True(t, resp.HubAvailable)
	assert.NotNil(t, resp.Accounts, "doit etre [] et non null en JSON")
	assert.Len(t, resp.Accounts, 0)
}

func TestProxyList_HubUnavailable_Fallback(t *testing.T) {
	// Cas typique : endpoint Hub 404 catchall (pas encore livre).
	client := &fakeMailAccountsClient{
		listResp: &hub_mail_accounts.ListAccountsResult{
			HubAvailable: false,
			Accounts:     []hub_mail_accounts.MailAccount{},
		},
	}
	h := newProxyHandler(client, userWithHubID("u1"), nil)

	rec := httptest.NewRecorder()
	h.handleList(rec, reqGetWithUserCtx("u1"))

	require.Equal(t, http.StatusOK, rec.Code, "fallback gracieux meme si Hub down")
	var resp MailAccountsListResponse
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp))
	assert.False(t, resp.HubAvailable)
	assert.Empty(t, resp.Accounts)
}

func TestProxyList_NoJWT_401(t *testing.T) {
	h := newProxyHandler(&fakeMailAccountsClient{}, nil, nil)
	r := httptest.NewRequest(http.MethodGet, "/api/veridian/mail-accounts/me", nil)
	// pas de user_id dans le ctx
	rec := httptest.NewRecorder()
	h.handleList(rec, r)
	assert.Equal(t, http.StatusUnauthorized, rec.Code)
}

func TestProxyList_UserPreV46_NoHubUserID_Fallback(t *testing.T) {
	// User existe mais hub_user_id nil (cas root, internal, ou pre-V46)
	user := &domain.User{ID: "u1", Email: "root@local", HubUserID: nil}
	client := &fakeMailAccountsClient{}
	h := newProxyHandler(client, user, nil)

	rec := httptest.NewRecorder()
	h.handleList(rec, reqGetWithUserCtx("u1"))

	require.Equal(t, http.StatusOK, rec.Code)
	var resp MailAccountsListResponse
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp))
	assert.False(t, resp.HubAvailable, "user sans hub_user_id => hub_available=false")
	assert.Empty(t, resp.Accounts)
	assert.Empty(t, client.listGotUID, "le client ne doit JAMAIS etre appele si pas de hub_user_id")
}

func TestProxyList_UserNotFound_Fallback(t *testing.T) {
	// DB instable : JWT valide mais GetUserByID retourne erreur
	client := &fakeMailAccountsClient{}
	h := newProxyHandler(client, nil, errors.New("db down"))

	rec := httptest.NewRecorder()
	h.handleList(rec, reqGetWithUserCtx("u-zombie"))

	require.Equal(t, http.StatusOK, rec.Code, "best-effort fallback meme si DB down")
	var resp MailAccountsListResponse
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp))
	assert.False(t, resp.HubAvailable)
}

// ─── handleSetDefault ───────────────────────────────────────────────────────

func TestProxySetDefault_OK(t *testing.T) {
	client := &fakeMailAccountsClient{
		setResp: &hub_mail_accounts.SetDefaultResult{
			HubAvailable: true,
			UserID:       "u1-hub",
			AccountID:    "acc1",
			IsDefault:    true,
			HTTPStatus:   200,
		},
	}
	h := newProxyHandler(client, userWithHubID("u1"), nil)

	rec := httptest.NewRecorder()
	h.handleSetDefault(rec, reqPostSetDefault("u1", "acc1"))

	require.Equal(t, http.StatusOK, rec.Code)
	var resp MailAccountSetDefaultResponse
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp))
	assert.True(t, resp.HubAvailable)
	assert.True(t, resp.IsDefault)
	assert.Equal(t, "acc1", resp.AccountID)
	assert.Equal(t, "u1-hub", client.setGotUID)
	assert.Equal(t, "acc1", client.setGotAccID)
}

func TestProxySetDefault_MissingAccountID_400(t *testing.T) {
	h := newProxyHandler(&fakeMailAccountsClient{}, userWithHubID("u1"), nil)

	// On simule le mux qui n'a pas resolved {accountId} - on cree une req sans pathvalue
	r := httptest.NewRequest(http.MethodPost, "/api/veridian/mail-accounts/me//default", nil)
	ctx := context.WithValue(r.Context(), domain.UserIDKey, "u1")
	r = r.WithContext(ctx)
	rec := httptest.NewRecorder()
	h.handleSetDefault(rec, r)
	assert.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestProxySetDefault_NoHubUserID_Fallback(t *testing.T) {
	user := &domain.User{ID: "u1", Email: "root@local", HubUserID: nil}
	client := &fakeMailAccountsClient{}
	h := newProxyHandler(client, user, nil)

	rec := httptest.NewRecorder()
	h.handleSetDefault(rec, reqPostSetDefault("u1", "acc1"))

	require.Equal(t, http.StatusOK, rec.Code)
	var resp MailAccountSetDefaultResponse
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp))
	assert.False(t, resp.HubAvailable)
	assert.Equal(t, "no_hub_user_id", resp.Reason)
	assert.Empty(t, client.setGotUID)
}

func TestProxySetDefault_HubUnavailable_Fallback(t *testing.T) {
	client := &fakeMailAccountsClient{
		setResp: &hub_mail_accounts.SetDefaultResult{
			HubAvailable: false,
			Reason:       "unreachable",
			HTTPStatus:   0,
		},
	}
	h := newProxyHandler(client, userWithHubID("u1"), nil)

	rec := httptest.NewRecorder()
	h.handleSetDefault(rec, reqPostSetDefault("u1", "acc1"))

	require.Equal(t, http.StatusOK, rec.Code)
	var resp MailAccountSetDefaultResponse
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp))
	assert.False(t, resp.HubAvailable)
	assert.Equal(t, "unreachable", resp.Reason)
}

// ─── Compile-time interface ─────────────────────────────────────────────────

var _ MailAccountsClient = (*fakeMailAccountsClient)(nil)
