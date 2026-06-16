package http

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/golang/mock/gomock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/Notifuse/notifuse/internal/domain"
	"github.com/Notifuse/notifuse/internal/domain/mocks"
	pkgmocks "github.com/Notifuse/notifuse/pkg/mocks"
)

func newReplyStatsHandler(ctrl *gomock.Controller) (*VeridianReplyStatsHandler, *mocks.MockVeridianReplyStatsService) {
	svc := mocks.NewMockVeridianReplyStatsService(ctrl)
	log := pkgmocks.NewMockLogger(ctrl)
	log.EXPECT().WithField(gomock.Any(), gomock.Any()).Return(log).AnyTimes()
	log.EXPECT().Error(gomock.Any()).AnyTimes()
	h := NewVeridianReplyStatsHandler(svc, func() ([]byte, error) { return []byte("test-secret"), nil }, log)
	return h, svc
}

func TestVeridianReplyStatsHandler_POST(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	h, svc := newReplyStatsHandler(ctrl)

	// start=2026-06-01 -> Since = 2026-06-01T00:00Z ; end=2026-06-15 -> Until =
	// 2026-06-16T00:00Z (jour de fin INCLUS = borne exclusive au lendemain).
	wantSince := time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)
	wantUntil := time.Date(2026, 6, 16, 0, 0, 0, 0, time.UTC)
	svc.EXPECT().GetReplyStats(gomock.Any(), &domain.VeridianReplyStatsRequest{
		WorkspaceID: "ws123",
		Since:       wantSince,
		Until:       wantUntil,
	}).Return(&domain.VeridianReplyStats{Replied: 9}, nil)

	body, _ := json.Marshal(map[string]string{"workspace_id": "ws123", "start": "2026-06-01", "end": "2026-06-15"})
	r := httptest.NewRequest(http.MethodPost, "/api/veridian/messages.replyStats", bytes.NewReader(body))
	rec := httptest.NewRecorder()

	h.handleReplyStats(rec, r)

	require.Equal(t, http.StatusOK, rec.Code)
	var resp domain.VeridianReplyStats
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp))
	assert.Equal(t, 9, resp.Replied)
}

func TestVeridianReplyStatsHandler_GET(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	h, svc := newReplyStatsHandler(ctrl)

	svc.EXPECT().GetReplyStats(gomock.Any(), &domain.VeridianReplyStatsRequest{
		WorkspaceID: "ws123",
	}).Return(&domain.VeridianReplyStats{Replied: 0}, nil)

	r := httptest.NewRequest(http.MethodGet, "/api/veridian/messages.replyStats?workspace_id=ws123", nil)
	rec := httptest.NewRecorder()

	h.handleReplyStats(rec, r)

	require.Equal(t, http.StatusOK, rec.Code)
}

func TestVeridianReplyStatsHandler_GETWithWindow(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	h, svc := newReplyStatsHandler(ctrl)

	wantSince := time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC)
	wantUntil := time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC) // end 2026-05-31 + 1j
	svc.EXPECT().GetReplyStats(gomock.Any(), &domain.VeridianReplyStatsRequest{
		WorkspaceID: "ws123",
		Since:       wantSince,
		Until:       wantUntil,
	}).Return(&domain.VeridianReplyStats{Replied: 4}, nil)

	r := httptest.NewRequest(http.MethodGet, "/api/veridian/messages.replyStats?workspace_id=ws123&start=2026-05-01&end=2026-05-31", nil)
	rec := httptest.NewRecorder()
	h.handleReplyStats(rec, r)
	require.Equal(t, http.StatusOK, rec.Code)
}

func TestVeridianReplyStatsHandler_QueryParamsOnPost(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	h, svc := newReplyStatsHandler(ctrl)
	svc.EXPECT().GetReplyStats(gomock.Any(), &domain.VeridianReplyStatsRequest{
		WorkspaceID: "wsQuery",
	}).Return(&domain.VeridianReplyStats{Replied: 1}, nil)

	r := httptest.NewRequest(http.MethodPost, "/api/veridian/messages.replyStats?workspace_id=wsQuery", nil)
	rec := httptest.NewRecorder()
	h.handleReplyStats(rec, r)
	require.Equal(t, http.StatusOK, rec.Code)
}

func TestVeridianReplyStatsHandler_MissingWorkspaceID(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	h, _ := newReplyStatsHandler(ctrl)

	r := httptest.NewRequest(http.MethodGet, "/api/veridian/messages.replyStats", nil)
	rec := httptest.NewRecorder()
	h.handleReplyStats(rec, r)

	require.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestVeridianReplyStatsHandler_InvalidStartDate(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	h, _ := newReplyStatsHandler(ctrl)

	r := httptest.NewRequest(http.MethodGet, "/api/veridian/messages.replyStats?workspace_id=ws123&start=not-a-date", nil)
	rec := httptest.NewRecorder()
	h.handleReplyStats(rec, r)

	require.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestVeridianReplyStatsHandler_InvalidJSON(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	h, _ := newReplyStatsHandler(ctrl)

	r := httptest.NewRequest(http.MethodPost, "/api/veridian/messages.replyStats", bytes.NewReader([]byte("{bad json")))
	rec := httptest.NewRecorder()
	h.handleReplyStats(rec, r)

	require.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestVeridianReplyStatsHandler_PermissionError(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	h, svc := newReplyStatsHandler(ctrl)
	svc.EXPECT().GetReplyStats(gomock.Any(), gomock.Any()).
		Return(nil, domain.NewPermissionError(domain.PermissionResourceContacts, domain.PermissionTypeRead, "no read"))

	r := httptest.NewRequest(http.MethodGet, "/api/veridian/messages.replyStats?workspace_id=ws123", nil)
	rec := httptest.NewRecorder()
	h.handleReplyStats(rec, r)

	require.Equal(t, http.StatusForbidden, rec.Code)
}

func TestVeridianReplyStatsHandler_AuthFailure(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	h, svc := newReplyStatsHandler(ctrl)
	svc.EXPECT().GetReplyStats(gomock.Any(), gomock.Any()).
		Return(nil, errors.New("failed to authenticate user: not a member"))

	r := httptest.NewRequest(http.MethodGet, "/api/veridian/messages.replyStats?workspace_id=ws123", nil)
	rec := httptest.NewRecorder()
	h.handleReplyStats(rec, r)

	require.Equal(t, http.StatusUnauthorized, rec.Code)
}

func TestVeridianReplyStatsHandler_InternalError(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	h, svc := newReplyStatsHandler(ctrl)
	svc.EXPECT().GetReplyStats(gomock.Any(), gomock.Any()).
		Return(nil, errors.New("db down"))

	r := httptest.NewRequest(http.MethodGet, "/api/veridian/messages.replyStats?workspace_id=ws123", nil)
	rec := httptest.NewRecorder()
	h.handleReplyStats(rec, r)

	require.Equal(t, http.StatusInternalServerError, rec.Code)
}

func TestVeridianReplyStatsHandler_RegisterRoutes(t *testing.T) {
	// Vérifie que POST ET GET sont routés explicitement (piège catchall
	// root_handler.go). Sans Authorization header, RequireAuth renvoie 401 — donc
	// le service n'est jamais appelé. 401 (et NON 404/405) prouve que les deux
	// méthodes atteignent le middleware via le mux (aucune ne tombe dans le
	// catchall SPA).
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	h, _ := newReplyStatsHandler(ctrl)

	mux := http.NewServeMux()
	h.RegisterRoutes(mux)

	for _, method := range []string{http.MethodGet, http.MethodPost} {
		r := httptest.NewRequest(method, "/api/veridian/messages.replyStats?workspace_id=ws123", nil)
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, r)
		assert.Equal(t, http.StatusUnauthorized, rec.Code, "method %s should reach RequireAuth (401), not the catchall", method)
		assert.NotEqual(t, http.StatusNotFound, rec.Code, "method %s not routed (catchall trap)", method)
		assert.NotEqual(t, http.StatusMethodNotAllowed, rec.Code, "method %s rejected by mux", method)
	}
}

func TestVeridianParseStatsDate(t *testing.T) {
	t.Run("empty -> zero time, no bound", func(t *testing.T) {
		got, err := veridianParseStatsDate("", false)
		require.NoError(t, err)
		assert.True(t, got.IsZero())
	})

	t.Run("bare date start -> midnight UTC", func(t *testing.T) {
		got, err := veridianParseStatsDate("2026-06-01", false)
		require.NoError(t, err)
		assert.Equal(t, time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC), got)
	})

	t.Run("bare date end -> midnight UTC + 1 day (end inclusive)", func(t *testing.T) {
		got, err := veridianParseStatsDate("2026-06-15", true)
		require.NoError(t, err)
		assert.Equal(t, time.Date(2026, 6, 16, 0, 0, 0, 0, time.UTC), got)
	})

	t.Run("RFC3339 instant taken as-is (no +1 day even for end)", func(t *testing.T) {
		got, err := veridianParseStatsDate("2026-06-15T12:30:00Z", true)
		require.NoError(t, err)
		assert.Equal(t, time.Date(2026, 6, 15, 12, 30, 0, 0, time.UTC), got)
	})

	t.Run("garbage -> error", func(t *testing.T) {
		_, err := veridianParseStatsDate("not-a-date", false)
		assert.Error(t, err)
	})
}
