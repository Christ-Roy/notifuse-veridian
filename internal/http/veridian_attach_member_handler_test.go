package http

// === Veridian patch — hub-attach-member (2026-05-21) ===
// Tests unitaires du handler handleAttachMember.
// Couvre : 201 OK, 200 idempotent, 400 body invalide, 400 role invalide,
// 400 champs manquants, 404 tenant inconnu, 423 suspendu, 409 race condition,
// 500 erreur service, HMAC valide/invalide (via middleware testé séparément),
// path param tenantId vide.
//
// Note HMAC : les tests de middleware HMAC (401 signature invalide, drift
// timestamp, etc.) sont dans veridian_hmac_test.go — le middleware est le
// même sur cette route que sur les autres routes Hub→Notifuse.
// handleAttachMember est testé sans middleware (isolé), exactement comme
// handleProvision, handleAttachOwner, etc. dans veridian_handler_test.go.

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Notifuse/notifuse/internal/domain"
	"github.com/Notifuse/notifuse/internal/domain/mocks"
	"github.com/Notifuse/notifuse/internal/service"
	"github.com/golang/mock/gomock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// postAttachMemberWithTenantID simule un POST /api/tenants/{tenantId}/attach-member
// avec le path value "tenantId" (≠ "id" utilisé par les autres routes du handler).
func postAttachMemberWithTenantID(t *testing.T, h func(http.ResponseWriter, *http.Request), tenantID, body string) *httptest.ResponseRecorder {
	t.Helper()
	var bodyReader *bytes.Reader
	if body != "" {
		bodyReader = bytes.NewReader([]byte(body))
	} else {
		bodyReader = bytes.NewReader(nil)
	}
	req := httptest.NewRequest(http.MethodPost, "/api/tenants/"+tenantID+"/attach-member", bodyReader)
	req.SetPathValue("tenantId", tenantID)
	rec := httptest.NewRecorder()
	h(rec, req)
	return rec
}

// helper : simule un POST /api/tenants/{tenantId}/attach-member avec path value.
func postAttachMember(t *testing.T, h func(http.ResponseWriter, *http.Request), tenantID, body string) *httptest.ResponseRecorder {
	t.Helper()
	return postAttachMemberWithTenantID(t, h, tenantID, body)
}

// postAttachMemberNoPath envoie la requete sans path value (pour tester le cas vide).
func postAttachMemberNoPath(t *testing.T, h func(http.ResponseWriter, *http.Request), body string) *httptest.ResponseRecorder {
	t.Helper()
	// path value vide : SetPathValue("tenantId", "") → handler retourne 400
	var bodyReader *bytes.Reader
	if body != "" {
		bodyReader = bytes.NewReader([]byte(body))
	} else {
		bodyReader = bytes.NewReader(nil)
	}
	req := httptest.NewRequest(http.MethodPost, "/api/tenants//attach-member", bodyReader)
	req.SetPathValue("tenantId", "")
	rec := httptest.NewRecorder()
	h(rec, req)
	return rec
}

// === Tests handleAttachMember ===

func TestHandleAttachMember_FirstAttach_Returns201(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	svc := mocks.NewMockVeridianService(ctrl)
	svc.EXPECT().AttachMember(gomock.Any(), gomock.Any()).Return(&domain.AttachMemberResponse{
		Attached:      true,
		AlreadyMember: false,
		WorkspaceID:   "ws-1",
		Role:          "member",
		LoginURL:      "https://notifuse.app.veridian.site/veridian/auto-login?token=abc",
	}, nil)

	h := newHandlerWithService(svc)
	rec := postAttachMember(t, h.handleAttachMember, "ws-1",
		`{"hub_user_id":"u-hub-1","hub_user_email":"alice@example.com","role":"member","invitation_id":"inv-1"}`)

	assert.Equal(t, http.StatusCreated, rec.Code)
	var resp domain.AttachMemberResponse
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp))
	assert.True(t, resp.Attached)
	assert.False(t, resp.AlreadyMember)
	assert.Equal(t, "ws-1", resp.WorkspaceID)
	assert.Equal(t, "member", resp.Role)
	assert.NotEmpty(t, resp.LoginURL)
}

func TestHandleAttachMember_AlreadyMember_Returns200(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	svc := mocks.NewMockVeridianService(ctrl)
	svc.EXPECT().AttachMember(gomock.Any(), gomock.Any()).Return(&domain.AttachMemberResponse{
		Attached:      true,
		AlreadyMember: true,
		WorkspaceID:   "ws-1",
		Role:          "member",
		LoginURL:      "https://notifuse.app.veridian.site/veridian/auto-login?token=fresh",
	}, nil)

	h := newHandlerWithService(svc)
	rec := postAttachMember(t, h.handleAttachMember, "ws-1",
		`{"hub_user_id":"u-hub-1","hub_user_email":"alice@example.com","role":"member"}`)

	// Idempotent re-call → 200 (pas 201)
	assert.Equal(t, http.StatusOK, rec.Code)
	var resp domain.AttachMemberResponse
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp))
	assert.True(t, resp.AlreadyMember)
}

func TestHandleAttachMember_InvalidJSON_Returns400(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	svc := mocks.NewMockVeridianService(ctrl) // pas d'EXPECT — service jamais appelé
	h := newHandlerWithService(svc)

	rec := postAttachMember(t, h.handleAttachMember, "ws-1", `{not valid json`)
	assert.Equal(t, http.StatusBadRequest, rec.Code)
	assert.Contains(t, rec.Body.String(), "invalid JSON body")
}

func TestHandleAttachMember_MissingFields_Returns400(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	svc := mocks.NewMockVeridianService(ctrl)
	h := newHandlerWithService(svc)

	cases := []struct {
		body    string
		missing string
	}{
		{`{"hub_user_email":"a@x.test","role":"member"}`, "hub_user_id"},
		{`{"hub_user_id":"u-1","role":"member"}`, "hub_user_email"},
		{`{"hub_user_id":"u-1","hub_user_email":"a@x.test"}`, "role"},
		{`{}`, "hub_user_id"},
	}

	for _, tc := range cases {
		rec := postAttachMember(t, h.handleAttachMember, "ws-1", tc.body)
		assert.Equal(t, http.StatusBadRequest, rec.Code, "body=%s", tc.body)
		assert.Contains(t, rec.Body.String(), "required", "body=%s", tc.body)
	}
}

func TestHandleAttachMember_InvalidRole_Returns400(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	svc := mocks.NewMockVeridianService(ctrl)
	h := newHandlerWithService(svc)

	rec := postAttachMember(t, h.handleAttachMember, "ws-1",
		`{"hub_user_id":"u-1","hub_user_email":"a@x.test","role":"superuser"}`)
	assert.Equal(t, http.StatusBadRequest, rec.Code)
	assert.Contains(t, rec.Body.String(), ErrCodeInvalidRole)
}

func TestHandleAttachMember_TenantNotFound_Returns404(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	svc := mocks.NewMockVeridianService(ctrl)
	svc.EXPECT().AttachMember(gomock.Any(), gomock.Any()).Return(nil, sql.ErrNoRows)

	h := newHandlerWithService(svc)
	rec := postAttachMember(t, h.handleAttachMember, "ws-unknown",
		`{"hub_user_id":"u-1","hub_user_email":"a@x.test","role":"member"}`)

	assert.Equal(t, http.StatusNotFound, rec.Code)
	assert.Contains(t, rec.Body.String(), ErrCodeTenantNotFound)
}

func TestHandleAttachMember_TenantNotFound_ErrorString_Returns404(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	svc := mocks.NewMockVeridianService(ctrl)
	svc.EXPECT().AttachMember(gomock.Any(), gomock.Any()).Return(nil, errors.New("workspace ws-x not found"))

	h := newHandlerWithService(svc)
	rec := postAttachMember(t, h.handleAttachMember, "ws-x",
		`{"hub_user_id":"u-1","hub_user_email":"a@x.test","role":"member"}`)

	assert.Equal(t, http.StatusNotFound, rec.Code)
}

func TestHandleAttachMember_TenantSuspended_Returns423(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	svc := mocks.NewMockVeridianService(ctrl)
	svc.EXPECT().AttachMember(gomock.Any(), gomock.Any()).Return(nil, service.ErrTenantSuspended)

	h := newHandlerWithService(svc)
	rec := postAttachMember(t, h.handleAttachMember, "ws-suspended",
		`{"hub_user_id":"u-1","hub_user_email":"a@x.test","role":"member"}`)

	assert.Equal(t, http.StatusLocked, rec.Code)
	assert.Contains(t, rec.Body.String(), ErrCodeTenantSuspended)
}

func TestHandleAttachMember_TenantSuspendedWrapped_Returns423(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	svc := mocks.NewMockVeridianService(ctrl)
	wrappedErr := errors.Join(service.ErrTenantSuspended, errors.New("reason: trial expired"))
	svc.EXPECT().AttachMember(gomock.Any(), gomock.Any()).Return(nil, wrappedErr)

	h := newHandlerWithService(svc)
	rec := postAttachMember(t, h.handleAttachMember, "ws-suspended",
		`{"hub_user_id":"u-1","hub_user_email":"a@x.test","role":"member"}`)

	assert.Equal(t, http.StatusLocked, rec.Code)
}

func TestHandleAttachMember_UserRoleConflict_Returns409(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	svc := mocks.NewMockVeridianService(ctrl)
	svc.EXPECT().AttachMember(gomock.Any(), gomock.Any()).Return(nil, service.ErrUserRoleConflict)

	h := newHandlerWithService(svc)
	rec := postAttachMember(t, h.handleAttachMember, "ws-1",
		`{"hub_user_id":"u-1","hub_user_email":"a@x.test","role":"member"}`)

	assert.Equal(t, http.StatusConflict, rec.Code)
	assert.Contains(t, rec.Body.String(), ErrCodeUserRoleConflict)
}

func TestHandleAttachMember_ServiceError_Returns500(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	svc := mocks.NewMockVeridianService(ctrl)
	svc.EXPECT().AttachMember(gomock.Any(), gomock.Any()).Return(nil, errors.New("db connection refused"))

	h := newHandlerWithService(svc)
	rec := postAttachMember(t, h.handleAttachMember, "ws-1",
		`{"hub_user_id":"u-1","hub_user_email":"a@x.test","role":"member"}`)

	assert.Equal(t, http.StatusInternalServerError, rec.Code)
	// Le 500 ne doit PAS leak l'erreur interne brute au client (axe 2) : message
	// générique côté client, erreur brute confinée aux logs.
	assert.NotContains(t, rec.Body.String(), "db connection refused", "le 500 ne doit pas leak l'erreur interne")
	assert.Contains(t, rec.Body.String(), "internal_error")
}

func TestHandleAttachMember_EmptyTenantID_Returns400(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	svc := mocks.NewMockVeridianService(ctrl)
	h := newHandlerWithService(svc)

	// path value vide → handler doit retourner 400 avant d'appeler le service
	rec := postAttachMemberNoPath(t, h.handleAttachMember,
		`{"hub_user_id":"u-1","hub_user_email":"a@x.test","role":"member"}`)
	assert.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestHandleAttachMember_AllValidRoles_Accept(t *testing.T) {
	for _, role := range []string{"owner", "admin", "member"} {
		role := role
		t.Run(role, func(t *testing.T) {
			ctrl := gomock.NewController(t)
			defer ctrl.Finish()

			svc := mocks.NewMockVeridianService(ctrl)
			svc.EXPECT().AttachMember(gomock.Any(), gomock.Any()).Return(&domain.AttachMemberResponse{
				Attached:    true,
				WorkspaceID: "ws-1",
				Role:        role,
			}, nil)

			h := newHandlerWithService(svc)
			body := `{"hub_user_id":"u-1","hub_user_email":"a@x.test","role":"` + role + `"}`
			rec := postAttachMember(t, h.handleAttachMember, "ws-1", body)
			assert.Equal(t, http.StatusCreated, rec.Code, "role=%s", role)
		})
	}
}
