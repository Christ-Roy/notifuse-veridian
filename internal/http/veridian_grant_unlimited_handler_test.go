package http

// === Veridian patch === Tests colocalises pour
// veridian_grant_unlimited_handler.go.

import (
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"testing"

	"github.com/Notifuse/notifuse/internal/domain"
	"github.com/Notifuse/notifuse/internal/domain/mocks"
	"github.com/Notifuse/notifuse/internal/service"
	"github.com/golang/mock/gomock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestVeridianHandleGrantUnlimited_OK(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	svc := mocks.NewMockVeridianService(ctrl)
	svc.EXPECT().GrantUnlimited(gomock.Any(), domain.GrantUnlimitedInput{
		TenantID:   "robertbrunon",
		Reason:     "internal_team_member",
		PlanSource: "",
	}).Return(&domain.GrantUnlimitedResponse{
		TenantID:     "robertbrunon",
		Plan:         "enterprise",
		PreviousPlan: "free",
		PlanSource:   domain.PlanSourceLifetimePartner,
		Quota:        -1,
		Reason:       "internal_team_member",
	}, nil)
	h := newHandlerWithService(svc)

	rec := postJSON(t, h.handleGrantUnlimited, "/api/veridian/admin/grant-unlimited",
		`{"tenant_id":"robertbrunon","reason":"internal_team_member"}`)
	require.Equal(t, http.StatusOK, rec.Code)

	var resp domain.GrantUnlimitedResponse
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp))
	assert.Equal(t, "enterprise", resp.Plan)
	assert.Equal(t, "free", resp.PreviousPlan)
	assert.Equal(t, int64(-1), resp.Quota)
	assert.Equal(t, domain.PlanSourceLifetimePartner, resp.PlanSource)
}

func TestVeridianHandleGrantUnlimited_WithExplicitPlanSource(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	svc := mocks.NewMockVeridianService(ctrl)
	svc.EXPECT().GrantUnlimited(gomock.Any(), domain.GrantUnlimitedInput{
		TenantID:   "client42",
		Reason:     "lifetime_offer_2026",
		PlanSource: domain.PlanSourceLifetimeSiteVitrine,
	}).Return(&domain.GrantUnlimitedResponse{
		TenantID:     "client42",
		Plan:         "enterprise",
		PreviousPlan: "pro",
		PlanSource:   domain.PlanSourceLifetimeSiteVitrine,
		Quota:        -1,
		Reason:       "lifetime_offer_2026",
	}, nil)
	h := newHandlerWithService(svc)

	rec := postJSON(t, h.handleGrantUnlimited, "/api/veridian/admin/grant-unlimited",
		`{"tenant_id":"client42","reason":"lifetime_offer_2026","plan_source":"lifetime_site_vitrine"}`)
	require.Equal(t, http.StatusOK, rec.Code)

	var resp domain.GrantUnlimitedResponse
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp))
	assert.Equal(t, domain.PlanSourceLifetimeSiteVitrine, resp.PlanSource)
}

func TestVeridianHandleGrantUnlimited_MissingFields(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	svc := mocks.NewMockVeridianService(ctrl)
	h := newHandlerWithService(svc)

	// tenant_id manquant
	rec := postJSON(t, h.handleGrantUnlimited, "/api/veridian/admin/grant-unlimited",
		`{"reason":"oups"}`)
	assert.Equal(t, http.StatusBadRequest, rec.Code)
	assert.Contains(t, rec.Body.String(), "tenant_id and reason are required")

	// reason manquant
	rec = postJSON(t, h.handleGrantUnlimited, "/api/veridian/admin/grant-unlimited",
		`{"tenant_id":"ws-1"}`)
	assert.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestVeridianHandleGrantUnlimited_InvalidJSON(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	svc := mocks.NewMockVeridianService(ctrl)
	h := newHandlerWithService(svc)

	rec := postJSON(t, h.handleGrantUnlimited, "/api/veridian/admin/grant-unlimited", `not-json`)
	assert.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestVeridianHandleGrantUnlimited_InvalidPlanSource(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	svc := mocks.NewMockVeridianService(ctrl)
	svc.EXPECT().GrantUnlimited(gomock.Any(), gomock.Any()).
		Return(nil, service.ErrInvalidPlanSourceForGrant)
	h := newHandlerWithService(svc)

	rec := postJSON(t, h.handleGrantUnlimited, "/api/veridian/admin/grant-unlimited",
		`{"tenant_id":"ws-1","reason":"test","plan_source":"stripe"}`)
	assert.Equal(t, http.StatusBadRequest, rec.Code)
	assert.Contains(t, rec.Body.String(), "invalid plan_source")
}

func TestVeridianHandleGrantUnlimited_TenantNotFound(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	svc := mocks.NewMockVeridianService(ctrl)
	svc.EXPECT().GrantUnlimited(gomock.Any(), gomock.Any()).
		Return(nil, sql.ErrNoRows)
	h := newHandlerWithService(svc)

	rec := postJSON(t, h.handleGrantUnlimited, "/api/veridian/admin/grant-unlimited",
		`{"tenant_id":"ghost","reason":"test"}`)
	assert.Equal(t, http.StatusNotFound, rec.Code)
	assert.Contains(t, rec.Body.String(), "tenant not found")
}

func TestVeridianHandleGrantUnlimited_InternalError(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	svc := mocks.NewMockVeridianService(ctrl)
	svc.EXPECT().GrantUnlimited(gomock.Any(), gomock.Any()).
		Return(nil, errors.New("db connection lost"))
	h := newHandlerWithService(svc)

	rec := postJSON(t, h.handleGrantUnlimited, "/api/veridian/admin/grant-unlimited",
		`{"tenant_id":"ws-1","reason":"test"}`)
	assert.Equal(t, http.StatusInternalServerError, rec.Code)
}

func TestVeridianHandleGrantUnlimited_Idempotent(t *testing.T) {
	// Appel deux fois successifs sur le meme tenant : meme reponse, pas
	// d'erreur. Le service est cense gerer l'idempotence (UPDATE plan a la
	// meme valeur = no-op + event).
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	svc := mocks.NewMockVeridianService(ctrl)
	expected := &domain.GrantUnlimitedResponse{
		TenantID:     "ws-1",
		Plan:         "enterprise",
		PreviousPlan: "enterprise", // deja enterprise au second call
		PlanSource:   domain.PlanSourceLifetimePartner,
		Quota:        -1,
		Reason:       "test",
	}
	svc.EXPECT().GrantUnlimited(gomock.Any(), gomock.Any()).Return(expected, nil).Times(2)
	h := newHandlerWithService(svc)

	for i := 0; i < 2; i++ {
		rec := postJSON(t, h.handleGrantUnlimited, "/api/veridian/admin/grant-unlimited",
			`{"tenant_id":"ws-1","reason":"test"}`)
		assert.Equal(t, http.StatusOK, rec.Code, "call %d failed", i)
	}
}
