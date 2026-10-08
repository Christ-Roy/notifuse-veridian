package http

import (
	"bytes"
	"encoding/json"
	"errors"
	nethttp "net/http"
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

func newProspectionStatsHandler(ctrl *gomock.Controller) (*VeridianProspectionStatsHandler, *mocks.MockVeridianProspectionStatsService) {
	svc := mocks.NewMockVeridianProspectionStatsService(ctrl)
	log := pkgmocks.NewMockLogger(ctrl)
	log.EXPECT().WithField(gomock.Any(), gomock.Any()).Return(log).AnyTimes()
	log.EXPECT().Error(gomock.Any()).AnyTimes()
	return NewVeridianProspectionStatsHandler(svc, func() ([]byte, error) { return []byte("test-secret"), nil }, log), svc
}

func TestVeridianProspectionStatsHandler(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	h, svc := newProspectionStatsHandler(ctrl)

	t.Run("POST : la fenetre suit la regle de replyStats (jour de fin inclus)", func(t *testing.T) {
		svc.EXPECT().GetProspectionStats(gomock.Any(), &domain.VeridianProspectionStatsRequest{
			WorkspaceID: "ws1",
			Since:       time.Date(2026, 9, 28, 0, 0, 0, 0, time.UTC),
			Until:       time.Date(2026, 10, 9, 0, 0, 0, 0, time.UTC),
		}).Return(&domain.VeridianProspectionStats{Sequences: []domain.VeridianSequenceStats{}, Segments: []domain.VeridianSegmentStats{}}, nil)
		body, _ := json.Marshal(map[string]string{"workspace_id": "ws1", "start": "2026-09-28", "end": "2026-10-08"})
		rec := httptest.NewRecorder()
		h.handle(rec, httptest.NewRequest(nethttp.MethodPost, "/api/veridian/prospection.stats", bytes.NewReader(body)))
		require.Equal(t, nethttp.StatusOK, rec.Code)
		assert.Contains(t, rec.Body.String(), `"sequences":[]`)
	})

	t.Run("GET sans fenetre : tout l'historique", func(t *testing.T) {
		svc.EXPECT().GetProspectionStats(gomock.Any(), &domain.VeridianProspectionStatsRequest{WorkspaceID: "ws1"}).Return(&domain.VeridianProspectionStats{}, nil)
		rec := httptest.NewRecorder()
		h.handle(rec, httptest.NewRequest(nethttp.MethodGet, "/api/veridian/prospection.stats?workspace_id=ws1", nil))
		require.Equal(t, nethttp.StatusOK, rec.Code)
	})

	t.Run("requetes invalides", func(t *testing.T) {
		for _, target := range []string{
			"/api/veridian/prospection.stats",
			"/api/veridian/prospection.stats?workspace_id=ws1&start=pas-une-date",
			"/api/veridian/prospection.stats?workspace_id=ws1&end=31-12-2026",
		} {
			rec := httptest.NewRecorder()
			h.handle(rec, httptest.NewRequest(nethttp.MethodGet, target, nil))
			assert.Equal(t, nethttp.StatusBadRequest, rec.Code, target)
		}
		rec := httptest.NewRecorder()
		h.handle(rec, httptest.NewRequest(nethttp.MethodPost, "/api/veridian/prospection.stats", bytes.NewReader([]byte("{pas json"))))
		assert.Equal(t, nethttp.StatusBadRequest, rec.Code)
	})

	t.Run("refus de permission : 403, pas 500", func(t *testing.T) {
		svc.EXPECT().GetProspectionStats(gomock.Any(), gomock.Any()).Return(nil, domain.NewPermissionError(domain.PermissionResourceContacts, domain.PermissionTypeRead, "no"))
		rec := httptest.NewRecorder()
		h.handle(rec, httptest.NewRequest(nethttp.MethodGet, "/api/veridian/prospection.stats?workspace_id=ws1", nil))
		assert.Equal(t, nethttp.StatusForbidden, rec.Code)
	})

	t.Run("erreur interne", func(t *testing.T) {
		svc.EXPECT().GetProspectionStats(gomock.Any(), gomock.Any()).Return(nil, errors.New("db down"))
		rec := httptest.NewRecorder()
		h.handle(rec, httptest.NewRequest(nethttp.MethodGet, "/api/veridian/prospection.stats?workspace_id=ws1", nil))
		assert.Equal(t, nethttp.StatusInternalServerError, rec.Code)
	})
}

func TestVeridianProspectionStatsHandler_RegistersBothMethods(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	h, _ := newProspectionStatsHandler(ctrl)
	mux := nethttp.NewServeMux()
	h.RegisterRoutes(mux)
	for _, method := range []string{nethttp.MethodGet, nethttp.MethodPost} {
		_, pattern := mux.Handler(httptest.NewRequest(method, "/api/veridian/prospection.stats", nil))
		assert.NotEmpty(t, pattern, method+" doit etre route (sinon le catchall SPA repond 200 HTML)")
	}
}
