package http

// === Veridian patch — Freeze member per-user (CONTRAT-HUB §5.21) ===
// Tests unitaires de handleFreezeMember + handleUnfreezeMember.
//
// Couvre :
//   - 200 success freeze / unfreeze
//   - 409 cannot_freeze_owner (owner refuse)
//   - 409 member_already_frozen (idempotent freeze)
//   - 404 tenant_not_found (sql.ErrNoRows)
//   - 404 user_not_member (ErrMemberNotInWorkspace)
//   - 400 validation (missing email, hub_user_id, malformed JSON)
//   - 422 invalid email format (sans @)
//   - 400 invalid reason
//   - 503 ErrFrozenMemberRepoNotConfigured

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

// helper : POST sur un endpoint /api/tenants/{tenantId}/<verb>.
func postFreezeRequest(t *testing.T, h func(http.ResponseWriter, *http.Request), tenantID, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, path, bytes.NewReader([]byte(body)))
	req.SetPathValue("tenantId", tenantID)
	rec := httptest.NewRecorder()
	h(rec, req)
	return rec
}

// === handleFreezeMember =====================================================

func TestHandleFreezeMember_Success_Returns200(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	svc := mocks.NewMockVeridianService(ctrl)
	svc.EXPECT().FreezeMember(gomock.Any(), gomock.Any()).Return(&domain.FreezeMemberResponse{
		TenantID:  "ws-1",
		UserEmail: "bob@example.com",
		HubUserID: "hub-u-bob",
		Reason:    domain.FreezeReasonQuotaSeatExceeded,
	}, false, nil)

	h := newHandlerWithService(svc)
	rec := postFreezeRequest(t, h.handleFreezeMember, "ws-1", "/api/tenants/ws-1/freeze-member",
		`{"user_email":"bob@example.com","hub_user_id":"hub-u-bob","reason":"quota_seat_exceeded"}`)

	assert.Equal(t, http.StatusOK, rec.Code)
	var resp domain.FreezeMemberResponse
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp))
	assert.Equal(t, "ws-1", resp.TenantID)
	assert.Equal(t, domain.FreezeReasonQuotaSeatExceeded, resp.Reason)
}

func TestHandleFreezeMember_AlreadyFrozen_Returns409(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	svc := mocks.NewMockVeridianService(ctrl)
	svc.EXPECT().FreezeMember(gomock.Any(), gomock.Any()).Return(&domain.FreezeMemberResponse{
		TenantID:  "ws-1",
		UserEmail: "bob@example.com",
		Reason:    domain.FreezeReasonManual,
	}, true, nil)

	h := newHandlerWithService(svc)
	rec := postFreezeRequest(t, h.handleFreezeMember, "ws-1", "/api/tenants/ws-1/freeze-member",
		`{"user_email":"bob@example.com","hub_user_id":"hub-u-bob"}`)

	assert.Equal(t, http.StatusConflict, rec.Code)
	var body map[string]interface{}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
	assert.Equal(t, ErrCodeMemberAlreadyFrozen, body["code"])
}

func TestHandleFreezeMember_Owner_Returns409CannotFreezeOwner(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	svc := mocks.NewMockVeridianService(ctrl)
	svc.EXPECT().FreezeMember(gomock.Any(), gomock.Any()).Return(nil, false, service.ErrCannotFreezeOwner)

	h := newHandlerWithService(svc)
	rec := postFreezeRequest(t, h.handleFreezeMember, "ws-1", "/api/tenants/ws-1/freeze-member",
		`{"user_email":"owner@x.test","hub_user_id":"hub-u-owner"}`)

	assert.Equal(t, http.StatusConflict, rec.Code)
	var body map[string]interface{}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
	assert.Equal(t, ErrCodeCannotFreezeOwner, body["code"])
}

func TestHandleFreezeMember_UserNotMember_Returns404(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	svc := mocks.NewMockVeridianService(ctrl)
	svc.EXPECT().FreezeMember(gomock.Any(), gomock.Any()).Return(nil, false, service.ErrMemberNotInWorkspace)

	h := newHandlerWithService(svc)
	rec := postFreezeRequest(t, h.handleFreezeMember, "ws-1", "/api/tenants/ws-1/freeze-member",
		`{"user_email":"ghost@x.test","hub_user_id":"hub-u-ghost"}`)

	assert.Equal(t, http.StatusNotFound, rec.Code)
	var body map[string]interface{}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
	assert.Equal(t, ErrCodeUserNotMember, body["code"])
}

func TestHandleFreezeMember_TenantNotFound_Returns404(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	svc := mocks.NewMockVeridianService(ctrl)
	svc.EXPECT().FreezeMember(gomock.Any(), gomock.Any()).Return(nil, false, sql.ErrNoRows)

	h := newHandlerWithService(svc)
	rec := postFreezeRequest(t, h.handleFreezeMember, "ws-unknown", "/api/tenants/ws-unknown/freeze-member",
		`{"user_email":"bob@x.test","hub_user_id":"u-1"}`)

	assert.Equal(t, http.StatusNotFound, rec.Code)
	var body map[string]interface{}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
	assert.Equal(t, ErrCodeTenantNotFound, body["code"])
}

// TestHandleFreezeMember_GenericServiceError_Returns500NoLeak couvre le
// fallthrough 500 non-classifié : une erreur service brute (DB/infra) ne doit
// JAMAIS être renvoyée telle quelle au client (axe 2). Message générique côté
// client, code machine internal_error exposé, erreur brute confinée aux logs.
func TestHandleFreezeMember_GenericServiceError_Returns500NoLeak(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	svc := mocks.NewMockVeridianService(ctrl)
	svc.EXPECT().FreezeMember(gomock.Any(), gomock.Any()).Return(nil, false, errors.New("pq: db connection lost"))

	h := newHandlerWithService(svc)
	rec := postFreezeRequest(t, h.handleFreezeMember, "ws-1", "/api/tenants/ws-1/freeze-member",
		`{"user_email":"bob@x.test","hub_user_id":"u-1"}`)

	assert.Equal(t, http.StatusInternalServerError, rec.Code)
	assert.NotContains(t, rec.Body.String(), "pq:", "le 500 ne doit pas leak l'erreur interne brute")
	assert.NotContains(t, rec.Body.String(), "db connection lost", "le 500 ne doit pas leak l'erreur interne brute")
	var body map[string]interface{}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
	assert.Equal(t, ErrCodeInternalError, body["code"], "le code machine internal_error doit rester exposé")
}

func TestHandleFreezeMember_MissingUserEmail_Returns400(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	svc := mocks.NewMockVeridianService(ctrl)
	// Pas d'EXPECT().FreezeMember : validation precoce avant service.

	h := newHandlerWithService(svc)
	rec := postFreezeRequest(t, h.handleFreezeMember, "ws-1", "/api/tenants/ws-1/freeze-member",
		`{"hub_user_id":"u-1"}`)

	assert.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestHandleFreezeMember_MissingHubUserID_Returns400(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	svc := mocks.NewMockVeridianService(ctrl)
	h := newHandlerWithService(svc)
	rec := postFreezeRequest(t, h.handleFreezeMember, "ws-1", "/api/tenants/ws-1/freeze-member",
		`{"user_email":"a@x.test"}`)

	assert.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestHandleFreezeMember_InvalidJSON_Returns400(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	svc := mocks.NewMockVeridianService(ctrl)
	h := newHandlerWithService(svc)
	rec := postFreezeRequest(t, h.handleFreezeMember, "ws-1", "/api/tenants/ws-1/freeze-member",
		`not-json-at-all`)

	assert.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestHandleFreezeMember_MalformedEmail_Returns422(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	svc := mocks.NewMockVeridianService(ctrl)
	h := newHandlerWithService(svc)
	rec := postFreezeRequest(t, h.handleFreezeMember, "ws-1", "/api/tenants/ws-1/freeze-member",
		`{"user_email":"not-an-email","hub_user_id":"u-1"}`)

	assert.Equal(t, http.StatusUnprocessableEntity, rec.Code)
}

func TestHandleFreezeMember_InvalidReason_Returns400(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	svc := mocks.NewMockVeridianService(ctrl)
	h := newHandlerWithService(svc)
	rec := postFreezeRequest(t, h.handleFreezeMember, "ws-1", "/api/tenants/ws-1/freeze-member",
		`{"user_email":"bob@x.test","hub_user_id":"u-1","reason":"super_invalid_reason"}`)

	assert.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestHandleFreezeMember_RepoNotConfigured_Returns503(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	svc := mocks.NewMockVeridianService(ctrl)
	svc.EXPECT().FreezeMember(gomock.Any(), gomock.Any()).Return(nil, false, service.ErrFrozenMemberRepoNotConfigured)

	h := newHandlerWithService(svc)
	rec := postFreezeRequest(t, h.handleFreezeMember, "ws-1", "/api/tenants/ws-1/freeze-member",
		`{"user_email":"bob@x.test","hub_user_id":"u-1"}`)

	assert.Equal(t, http.StatusServiceUnavailable, rec.Code)
}

func TestHandleFreezeMember_MissingTenantID_Returns400(t *testing.T) {
	// Pas de SetPathValue → tenantId vide.
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	svc := mocks.NewMockVeridianService(ctrl)
	h := newHandlerWithService(svc)

	req := httptest.NewRequest(http.MethodPost, "/api/tenants//freeze-member", bytes.NewReader([]byte(`{"user_email":"a@x.test","hub_user_id":"u"}`)))
	rec := httptest.NewRecorder()
	h.handleFreezeMember(rec, req)

	assert.Equal(t, http.StatusBadRequest, rec.Code)
}

// === handleUnfreezeMember ===================================================

func TestHandleUnfreezeMember_Success_Returns200(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	svc := mocks.NewMockVeridianService(ctrl)
	svc.EXPECT().UnfreezeMember(gomock.Any(), gomock.Any()).Return(&domain.UnfreezeMemberResponse{
		TenantID:  "ws-1",
		UserEmail: "bob@example.com",
		HubUserID: "hub-u-bob",
	}, true, nil)

	h := newHandlerWithService(svc)
	rec := postFreezeRequest(t, h.handleUnfreezeMember, "ws-1", "/api/tenants/ws-1/unfreeze-member",
		`{"user_email":"bob@example.com","hub_user_id":"hub-u-bob"}`)

	assert.Equal(t, http.StatusOK, rec.Code)
	var resp domain.UnfreezeMemberResponse
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp))
	assert.Equal(t, "ws-1", resp.TenantID)
}

func TestHandleUnfreezeMember_NotFrozen_Still200Idempotent(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	// Service signale wasFrozen=false (idempotent path). Handler renvoie quand
	// meme 200 conformement au contrat — pas de 404 surprise.
	svc := mocks.NewMockVeridianService(ctrl)
	svc.EXPECT().UnfreezeMember(gomock.Any(), gomock.Any()).Return(&domain.UnfreezeMemberResponse{
		TenantID:  "ws-1",
		UserEmail: "bob@example.com",
	}, false, nil)

	h := newHandlerWithService(svc)
	rec := postFreezeRequest(t, h.handleUnfreezeMember, "ws-1", "/api/tenants/ws-1/unfreeze-member",
		`{"user_email":"bob@example.com","hub_user_id":"u-1"}`)

	assert.Equal(t, http.StatusOK, rec.Code)
}

func TestHandleUnfreezeMember_UserNotMember_Returns404(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	svc := mocks.NewMockVeridianService(ctrl)
	svc.EXPECT().UnfreezeMember(gomock.Any(), gomock.Any()).Return(nil, false, service.ErrMemberNotInWorkspace)

	h := newHandlerWithService(svc)
	rec := postFreezeRequest(t, h.handleUnfreezeMember, "ws-1", "/api/tenants/ws-1/unfreeze-member",
		`{"user_email":"ghost@x.test","hub_user_id":"u-1"}`)

	assert.Equal(t, http.StatusNotFound, rec.Code)
	var body map[string]interface{}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
	assert.Equal(t, ErrCodeUserNotMember, body["code"])
}

func TestHandleUnfreezeMember_TenantNotFound_Returns404(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	svc := mocks.NewMockVeridianService(ctrl)
	svc.EXPECT().UnfreezeMember(gomock.Any(), gomock.Any()).Return(nil, false, sql.ErrNoRows)

	h := newHandlerWithService(svc)
	rec := postFreezeRequest(t, h.handleUnfreezeMember, "ws-unknown", "/api/tenants/ws-unknown/unfreeze-member",
		`{"user_email":"bob@x.test","hub_user_id":"u-1"}`)

	assert.Equal(t, http.StatusNotFound, rec.Code)
}

func TestHandleUnfreezeMember_MissingFields_Returns400(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	svc := mocks.NewMockVeridianService(ctrl)
	h := newHandlerWithService(svc)
	rec := postFreezeRequest(t, h.handleUnfreezeMember, "ws-1", "/api/tenants/ws-1/unfreeze-member",
		`{}`)

	assert.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestHandleUnfreezeMember_InvalidJSON_Returns400(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	svc := mocks.NewMockVeridianService(ctrl)
	h := newHandlerWithService(svc)
	rec := postFreezeRequest(t, h.handleUnfreezeMember, "ws-1", "/api/tenants/ws-1/unfreeze-member",
		`junk`)

	assert.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestHandleUnfreezeMember_RepoNotConfigured_Returns503(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	svc := mocks.NewMockVeridianService(ctrl)
	svc.EXPECT().UnfreezeMember(gomock.Any(), gomock.Any()).Return(nil, false, service.ErrFrozenMemberRepoNotConfigured)

	h := newHandlerWithService(svc)
	rec := postFreezeRequest(t, h.handleUnfreezeMember, "ws-1", "/api/tenants/ws-1/unfreeze-member",
		`{"user_email":"a@x.test","hub_user_id":"u-1"}`)

	assert.Equal(t, http.StatusServiceUnavailable, rec.Code)
}

func TestHandleUnfreezeMember_MissingTenantID_Returns400(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	svc := mocks.NewMockVeridianService(ctrl)
	h := newHandlerWithService(svc)

	req := httptest.NewRequest(http.MethodPost, "/api/tenants//unfreeze-member", bytes.NewReader([]byte(`{"user_email":"a@x.test","hub_user_id":"u"}`)))
	rec := httptest.NewRecorder()
	h.handleUnfreezeMember(rec, req)

	assert.Equal(t, http.StatusBadRequest, rec.Code)
}
