package http

// === Veridian patch — Hub invitation flow (2026-05-23) ===
// Tests unitaires VeridianInviteMemberHandler — endpoint
// POST /api/veridian/workspaces.inviteMember (auth JWT user-type).
//
// Le handler attend dans le ctx (apres requireAuth middleware) :
//   - domain.UserIDKey (string) : id du caller (owner Notifuse)
// Il appelle ensuite :
//   - authService.AuthenticateUserForWorkspace -> verifie membership
//   - hubUserResolver.GetHubUserID -> SELECT users.hub_user_id (V46)
//   - hubClient.Create -> POST app.veridian.site/api/invitations/create
//
// Tests = bypass middleware en appelant handleInvite directement.

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/Notifuse/notifuse/internal/domain"
	"github.com/Notifuse/notifuse/internal/domain/mocks"
	"github.com/Notifuse/notifuse/internal/service"
	"github.com/Notifuse/notifuse/pkg/logger"

	"github.com/golang/mock/gomock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// stubHubClient implemente service.HubInvitationClient pour les tests.
type stubHubClient struct {
	called    int
	lastInput service.HubInvitationInput
	result    *service.HubInvitationResult
	err       error
}

func (s *stubHubClient) Create(_ context.Context, input service.HubInvitationInput) (*service.HubInvitationResult, error) {
	s.called++
	s.lastInput = input
	if s.err != nil {
		return nil, s.err
	}
	return s.result, nil
}

// stubHubUserIDResolver implemente HubUserIDResolver pour les tests.
type stubHubUserIDResolver struct {
	hubUserID string
	err       error
}

func (s *stubHubUserIDResolver) GetHubUserID(_ context.Context, _ string) (string, error) {
	return s.hubUserID, s.err
}

func newInviteHandler(
	t *testing.T,
	hubClient service.HubInvitationClient,
	authSvc domain.AuthService,
	resolver HubUserIDResolver,
	managed bool,
) *VeridianInviteMemberHandler {
	t.Helper()
	return NewVeridianInviteMemberHandler(
		hubClient,
		authSvc,
		nil, // userService unused in direct handler test path
		resolver,
		func() ([]byte, error) { return []byte("test-jwt-secret"), nil },
		logger.NewLogger(),
		managed,
	)
}

func buildInviteReq(t *testing.T, body string) *http.Request {
	t.Helper()
	return httptest.NewRequest(http.MethodPost, "/api/veridian/workspaces.inviteMember", bytes.NewReader([]byte(body)))
}

// ─── Tests principaux ────────────────────────────────────────────────────────

func TestVeridianInviteMember_OK_201Reused(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	authSvc := mocks.NewMockAuthService(ctrl)
	owner := &domain.User{ID: "user-local-1", Type: domain.UserTypeUser, Email: "owner@example.com"}
	authSvc.EXPECT().
		AuthenticateUserForWorkspace(gomock.Any(), "ws-42").
		Return(context.Background(), owner, &domain.UserWorkspace{WorkspaceID: "ws-42"}, nil)

	hubStub := &stubHubClient{
		result: &service.HubInvitationResult{
			InvitationID: "inv-abc",
			Token:        "tok-xyz",
			MagicLinkURL: "https://hub.staging.veridian.site/invite/tok-xyz",
			ExpiresAt:    time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC),
			TargetRole:   "member",
			Reused:       false,
		},
	}
	resolver := &stubHubUserIDResolver{hubUserID: "hub-uuid-owner"}

	h := newInviteHandler(t, hubStub, authSvc, resolver, true)
	req := buildInviteReq(t, `{"workspace_id":"ws-42","email":"guest@example.com","role":"member"}`)
	rec := httptest.NewRecorder()
	h.handleInvite(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)
	var resp VeridianInviteMemberResponse
	require.NoError(t, json.NewDecoder(rec.Body).Decode(&resp))
	assert.Equal(t, "success", resp.Status)
	assert.Equal(t, "inv-abc", resp.HubInvitationID)
	assert.Equal(t, "https://hub.staging.veridian.site/invite/tok-xyz", resp.MagicLinkURL)
	assert.False(t, resp.Reused)

	// Verifie que le client Hub a bien recu hub_user_id resolu, pas le local user_id.
	assert.Equal(t, 1, hubStub.called)
	assert.Equal(t, "hub-uuid-owner", hubStub.lastInput.InviterUserID)
	assert.Equal(t, "owner@example.com", hubStub.lastInput.InviterEmail)
	assert.Equal(t, "guest@example.com", hubStub.lastInput.InviteeEmail)
	assert.Equal(t, "ws-42", hubStub.lastInput.TargetWorkspaceID)
	assert.Equal(t, "notifuse", hubStub.lastInput.TargetApp)
	assert.Equal(t, "member", hubStub.lastInput.TargetRole)
}

func TestVeridianInviteMember_PrefersUserStructHubUserID(t *testing.T) {
	// Quand user.HubUserID est non-nil (V46 backfille charge par
	// user_postgres), on l'utilise PRIORITAIREMENT (pas de lookup SQL en plus).
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	authSvc := mocks.NewMockAuthService(ctrl)
	hubID := "hub-uuid-from-struct"
	owner := &domain.User{
		ID:        "user-local-7",
		Type:      domain.UserTypeUser,
		Email:     "o@x.com",
		HubUserID: &hubID,
	}
	authSvc.EXPECT().
		AuthenticateUserForWorkspace(gomock.Any(), "ws-7").
		Return(context.Background(), owner, &domain.UserWorkspace{}, nil)

	hubStub := &stubHubClient{
		result: &service.HubInvitationResult{InvitationID: "i", ExpiresAt: time.Now()},
	}
	// Resolver retourne autre chose : on doit l'IGNORER car user.HubUserID
	// fait foi.
	resolver := &stubHubUserIDResolver{hubUserID: "RESOLVER-SHOULD-NOT-BE-USED"}

	h := newInviteHandler(t, hubStub, authSvc, resolver, true)
	req := buildInviteReq(t, `{"workspace_id":"ws-7","email":"g@g.com"}`)
	rec := httptest.NewRecorder()
	h.handleInvite(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, "hub-uuid-from-struct", hubStub.lastInput.InviterUserID)
}

func TestVeridianInviteMember_FallbackEmptyHubUserID_UsesLocalID(t *testing.T) {
	// Si hub_user_id NULL (V46 row pas backfillee) → on passe quand meme avec
	// le user_id local, le Hub fallback sur email. On verifie via log + appel.
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	authSvc := mocks.NewMockAuthService(ctrl)
	owner := &domain.User{ID: "user-local-99", Type: domain.UserTypeUser, Email: "legacy@x.com"}
	authSvc.EXPECT().
		AuthenticateUserForWorkspace(gomock.Any(), "ws-1").
		Return(context.Background(), owner, &domain.UserWorkspace{}, nil)

	hubStub := &stubHubClient{
		result: &service.HubInvitationResult{
			InvitationID: "inv-x",
			Token:        "t",
			MagicLinkURL: "u",
			ExpiresAt:    time.Now().Add(time.Hour),
			TargetRole:   "member",
		},
	}
	resolver := &stubHubUserIDResolver{hubUserID: ""} // NULL en DB

	h := newInviteHandler(t, hubStub, authSvc, resolver, true)
	req := buildInviteReq(t, `{"workspace_id":"ws-1","email":"g@g.com"}`)
	rec := httptest.NewRecorder()
	h.handleInvite(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
	// Fallback : on a envoye user.ID au Hub.
	assert.Equal(t, "user-local-99", hubStub.lastInput.InviterUserID)
}

func TestVeridianInviteMember_DefaultRoleMember(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	authSvc := mocks.NewMockAuthService(ctrl)
	authSvc.EXPECT().
		AuthenticateUserForWorkspace(gomock.Any(), "ws-1").
		Return(context.Background(), &domain.User{ID: "u", Type: domain.UserTypeUser, Email: "o@o.com"}, &domain.UserWorkspace{}, nil)

	hubStub := &stubHubClient{result: &service.HubInvitationResult{InvitationID: "i", ExpiresAt: time.Now()}}
	h := newInviteHandler(t, hubStub, authSvc, &stubHubUserIDResolver{hubUserID: "hub-uid"}, true)

	// Body sans champ "role".
	req := buildInviteReq(t, `{"workspace_id":"ws-1","email":"g@g.com"}`)
	rec := httptest.NewRecorder()
	h.handleInvite(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, "member", hubStub.lastInput.TargetRole)
}

// ─── Erreurs validation ──────────────────────────────────────────────────────

func TestVeridianInviteMember_MethodNotAllowed_405(t *testing.T) {
	h := newInviteHandler(t, &stubHubClient{}, nil, &stubHubUserIDResolver{}, true)
	req := httptest.NewRequest(http.MethodGet, "/api/veridian/workspaces.inviteMember", nil)
	rec := httptest.NewRecorder()
	h.handleInvite(rec, req)
	assert.Equal(t, http.StatusMethodNotAllowed, rec.Code)
}

func TestVeridianInviteMember_SelfHostedMode_503(t *testing.T) {
	h := newInviteHandler(t, &stubHubClient{}, nil, &stubHubUserIDResolver{}, false)
	req := buildInviteReq(t, `{"workspace_id":"w","email":"e@e.com"}`)
	rec := httptest.NewRecorder()
	h.handleInvite(rec, req)
	assert.Equal(t, http.StatusServiceUnavailable, rec.Code)
	assert.Contains(t, rec.Body.String(), "self-hosted")
}

func TestVeridianInviteMember_InvalidJSON_400(t *testing.T) {
	h := newInviteHandler(t, &stubHubClient{}, nil, &stubHubUserIDResolver{}, true)
	req := buildInviteReq(t, `{garbage`)
	rec := httptest.NewRecorder()
	h.handleInvite(rec, req)
	assert.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestVeridianInviteMember_MissingFields_400(t *testing.T) {
	h := newInviteHandler(t, &stubHubClient{}, nil, &stubHubUserIDResolver{}, true)
	cases := []string{
		`{}`,
		`{"workspace_id":"w"}`,
		`{"email":"e@e.com"}`,
	}
	for _, body := range cases {
		req := buildInviteReq(t, body)
		rec := httptest.NewRecorder()
		h.handleInvite(rec, req)
		assert.Equal(t, http.StatusBadRequest, rec.Code, "body=%s", body)
	}
}

// ─── Auth / membership ──────────────────────────────────────────────────────

func TestVeridianInviteMember_AuthFails_403(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	authSvc := mocks.NewMockAuthService(ctrl)
	authSvc.EXPECT().
		AuthenticateUserForWorkspace(gomock.Any(), "ws-not-mine").
		Return(context.Background(), nil, nil, errors.New("not a member"))

	h := newInviteHandler(t, &stubHubClient{}, authSvc, &stubHubUserIDResolver{}, true)
	req := buildInviteReq(t, `{"workspace_id":"ws-not-mine","email":"e@e.com"}`)
	rec := httptest.NewRecorder()
	h.handleInvite(rec, req)
	assert.Equal(t, http.StatusForbidden, rec.Code)
}

func TestVeridianInviteMember_APIKeyCannotInvite_403(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	authSvc := mocks.NewMockAuthService(ctrl)
	authSvc.EXPECT().
		AuthenticateUserForWorkspace(gomock.Any(), "ws-1").
		Return(context.Background(), &domain.User{ID: "k", Type: domain.UserTypeAPIKey, Email: "api@x.com"}, &domain.UserWorkspace{}, nil)

	h := newInviteHandler(t, &stubHubClient{}, authSvc, &stubHubUserIDResolver{}, true)
	req := buildInviteReq(t, `{"workspace_id":"ws-1","email":"e@e.com"}`)
	rec := httptest.NewRecorder()
	h.handleInvite(rec, req)
	assert.Equal(t, http.StatusForbidden, rec.Code)
	assert.Contains(t, rec.Body.String(), "API key")
}

func TestVeridianInviteMember_SelfInvite_400(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	authSvc := mocks.NewMockAuthService(ctrl)
	authSvc.EXPECT().
		AuthenticateUserForWorkspace(gomock.Any(), "ws-1").
		Return(context.Background(), &domain.User{ID: "u", Type: domain.UserTypeUser, Email: "me@x.com"}, &domain.UserWorkspace{}, nil)

	h := newInviteHandler(t, &stubHubClient{}, authSvc, &stubHubUserIDResolver{}, true)
	req := buildInviteReq(t, `{"workspace_id":"ws-1","email":"me@x.com"}`)
	rec := httptest.NewRecorder()
	h.handleInvite(rec, req)
	assert.Equal(t, http.StatusBadRequest, rec.Code)
	assert.Contains(t, rec.Body.String(), "yourself")
}

// ─── Mapping erreurs Hub ─────────────────────────────────────────────────────

func TestVeridianInviteMember_HubUnreachable_502(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	authSvc := mocks.NewMockAuthService(ctrl)
	authSvc.EXPECT().
		AuthenticateUserForWorkspace(gomock.Any(), "ws-1").
		Return(context.Background(), &domain.User{ID: "u", Type: domain.UserTypeUser, Email: "o@x.com"}, &domain.UserWorkspace{}, nil)

	hubStub := &stubHubClient{
		err: &service.HubInvitationError{HubStatus: 0, Code: "hub_unreachable", Message: "dial tcp: timeout"},
	}
	h := newInviteHandler(t, hubStub, authSvc, &stubHubUserIDResolver{hubUserID: "hub-x"}, true)
	req := buildInviteReq(t, `{"workspace_id":"ws-1","email":"e@e.com"}`)
	rec := httptest.NewRecorder()
	h.handleInvite(rec, req)
	assert.Equal(t, http.StatusBadGateway, rec.Code)
	assert.Contains(t, rec.Body.String(), "hub unreachable")
}

func TestVeridianInviteMember_HubInviterNotFound_422(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	authSvc := mocks.NewMockAuthService(ctrl)
	authSvc.EXPECT().
		AuthenticateUserForWorkspace(gomock.Any(), "ws-1").
		Return(context.Background(), &domain.User{ID: "u", Type: domain.UserTypeUser, Email: "o@x.com"}, &domain.UserWorkspace{}, nil)

	hubStub := &stubHubClient{
		err: &service.HubInvitationError{HubStatus: 404, Code: "inviter_not_found", Message: "unknown user"},
	}
	h := newInviteHandler(t, hubStub, authSvc, &stubHubUserIDResolver{hubUserID: "hub-x"}, true)
	req := buildInviteReq(t, `{"workspace_id":"ws-1","email":"e@e.com"}`)
	rec := httptest.NewRecorder()
	h.handleInvite(rec, req)
	assert.Equal(t, http.StatusUnprocessableEntity, rec.Code)
	assert.Contains(t, rec.Body.String(), "Veridian Hub")
}

func TestVeridianInviteMember_HubUnauthorizedHMAC_502(t *testing.T) {
	// HMAC invalide cote Hub = pb config app, on renvoie 502 explicite.
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	authSvc := mocks.NewMockAuthService(ctrl)
	authSvc.EXPECT().
		AuthenticateUserForWorkspace(gomock.Any(), "ws-1").
		Return(context.Background(), &domain.User{ID: "u", Type: domain.UserTypeUser, Email: "o@x.com"}, &domain.UserWorkspace{}, nil)

	hubStub := &stubHubClient{
		err: &service.HubInvitationError{HubStatus: 401, Code: "unauthorized", Message: "invalid signature"},
	}
	h := newInviteHandler(t, hubStub, authSvc, &stubHubUserIDResolver{hubUserID: "hub-x"}, true)
	req := buildInviteReq(t, `{"workspace_id":"ws-1","email":"e@e.com"}`)
	rec := httptest.NewRecorder()
	h.handleInvite(rec, req)
	assert.Equal(t, http.StatusBadGateway, rec.Code)
}

func TestVeridianInviteMember_HubInvalidPayload_400(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	authSvc := mocks.NewMockAuthService(ctrl)
	authSvc.EXPECT().
		AuthenticateUserForWorkspace(gomock.Any(), "ws-1").
		Return(context.Background(), &domain.User{ID: "u", Type: domain.UserTypeUser, Email: "o@x.com"}, &domain.UserWorkspace{}, nil)

	hubStub := &stubHubClient{
		err: &service.HubInvitationError{HubStatus: 400, Code: "invalid_payload", Message: "role enum violated"},
	}
	h := newInviteHandler(t, hubStub, authSvc, &stubHubUserIDResolver{hubUserID: "hub-x"}, true)
	req := buildInviteReq(t, `{"workspace_id":"ws-1","email":"e@e.com"}`)
	rec := httptest.NewRecorder()
	h.handleInvite(rec, req)
	assert.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestVeridianInviteMember_HubRateLimited_429(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	authSvc := mocks.NewMockAuthService(ctrl)
	authSvc.EXPECT().
		AuthenticateUserForWorkspace(gomock.Any(), "ws-1").
		Return(context.Background(), &domain.User{ID: "u", Type: domain.UserTypeUser, Email: "o@x.com"}, &domain.UserWorkspace{}, nil)

	hubStub := &stubHubClient{
		err: &service.HubInvitationError{HubStatus: 429, Code: "rate_limited", Message: "60req/min"},
	}
	h := newInviteHandler(t, hubStub, authSvc, &stubHubUserIDResolver{hubUserID: "hub-x"}, true)
	req := buildInviteReq(t, `{"workspace_id":"ws-1","email":"e@e.com"}`)
	rec := httptest.NewRecorder()
	h.handleInvite(rec, req)
	assert.Equal(t, http.StatusTooManyRequests, rec.Code)
}

func TestVeridianInviteMember_HubSelfInvitation_400(t *testing.T) {
	// Defense in depth : si la verif locale n'attrape pas (ex: alias email),
	// le Hub repond self_invitation et on remappe 400.
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	authSvc := mocks.NewMockAuthService(ctrl)
	authSvc.EXPECT().
		AuthenticateUserForWorkspace(gomock.Any(), "ws-1").
		Return(context.Background(), &domain.User{ID: "u", Type: domain.UserTypeUser, Email: "owner@x.com"}, &domain.UserWorkspace{}, nil)

	hubStub := &stubHubClient{
		err: &service.HubInvitationError{HubStatus: 422, Code: "self_invitation", Message: "same email"},
	}
	h := newInviteHandler(t, hubStub, authSvc, &stubHubUserIDResolver{hubUserID: "hub-x"}, true)
	// Email DIFFERENT du caller pour passer la verif locale (l'alias serait
	// resolu cote Hub).
	req := buildInviteReq(t, `{"workspace_id":"ws-1","email":"other@x.com"}`)
	rec := httptest.NewRecorder()
	h.handleInvite(rec, req)
	assert.Equal(t, http.StatusBadRequest, rec.Code)
	assert.Contains(t, rec.Body.String(), "yourself")
}

func TestVeridianInviteMember_HubDisabled_503(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	authSvc := mocks.NewMockAuthService(ctrl)
	authSvc.EXPECT().
		AuthenticateUserForWorkspace(gomock.Any(), "ws-1").
		Return(context.Background(), &domain.User{ID: "u", Type: domain.UserTypeUser, Email: "o@x.com"}, &domain.UserWorkspace{}, nil)

	hubStub := &stubHubClient{err: service.ErrHubInvitationDisabled}
	h := newInviteHandler(t, hubStub, authSvc, &stubHubUserIDResolver{hubUserID: "hub-x"}, true)
	req := buildInviteReq(t, `{"workspace_id":"ws-1","email":"e@e.com"}`)
	rec := httptest.NewRecorder()
	h.handleInvite(rec, req)
	assert.Equal(t, http.StatusServiceUnavailable, rec.Code)
	assert.Contains(t, rec.Body.String(), "HUB_INVITATION_SECRET_NOTIFUSE")
}

// ─── RegisterRoutes + smoke ──────────────────────────────────────────────────

func TestVeridianInviteMember_RegisterRoutes(t *testing.T) {
	h := newInviteHandler(t, &stubHubClient{}, nil, &stubHubUserIDResolver{}, true)
	mux := http.NewServeMux()
	h.RegisterRoutes(mux)

	req := httptest.NewRequest(http.MethodPost, "/api/veridian/workspaces.inviteMember", nil)
	_, pattern := mux.Handler(req)
	assert.NotEmpty(t, pattern, "POST /api/veridian/workspaces.inviteMember should be mounted")
}

func TestVeridianInviteMember_RegisterRoutes_RequiresAuth(t *testing.T) {
	// Smoke : sans Authorization header, middleware doit rejeter 401.
	h := newInviteHandler(t, &stubHubClient{}, nil, &stubHubUserIDResolver{}, true)
	mux := http.NewServeMux()
	h.RegisterRoutes(mux)

	server := httptest.NewServer(mux)
	defer server.Close()

	resp, err := http.Post(server.URL+"/api/veridian/workspaces.inviteMember", "application/json", bytes.NewReader([]byte(`{}`)))
	require.NoError(t, err)
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	assert.Equal(t, http.StatusUnauthorized, resp.StatusCode, "body=%s", body)
}
