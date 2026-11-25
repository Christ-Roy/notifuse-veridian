package http

// === Veridian patch — Lot K (2026-05-21) ===
// Tests des handlers §5.15 rotate-api-key + §5.16 transfer-owner.

import (
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"testing"
	"time"

	"github.com/Notifuse/notifuse/internal/domain"
	"github.com/Notifuse/notifuse/internal/domain/mocks"
	"github.com/Notifuse/notifuse/internal/service"
	"github.com/golang/mock/gomock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// === handleRotateAPIKey ===

func TestVeridianHandleRotateAPIKey_OK(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	revokeAt := time.Date(2026, 5, 21, 12, 5, 0, 0, time.UTC)
	svc := mocks.NewMockVeridianService(ctrl)
	svc.EXPECT().RotateAPIKey(gomock.Any(), domain.RotateAPIKeyInput{
		TenantID: "ws-1",
		Reason:   "scheduled rotation 90j",
	}).Return(&domain.RotateAPIKeyResponse{
		TenantID:           "ws-1",
		NewAPIKey:          "jwt.new.token",
		NewAPIKeyEmail:     "veridian-api-ws-1-r12345@notifuse.app.veridian.site",
		OldAPIKeyRevokesAt: revokeAt,
	}, nil)

	h := newHandlerWithService(svc)
	rec := postWithPathValue(t, h.handleRotateAPIKey, http.MethodPost,
		"/api/tenants/ws-1/rotate-api-key", "ws-1",
		`{"reason":"scheduled rotation 90j"}`)

	assert.Equal(t, http.StatusOK, rec.Code)
	var resp domain.RotateAPIKeyResponse
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp))
	assert.Equal(t, "ws-1", resp.TenantID)
	assert.Equal(t, "jwt.new.token", resp.NewAPIKey)
	assert.Equal(t, "veridian-api-ws-1-r12345@notifuse.app.veridian.site", resp.NewAPIKeyEmail)
	assert.True(t, resp.OldAPIKeyRevokesAt.Equal(revokeAt))
}

func TestVeridianHandleRotateAPIKey_MissingPathID(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	svc := mocks.NewMockVeridianService(ctrl)
	h := newHandlerWithService(svc)

	rec := postWithPathValue(t, h.handleRotateAPIKey, http.MethodPost,
		"/api/tenants//rotate-api-key", "", `{"reason":"x"}`)
	assert.Equal(t, http.StatusBadRequest, rec.Code)
	assert.Contains(t, rec.Body.String(), "tenant id is required")
}

func TestVeridianHandleRotateAPIKey_MissingBody(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	svc := mocks.NewMockVeridianService(ctrl)
	h := newHandlerWithService(svc)

	rec := postWithPathValue(t, h.handleRotateAPIKey, http.MethodPost,
		"/api/tenants/ws-1/rotate-api-key", "ws-1", "")
	assert.Equal(t, http.StatusBadRequest, rec.Code)
	assert.Contains(t, rec.Body.String(), "reason")
}

func TestVeridianHandleRotateAPIKey_MissingReason(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	svc := mocks.NewMockVeridianService(ctrl)
	h := newHandlerWithService(svc)

	rec := postWithPathValue(t, h.handleRotateAPIKey, http.MethodPost,
		"/api/tenants/ws-1/rotate-api-key", "ws-1", `{}`)
	assert.Equal(t, http.StatusBadRequest, rec.Code)
	assert.Contains(t, rec.Body.String(), "reason is required")
}

func TestVeridianHandleRotateAPIKey_InvalidJSON(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	svc := mocks.NewMockVeridianService(ctrl)
	h := newHandlerWithService(svc)

	rec := postWithPathValue(t, h.handleRotateAPIKey, http.MethodPost,
		"/api/tenants/ws-1/rotate-api-key", "ws-1", `{not json`)
	assert.Equal(t, http.StatusBadRequest, rec.Code)
	assert.Contains(t, rec.Body.String(), "invalid JSON body")
}

func TestVeridianHandleRotateAPIKey_NotFound(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	svc := mocks.NewMockVeridianService(ctrl)
	svc.EXPECT().RotateAPIKey(gomock.Any(), gomock.Any()).Return(nil, sql.ErrNoRows)

	h := newHandlerWithService(svc)
	rec := postWithPathValue(t, h.handleRotateAPIKey, http.MethodPost,
		"/api/tenants/ws-x/rotate-api-key", "ws-x", `{"reason":"x"}`)
	assert.Equal(t, http.StatusNotFound, rec.Code)
	assert.Contains(t, rec.Body.String(), "tenant not found")
}

func TestVeridianHandleRotateAPIKey_GraceUnavailable503(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	svc := mocks.NewMockVeridianService(ctrl)
	svc.EXPECT().RotateAPIKey(gomock.Any(), gomock.Any()).
		Return(nil, service.ErrAPIKeyGraceRepoNotConfigured)

	h := newHandlerWithService(svc)
	rec := postWithPathValue(t, h.handleRotateAPIKey, http.MethodPost,
		"/api/tenants/ws-1/rotate-api-key", "ws-1", `{"reason":"x"}`)
	assert.Equal(t, http.StatusServiceUnavailable, rec.Code)
	assert.Contains(t, rec.Body.String(), "grace tracking not configured")
}

func TestVeridianHandleRotateAPIKey_GenericServiceError(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	svc := mocks.NewMockVeridianService(ctrl)
	svc.EXPECT().RotateAPIKey(gomock.Any(), gomock.Any()).
		Return(nil, errors.New("upstream barf"))

	h := newHandlerWithService(svc)
	rec := postWithPathValue(t, h.handleRotateAPIKey, http.MethodPost,
		"/api/tenants/ws-1/rotate-api-key", "ws-1", `{"reason":"x"}`)
	assert.Equal(t, http.StatusInternalServerError, rec.Code)
	// Le 500 ne doit pas leak l'erreur interne brute au client (axe 2).
	assert.NotContains(t, rec.Body.String(), "upstream barf", "le 500 ne doit pas leak l'erreur interne")
	assert.Contains(t, rec.Body.String(), "internal_error")
}

// === handleTransferOwner ===

func TestVeridianHandleTransferOwner_OK(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	transferredAt := time.Date(2026, 5, 21, 12, 0, 0, 0, time.UTC)
	svc := mocks.NewMockVeridianService(ctrl)
	svc.EXPECT().TransferOwner(gomock.Any(), domain.TransferOwnerInput{
		TenantID:       "ws-1",
		OwnerHubUserID: "6b3c1f4e-2a51-4c1e-9a77-0d9f2c5b1a01",
		Reason:         "client account migration",
	}).Return(&domain.TransferOwnerResponse{
		TenantID:      "ws-1",
		OldOwner:      "old@x.test",
		NewOwner:      "new@x.test",
		TransferredAt: transferredAt,
	}, nil)

	h := newHandlerWithService(svc)
	rec := postWithPathValue(t, h.handleTransferOwner, http.MethodPost,
		"/api/tenants/ws-1/transfer-owner", "ws-1",
		`{"owner_hub_user_id":"6b3c1f4e-2a51-4c1e-9a77-0d9f2c5b1a01","new_owner_email":"new@x.test","reason":"client account migration"}`)

	assert.Equal(t, http.StatusOK, rec.Code)
	var resp domain.TransferOwnerResponse
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp))
	assert.Equal(t, "ws-1", resp.TenantID)
	assert.Equal(t, "old@x.test", resp.OldOwner)
	assert.Equal(t, "new@x.test", resp.NewOwner)
}

func TestVeridianHandleTransferOwner_MissingPathID(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	svc := mocks.NewMockVeridianService(ctrl)
	h := newHandlerWithService(svc)

	rec := postWithPathValue(t, h.handleTransferOwner, http.MethodPost,
		"/api/tenants//transfer-owner", "", `{"owner_hub_user_id":"6b3c1f4e-2a51-4c1e-9a77-0d9f2c5b1a01","reason":"r"}`)
	assert.Equal(t, http.StatusBadRequest, rec.Code)
	assert.Contains(t, rec.Body.String(), "tenant id is required")
}

func TestVeridianHandleTransferOwner_MissingBody(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	svc := mocks.NewMockVeridianService(ctrl)
	h := newHandlerWithService(svc)

	rec := postWithPathValue(t, h.handleTransferOwner, http.MethodPost,
		"/api/tenants/ws-1/transfer-owner", "ws-1", "")
	assert.Equal(t, http.StatusBadRequest, rec.Code)
	assert.Contains(t, rec.Body.String(), "owner_hub_user_id")
}

func TestVeridianHandleTransferOwner_MissingOwnerHubUserID(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	svc := mocks.NewMockVeridianService(ctrl)
	h := newHandlerWithService(svc)

	rec := postWithPathValue(t, h.handleTransferOwner, http.MethodPost,
		"/api/tenants/ws-1/transfer-owner", "ws-1", `{"reason":"r"}`)
	assert.Equal(t, http.StatusBadRequest, rec.Code)
	assert.Contains(t, rec.Body.String(), "owner_hub_user_id")
}

func TestVeridianHandleTransferOwner_MalformedOwnerHubUserID(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	svc := mocks.NewMockVeridianService(ctrl)
	h := newHandlerWithService(svc)

	rec := postWithPathValue(t, h.handleTransferOwner, http.MethodPost,
		"/api/tenants/ws-1/transfer-owner", "ws-1", `{"owner_hub_user_id":"u-1","reason":"r"}`)
	assert.Equal(t, http.StatusBadRequest, rec.Code)
	assert.Contains(t, rec.Body.String(), ErrCodeInvalidPayload)
}

func TestVeridianHandleTransferOwner_UnknownOrNotMember(t *testing.T) {
	cases := []struct {
		name string
		err  error
		code int
		body string
	}{
		{"unknown", service.ErrUserNotInApp, http.StatusNotFound, ErrCodeUserNotInApp},
		{"not_member", service.ErrOwnerNotMember, http.StatusConflict, ErrCodeNotAMember},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ctrl := gomock.NewController(t)
			defer ctrl.Finish()
			svc := mocks.NewMockVeridianService(ctrl)
			svc.EXPECT().TransferOwner(gomock.Any(), gomock.Any()).Return(nil, tc.err)
			h := newHandlerWithService(svc)
			rec := postWithPathValue(t, h.handleTransferOwner, http.MethodPost,
				"/api/tenants/ws-1/transfer-owner", "ws-1", `{"owner_hub_user_id":"6b3c1f4e-2a51-4c1e-9a77-0d9f2c5b1a01","reason":"r"}`)
			assert.Equal(t, tc.code, rec.Code)
			assert.Contains(t, rec.Body.String(), tc.body)
		})
	}
}

func TestVeridianHandleTransferOwner_MissingReason(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	svc := mocks.NewMockVeridianService(ctrl)
	h := newHandlerWithService(svc)

	rec := postWithPathValue(t, h.handleTransferOwner, http.MethodPost,
		"/api/tenants/ws-1/transfer-owner", "ws-1", `{"owner_hub_user_id":"6b3c1f4e-2a51-4c1e-9a77-0d9f2c5b1a01"}`)
	assert.Equal(t, http.StatusBadRequest, rec.Code)
	assert.Contains(t, rec.Body.String(), "reason")
}

func TestVeridianHandleTransferOwner_InvalidJSON(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	svc := mocks.NewMockVeridianService(ctrl)
	h := newHandlerWithService(svc)

	rec := postWithPathValue(t, h.handleTransferOwner, http.MethodPost,
		"/api/tenants/ws-1/transfer-owner", "ws-1", `{not json`)
	assert.Equal(t, http.StatusBadRequest, rec.Code)
	assert.Contains(t, rec.Body.String(), "invalid JSON body")
}

func TestVeridianHandleTransferOwner_NotFound(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	svc := mocks.NewMockVeridianService(ctrl)
	svc.EXPECT().TransferOwner(gomock.Any(), gomock.Any()).Return(nil, sql.ErrNoRows)

	h := newHandlerWithService(svc)
	rec := postWithPathValue(t, h.handleTransferOwner, http.MethodPost,
		"/api/tenants/ws-x/transfer-owner", "ws-x",
		`{"owner_hub_user_id":"6b3c1f4e-2a51-4c1e-9a77-0d9f2c5b1a01","reason":"r"}`)
	assert.Equal(t, http.StatusNotFound, rec.Code)
	assert.Contains(t, rec.Body.String(), "tenant not found")
}

func TestVeridianHandleTransferOwner_GenericServiceError(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	svc := mocks.NewMockVeridianService(ctrl)
	svc.EXPECT().TransferOwner(gomock.Any(), gomock.Any()).
		Return(nil, errors.New("upstream barf"))

	h := newHandlerWithService(svc)
	rec := postWithPathValue(t, h.handleTransferOwner, http.MethodPost,
		"/api/tenants/ws-1/transfer-owner", "ws-1",
		`{"owner_hub_user_id":"6b3c1f4e-2a51-4c1e-9a77-0d9f2c5b1a01","reason":"r"}`)
	assert.Equal(t, http.StatusInternalServerError, rec.Code)
	// Le 500 ne doit pas leak l'erreur interne brute au client (axe 2).
	assert.NotContains(t, rec.Body.String(), "upstream barf", "le 500 ne doit pas leak l'erreur interne")
	assert.Contains(t, rec.Body.String(), "internal_error")
}
