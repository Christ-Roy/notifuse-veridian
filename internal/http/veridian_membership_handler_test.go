package http

// === Veridian patch — v1.3 Multi-membre cross-app (2026-05-19) ===
// Tests unitaires des handlers handleSyncMember, handleRemoveMember,
// handleRestoreMember. Couvre les codes HTTP 200/400/404/409/422/500 et le
// mapping des erreurs sentinelles (ErrCannotRemoveOwner, sql.ErrNoRows).
//
// HMAC : tests middleware separes dans veridian_hmac_test.go — ici on isole
// la logique handler comme veridian_handler_test.go et
// veridian_attach_member_handler_test.go.

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/Notifuse/notifuse/internal/domain"
	"github.com/Notifuse/notifuse/internal/domain/mocks"
	"github.com/Notifuse/notifuse/internal/service"
	"github.com/golang/mock/gomock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// helper : POST sur un endpoint /api/tenants/{id}/<verb> avec path value "id".
func postWithTenantID(t *testing.T, h func(http.ResponseWriter, *http.Request), tenantID, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	var bodyReader *bytes.Reader
	if body != "" {
		bodyReader = bytes.NewReader([]byte(body))
	} else {
		bodyReader = bytes.NewReader(nil)
	}
	req := httptest.NewRequest(http.MethodPost, path, bodyReader)
	req.SetPathValue("id", tenantID)
	rec := httptest.NewRecorder()
	h(rec, req)
	return rec
}

// === handleSyncMember ===================================================

func TestHandleSyncMember_FirstSync_Returns200(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	svc := mocks.NewMockVeridianService(ctrl)
	svc.EXPECT().SyncMember(gomock.Any(), gomock.Any()).Return(&domain.SyncMemberResponse{
		TenantID:  "ws-1",
		UserEmail: "alice@example.com",
		Synced:    true,
		AppUserID: "user-uuid-1",
		AppRole:   "member",
	}, nil)

	h := newHandlerWithService(svc)
	rec := postWithTenantID(t, h.handleSyncMember, "ws-1", "/api/tenants/ws-1/sync-member",
		`{"user_email":"alice@example.com","hub_user_id":"hub-u-1","role":"member"}`)

	assert.Equal(t, http.StatusOK, rec.Code)
	var resp domain.SyncMemberResponse
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp))
	assert.True(t, resp.Synced)
	assert.Equal(t, "user-uuid-1", resp.AppUserID)
	assert.Equal(t, "member", resp.AppRole)
}

func TestHandleSyncMember_OwnerKept_NoDowngrade(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	svc := mocks.NewMockVeridianService(ctrl)
	// Idempotent : user deja owner, service garde owner (additif).
	svc.EXPECT().SyncMember(gomock.Any(), gomock.Any()).Return(&domain.SyncMemberResponse{
		TenantID:  "ws-1",
		UserEmail: "owner@example.com",
		Synced:    true,
		AppUserID: "user-uuid-owner",
		AppRole:   "owner",
	}, nil)

	h := newHandlerWithService(svc)
	rec := postWithTenantID(t, h.handleSyncMember, "ws-1", "/api/tenants/ws-1/sync-member",
		`{"user_email":"owner@example.com","hub_user_id":"hub-owner","role":"member"}`)

	assert.Equal(t, http.StatusOK, rec.Code)
	var resp domain.SyncMemberResponse
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp))
	assert.Equal(t, "owner", resp.AppRole, "must not downgrade existing owner")
}

func TestHandleSyncMember_InvalidJSON_Returns400(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	svc := mocks.NewMockVeridianService(ctrl)
	h := newHandlerWithService(svc)

	rec := postWithTenantID(t, h.handleSyncMember, "ws-1", "/api/tenants/ws-1/sync-member", `{not json`)
	assert.Equal(t, http.StatusBadRequest, rec.Code)
	assert.Contains(t, rec.Body.String(), "invalid JSON body")
}

func TestHandleSyncMember_MissingFields_Returns400(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	svc := mocks.NewMockVeridianService(ctrl)
	h := newHandlerWithService(svc)

	cases := []struct {
		body    string
		missing string
	}{
		{`{"hub_user_id":"u-1","role":"member"}`, "user_email"},
		{`{"user_email":"a@x.test","role":"member"}`, "hub_user_id"},
		{`{"user_email":"a@x.test","hub_user_id":"u-1"}`, "role"},
		{`{}`, "user_email"},
	}
	for _, tc := range cases {
		rec := postWithTenantID(t, h.handleSyncMember, "ws-1", "/api/tenants/ws-1/sync-member", tc.body)
		assert.Equal(t, http.StatusBadRequest, rec.Code, "body=%s", tc.body)
		assert.Contains(t, rec.Body.String(), "required", "body=%s", tc.body)
	}
}

func TestHandleSyncMember_MalformedEmail_Returns422(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	svc := mocks.NewMockVeridianService(ctrl)
	h := newHandlerWithService(svc)

	rec := postWithTenantID(t, h.handleSyncMember, "ws-1", "/api/tenants/ws-1/sync-member",
		`{"user_email":"not-an-email","hub_user_id":"u-1","role":"member"}`)
	assert.Equal(t, http.StatusUnprocessableEntity, rec.Code)
}

func TestHandleSyncMember_InvalidRole_Returns400(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	svc := mocks.NewMockVeridianService(ctrl)
	h := newHandlerWithService(svc)

	rec := postWithTenantID(t, h.handleSyncMember, "ws-1", "/api/tenants/ws-1/sync-member",
		`{"user_email":"a@x.test","hub_user_id":"u-1","role":"superuser"}`)
	assert.Equal(t, http.StatusBadRequest, rec.Code)
	assert.Contains(t, rec.Body.String(), ErrCodeInvalidRole)
}

func TestHandleSyncMember_OwnerRoleRefused(t *testing.T) {
	// owner ne doit pas etre accepte par sync-member (owner = provision ou transfer-owner).
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	svc := mocks.NewMockVeridianService(ctrl)
	h := newHandlerWithService(svc)

	rec := postWithTenantID(t, h.handleSyncMember, "ws-1", "/api/tenants/ws-1/sync-member",
		`{"user_email":"a@x.test","hub_user_id":"u-1","role":"owner"}`)
	assert.Equal(t, http.StatusBadRequest, rec.Code)
	assert.Contains(t, rec.Body.String(), ErrCodeInvalidRole)
}

func TestHandleSyncMember_TenantNotFound_Returns404(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	svc := mocks.NewMockVeridianService(ctrl)
	svc.EXPECT().SyncMember(gomock.Any(), gomock.Any()).Return(nil, sql.ErrNoRows)

	h := newHandlerWithService(svc)
	rec := postWithTenantID(t, h.handleSyncMember, "ws-unknown", "/api/tenants/ws-unknown/sync-member",
		`{"user_email":"a@x.test","hub_user_id":"u-1","role":"member"}`)

	assert.Equal(t, http.StatusNotFound, rec.Code)
	assert.Contains(t, rec.Body.String(), ErrCodeTenantNotFound)
}

func TestHandleSyncMember_EmptyTenantID_Returns400(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	svc := mocks.NewMockVeridianService(ctrl)
	h := newHandlerWithService(svc)

	// path value vide → 400 sans appeler le service.
	req := httptest.NewRequest(http.MethodPost, "/api/tenants//sync-member",
		bytes.NewReader([]byte(`{"user_email":"a@x.test","hub_user_id":"u-1","role":"member"}`)))
	req.SetPathValue("id", "")
	rec := httptest.NewRecorder()
	h.handleSyncMember(rec, req)
	assert.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestHandleSyncMember_ServiceError_Returns500(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	svc := mocks.NewMockVeridianService(ctrl)
	svc.EXPECT().SyncMember(gomock.Any(), gomock.Any()).Return(nil, errors.New("db connection refused"))

	h := newHandlerWithService(svc)
	rec := postWithTenantID(t, h.handleSyncMember, "ws-1", "/api/tenants/ws-1/sync-member",
		`{"user_email":"a@x.test","hub_user_id":"u-1","role":"member"}`)

	assert.Equal(t, http.StatusInternalServerError, rec.Code)
}

// === handleRemoveMember =================================================

func TestHandleRemoveMember_Success_Returns200(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	now := time.Now().UTC()
	svc := mocks.NewMockVeridianService(ctrl)
	svc.EXPECT().RemoveMember(gomock.Any(), gomock.Any()).Return(&domain.RemoveMemberResponse{
		TenantID:  "ws-1",
		UserEmail: "alice@example.com",
		RemovedAt: now,
	}, nil)

	h := newHandlerWithService(svc)
	rec := postWithTenantID(t, h.handleRemoveMember, "ws-1", "/api/tenants/ws-1/remove-member",
		`{"user_email":"alice@example.com","reason":"admin_action"}`)

	assert.Equal(t, http.StatusOK, rec.Code)
	var resp domain.RemoveMemberResponse
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp))
	assert.Equal(t, "alice@example.com", resp.UserEmail)
}

func TestHandleRemoveMember_Owner_Returns409(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	svc := mocks.NewMockVeridianService(ctrl)
	svc.EXPECT().RemoveMember(gomock.Any(), gomock.Any()).Return(nil, service.ErrCannotRemoveOwner)

	h := newHandlerWithService(svc)
	rec := postWithTenantID(t, h.handleRemoveMember, "ws-1", "/api/tenants/ws-1/remove-member",
		`{"user_email":"owner@example.com"}`)

	assert.Equal(t, http.StatusConflict, rec.Code)
	assert.Contains(t, rec.Body.String(), ErrCodeCannotRemoveOwner)
	assert.Contains(t, rec.Body.String(), "transfer-owner")
}

func TestHandleRemoveMember_TenantNotFound_Returns404(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	svc := mocks.NewMockVeridianService(ctrl)
	svc.EXPECT().RemoveMember(gomock.Any(), gomock.Any()).Return(nil, sql.ErrNoRows)

	h := newHandlerWithService(svc)
	rec := postWithTenantID(t, h.handleRemoveMember, "ws-unknown", "/api/tenants/ws-unknown/remove-member",
		`{"user_email":"alice@example.com"}`)

	assert.Equal(t, http.StatusNotFound, rec.Code)
	assert.Contains(t, rec.Body.String(), ErrCodeTenantNotFound)
}

func TestHandleRemoveMember_MissingEmail_Returns400(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	svc := mocks.NewMockVeridianService(ctrl)
	h := newHandlerWithService(svc)

	rec := postWithTenantID(t, h.handleRemoveMember, "ws-1", "/api/tenants/ws-1/remove-member", `{}`)
	assert.Equal(t, http.StatusBadRequest, rec.Code)
	assert.Contains(t, rec.Body.String(), "user_email")
}

func TestHandleRemoveMember_InvalidJSON_Returns400(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	svc := mocks.NewMockVeridianService(ctrl)
	h := newHandlerWithService(svc)

	rec := postWithTenantID(t, h.handleRemoveMember, "ws-1", "/api/tenants/ws-1/remove-member", `{not json`)
	assert.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestHandleRemoveMember_EmptyTenantID_Returns400(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	svc := mocks.NewMockVeridianService(ctrl)
	h := newHandlerWithService(svc)

	req := httptest.NewRequest(http.MethodPost, "/api/tenants//remove-member",
		bytes.NewReader([]byte(`{"user_email":"a@x.test"}`)))
	req.SetPathValue("id", "")
	rec := httptest.NewRecorder()
	h.handleRemoveMember(rec, req)
	assert.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestHandleRemoveMember_ServiceError_Returns500(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	svc := mocks.NewMockVeridianService(ctrl)
	svc.EXPECT().RemoveMember(gomock.Any(), gomock.Any()).Return(nil, errors.New("boom"))

	h := newHandlerWithService(svc)
	rec := postWithTenantID(t, h.handleRemoveMember, "ws-1", "/api/tenants/ws-1/remove-member",
		`{"user_email":"a@x.test"}`)

	assert.Equal(t, http.StatusInternalServerError, rec.Code)
}

// === handleRestoreMember ================================================

func TestHandleRestoreMember_Success_Returns200(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	now := time.Now().UTC()
	svc := mocks.NewMockVeridianService(ctrl)
	svc.EXPECT().RestoreMember(gomock.Any(), gomock.Any()).Return(&domain.RestoreMemberResponse{
		TenantID:   "ws-1",
		UserEmail:  "alice@example.com",
		RestoredAt: now,
	}, nil)

	h := newHandlerWithService(svc)
	rec := postWithTenantID(t, h.handleRestoreMember, "ws-1", "/api/tenants/ws-1/restore-member",
		`{"user_email":"alice@example.com"}`)

	assert.Equal(t, http.StatusOK, rec.Code)
	var resp domain.RestoreMemberResponse
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp))
	assert.Equal(t, "alice@example.com", resp.UserEmail)
}

func TestHandleRestoreMember_TenantNotFound_Returns404(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	svc := mocks.NewMockVeridianService(ctrl)
	svc.EXPECT().RestoreMember(gomock.Any(), gomock.Any()).Return(nil, sql.ErrNoRows)

	h := newHandlerWithService(svc)
	rec := postWithTenantID(t, h.handleRestoreMember, "ws-unknown", "/api/tenants/ws-unknown/restore-member",
		`{"user_email":"alice@example.com"}`)

	assert.Equal(t, http.StatusNotFound, rec.Code)
	assert.Contains(t, rec.Body.String(), ErrCodeTenantNotFound)
}

func TestHandleRestoreMember_MissingEmail_Returns400(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	svc := mocks.NewMockVeridianService(ctrl)
	h := newHandlerWithService(svc)

	rec := postWithTenantID(t, h.handleRestoreMember, "ws-1", "/api/tenants/ws-1/restore-member", `{}`)
	assert.Equal(t, http.StatusBadRequest, rec.Code)
	assert.Contains(t, rec.Body.String(), "user_email")
}

func TestHandleRestoreMember_InvalidJSON_Returns400(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	svc := mocks.NewMockVeridianService(ctrl)
	h := newHandlerWithService(svc)

	rec := postWithTenantID(t, h.handleRestoreMember, "ws-1", "/api/tenants/ws-1/restore-member", `{not json`)
	assert.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestHandleRestoreMember_EmptyTenantID_Returns400(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	svc := mocks.NewMockVeridianService(ctrl)
	h := newHandlerWithService(svc)

	req := httptest.NewRequest(http.MethodPost, "/api/tenants//restore-member",
		bytes.NewReader([]byte(`{"user_email":"a@x.test"}`)))
	req.SetPathValue("id", "")
	rec := httptest.NewRecorder()
	h.handleRestoreMember(rec, req)
	assert.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestHandleRestoreMember_ServiceError_Returns500(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	svc := mocks.NewMockVeridianService(ctrl)
	svc.EXPECT().RestoreMember(gomock.Any(), gomock.Any()).Return(nil, errors.New("boom"))

	h := newHandlerWithService(svc)
	rec := postWithTenantID(t, h.handleRestoreMember, "ws-1", "/api/tenants/ws-1/restore-member",
		`{"user_email":"a@x.test"}`)

	assert.Equal(t, http.StatusInternalServerError, rec.Code)
}

// === Validation domain types (route-coverage filler) ====================

func TestSyncMemberRole_IsValid(t *testing.T) {
	assert.True(t, domain.SyncMemberRoleMember.IsValid())
	assert.True(t, domain.SyncMemberRoleAdmin.IsValid())
	assert.False(t, domain.SyncMemberRole("owner").IsValid())
	assert.False(t, domain.SyncMemberRole("").IsValid())
	assert.False(t, domain.SyncMemberRole("admin2").IsValid())
}
