package http

// Tests du handler POST /api/veridian/admin/cold-simulate (cold lifecycle E2E).
// Couvre le routage par mode, le garde-fou staging-only (503), la validation des
// entrées, et le câblage vers le VRAI code métier (reply processor + message
// history repo). Le comportement end-to-end réel est validé par les specs
// Playwright tests/e2e-veridian/specs/cold-lifecycle.spec.ts contre staging ;
// ici on isole la logique du handler avec des mocks gomock + un stub processor.

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/Notifuse/notifuse/internal/domain"
	"github.com/Notifuse/notifuse/internal/domain/mocks"
	"github.com/Notifuse/notifuse/pkg/logger"
	"github.com/golang/mock/gomock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// stubColdReplyProcessor stub le contrat VeridianColdReplyProcessor in-process
// (2 méthodes seulement, pas de mock gomock nécessaire).
type stubColdReplyProcessor struct {
	processedMsg  *domain.VeridianIMAPMessage
	processErr    error
	hasReplied    bool
	hasRepliedErr error
}

func (s *stubColdReplyProcessor) ProcessInboundMessage(_ context.Context, msg *domain.VeridianIMAPMessage) error {
	s.processedMsg = msg
	return s.processErr
}

func (s *stubColdReplyProcessor) HasReplied(_ context.Context, _ string, _ string) (bool, error) {
	return s.hasReplied, s.hasRepliedErr
}

// postColdSimulate POST direct sur le handler (pas de middleware HMAC : on isole).
func postColdSimulate(t *testing.T, h *VeridianHandler, body interface{}) *httptest.ResponseRecorder {
	t.Helper()
	raw, err := json.Marshal(body)
	require.NoError(t, err)
	req := httptest.NewRequest(http.MethodPost, "/api/veridian/admin/cold-simulate", bytes.NewReader(raw))
	rec := httptest.NewRecorder()
	h.handleColdSimulate(rec, req)
	return rec
}

func newColdSimulateHandler() *VeridianHandler {
	return &VeridianHandler{logger: logger.NewLogger()}
}

// 503 si SetColdSimulate jamais appelé (mode prod / self-hosted / boot partiel).
func TestHandleColdSimulate_NotWired503(t *testing.T) {
	h := newColdSimulateHandler()
	rec := postColdSimulate(t, h, veridianColdSimulateRequest{Mode: "seed_sent", WorkspaceID: "ws1"})
	assert.Equal(t, http.StatusServiceUnavailable, rec.Code)

	var body map[string]interface{}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
	assert.Equal(t, "paywall_unavailable", body["code"])
}

// 503 si environnement != staging (garde-fou prod : l'endpoint refuse même câblé).
func TestHandleColdSimulate_NonStagingEnv503(t *testing.T) {
	h := newColdSimulateHandler()
	h.SetColdSimulate(&stubColdReplyProcessor{}, nil, nil, "production")
	rec := postColdSimulate(t, h, veridianColdSimulateRequest{Mode: "seed_sent", WorkspaceID: "ws1"})
	assert.Equal(t, http.StatusServiceUnavailable, rec.Code)
}

// 400 si JSON invalide.
func TestHandleColdSimulate_InvalidJSON400(t *testing.T) {
	h := newColdSimulateHandler()
	h.SetColdSimulate(&stubColdReplyProcessor{}, nil, nil, "staging")
	req := httptest.NewRequest(http.MethodPost, "/api/veridian/admin/cold-simulate", bytes.NewReader([]byte("{not-json")))
	rec := httptest.NewRecorder()
	h.handleColdSimulate(rec, req)
	assert.Equal(t, http.StatusBadRequest, rec.Code)
}

// 400 si workspace_id manquant.
func TestHandleColdSimulate_MissingWorkspace400(t *testing.T) {
	h := newColdSimulateHandler()
	h.SetColdSimulate(&stubColdReplyProcessor{}, nil, nil, "staging")
	rec := postColdSimulate(t, h, veridianColdSimulateRequest{Mode: "seed_sent"})
	assert.Equal(t, http.StatusBadRequest, rec.Code)
}

// 400 si mode inconnu.
func TestHandleColdSimulate_UnknownMode400(t *testing.T) {
	h := newColdSimulateHandler()
	h.SetColdSimulate(&stubColdReplyProcessor{}, nil, nil, "staging")
	rec := postColdSimulate(t, h, veridianColdSimulateRequest{Mode: "wat", WorkspaceID: "ws1"})
	assert.Equal(t, http.StatusBadRequest, rec.Code)
}

// === mode daily_cap_decision : le prédicat exact du gate (count >= cap) ===

func TestHandleColdSimulate_DailyCapDecision_BelowCap(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	msgRepo := mocks.NewMockMessageHistoryRepository(ctrl)
	// 2 envois aujourd'hui, cap = 3 → pas bloqué.
	msgRepo.EXPECT().
		CountSentSinceForContact(gomock.Any(), "ws1", "p@corp.com", gomock.Any()).
		Return(2, nil)

	h := newColdSimulateHandler()
	h.SetColdSimulate(&stubColdReplyProcessor{}, msgRepo, nil, "staging")

	rec := postColdSimulate(t, h, veridianColdSimulateRequest{
		Mode: "daily_cap_decision", WorkspaceID: "ws1",
		ContactEmail: "p@corp.com", PerRecipientCap: 3,
	})
	require.Equal(t, http.StatusOK, rec.Code)

	var got veridianColdSimulateResponse
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &got))
	assert.Equal(t, 2, got.SentToday)
	assert.False(t, got.WouldBeCapped, "2 < cap 3 → pas bloqué")
}

func TestHandleColdSimulate_DailyCapDecision_AtCapBlocks(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	msgRepo := mocks.NewMockMessageHistoryRepository(ctrl)
	// 1 envoi aujourd'hui, cap = 1 → bloqué (count >= cap, prédicat strict).
	msgRepo.EXPECT().
		CountSentSinceForContact(gomock.Any(), "ws1", "p@corp.com", gomock.Any()).
		Return(1, nil)

	h := newColdSimulateHandler()
	h.SetColdSimulate(&stubColdReplyProcessor{}, msgRepo, nil, "staging")

	rec := postColdSimulate(t, h, veridianColdSimulateRequest{
		Mode: "daily_cap_decision", WorkspaceID: "ws1",
		ContactEmail: "p@corp.com", PerRecipientCap: 1,
	})
	require.Equal(t, http.StatusOK, rec.Code)

	var got veridianColdSimulateResponse
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &got))
	assert.Equal(t, 1, got.SentToday)
	assert.True(t, got.WouldBeCapped, "count 1 >= cap 1 → bloqué (anti-harcèlement)")
}

// La requête de comptage est bornée à minuit UTC (jour calendaire), identique au gate.
func TestHandleColdSimulate_DailyCapDecision_CountsSinceMidnightUTC(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	msgRepo := mocks.NewMockMessageHistoryRepository(ctrl)
	expectedSince := veridianColdSimulateStartOfDay(time.Now())
	msgRepo.EXPECT().
		CountSentSinceForContact(gomock.Any(), "ws1", "p@corp.com", gomock.AssignableToTypeOf(time.Time{})).
		DoAndReturn(func(_ context.Context, _ string, _ string, since time.Time) (int, error) {
			// La borne envoyée au repo doit être minuit UTC du jour (à la seconde près).
			assert.WithinDuration(t, expectedSince, since, time.Second)
			assert.Equal(t, time.UTC, since.Location())
			return 0, nil
		})

	h := newColdSimulateHandler()
	h.SetColdSimulate(&stubColdReplyProcessor{}, msgRepo, nil, "staging")

	rec := postColdSimulate(t, h, veridianColdSimulateRequest{
		Mode: "daily_cap_decision", WorkspaceID: "ws1",
		ContactEmail: "p@corp.com", PerRecipientCap: 5,
	})
	require.Equal(t, http.StatusOK, rec.Code)
}

func TestHandleColdSimulate_DailyCapDecision_MissingContact400(t *testing.T) {
	h := newColdSimulateHandler()
	h.SetColdSimulate(&stubColdReplyProcessor{}, mocks.NewMockMessageHistoryRepository(gomock.NewController(t)), nil, "staging")
	rec := postColdSimulate(t, h, veridianColdSimulateRequest{
		Mode: "daily_cap_decision", WorkspaceID: "ws1", PerRecipientCap: 1,
	})
	assert.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestHandleColdSimulate_DailyCapDecision_NonPositiveCap400(t *testing.T) {
	h := newColdSimulateHandler()
	h.SetColdSimulate(&stubColdReplyProcessor{}, mocks.NewMockMessageHistoryRepository(gomock.NewController(t)), nil, "staging")
	rec := postColdSimulate(t, h, veridianColdSimulateRequest{
		Mode: "daily_cap_decision", WorkspaceID: "ws1", ContactEmail: "p@corp.com", PerRecipientCap: 0,
	})
	assert.Equal(t, http.StatusBadRequest, rec.Code)
}

// === mode seed_sent : pose N entrées message_history via le VRAI repo Create ===

func TestHandleColdSimulate_SeedSent_CreatesNAndCounts(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	msgRepo := mocks.NewMockMessageHistoryRepository(ctrl)
	wsRepo := mocks.NewMockWorkspaceRepository(ctrl)

	wsRepo.EXPECT().GetByID(gomock.Any(), "ws1").
		Return(&domain.Workspace{ID: "ws1", Settings: domain.WorkspaceSettings{SecretKey: "sk-test"}}, nil)
	// 3 entrées "sent" créées vers le contact (sent_at posé, failed_at nil).
	msgRepo.EXPECT().
		Create(gomock.Any(), "ws1", "sk-test", gomock.Any()).
		DoAndReturn(func(_ context.Context, _ string, _ string, m *domain.MessageHistory) error {
			assert.Equal(t, "p@corp.com", m.ContactEmail)
			assert.Nil(t, m.FailedAt, "entrée 'sent' (pas failed)")
			assert.False(t, m.SentAt.IsZero(), "sent_at posé (lu par le cap)")
			return nil
		}).Times(3)
	msgRepo.EXPECT().
		CountSentSinceForContact(gomock.Any(), "ws1", "p@corp.com", gomock.Any()).
		Return(3, nil)

	h := newColdSimulateHandler()
	h.SetColdSimulate(&stubColdReplyProcessor{}, msgRepo, wsRepo, "staging")

	rec := postColdSimulate(t, h, veridianColdSimulateRequest{
		Mode: "seed_sent", WorkspaceID: "ws1", ContactEmail: "p@corp.com", Count: 3,
	})
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())

	var got veridianColdSimulateResponse
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &got))
	assert.Equal(t, 3, got.Seeded)
	assert.Equal(t, 3, got.SentToday)
}

func TestHandleColdSimulate_SeedSent_CountBounds400(t *testing.T) {
	h := newColdSimulateHandler()
	h.SetColdSimulate(&stubColdReplyProcessor{},
		mocks.NewMockMessageHistoryRepository(gomock.NewController(t)),
		mocks.NewMockWorkspaceRepository(gomock.NewController(t)), "staging")
	for _, count := range []int{0, 51} {
		rec := postColdSimulate(t, h, veridianColdSimulateRequest{
			Mode: "seed_sent", WorkspaceID: "ws1", ContactEmail: "p@corp.com", Count: count,
		})
		assert.Equal(t, http.StatusBadRequest, rec.Code, "count=%d hors borne 1..50", count)
	}
}

// === mode inbound_reply : frappe le VRAI ProcessInboundMessage + HasReplied ===

func TestHandleColdSimulate_InboundReply_MarksReplied(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	msgRepo := mocks.NewMockMessageHistoryRepository(ctrl)
	wsRepo := mocks.NewMockWorkspaceRepository(ctrl)
	proc := &stubColdReplyProcessor{hasReplied: true}

	wsRepo.EXPECT().GetByID(gomock.Any(), "ws1").
		Return(&domain.Workspace{ID: "ws1", Settings: domain.WorkspaceSettings{SecretKey: "sk-test"}}, nil)
	// Seed de l'envoi initial cité par la réponse (le match fort le retrouvera).
	msgRepo.EXPECT().Create(gomock.Any(), "ws1", "sk-test", gomock.Any()).Return(nil)

	h := newColdSimulateHandler()
	h.SetColdSimulate(proc, msgRepo, wsRepo, "staging")

	rec := postColdSimulate(t, h, veridianColdSimulateRequest{
		Mode: "inbound_reply", WorkspaceID: "ws1", From: "Prospect@Corp.com",
		Subject: "Re: votre offre",
	})
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())

	var got veridianColdSimulateResponse
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &got))
	assert.True(t, got.HasReplied, "le contact doit être marqué replied")
	assert.True(t, got.IsReply)
	assert.NotEmpty(t, got.SeededMsgID)

	// Le message passé au VRAI service doit être normalisé + cohérent.
	require.NotNil(t, proc.processedMsg)
	assert.Equal(t, "prospect@corp.com", proc.processedMsg.From, "From normalisé lowercase")
	assert.Equal(t, "ws1", proc.processedMsg.WorkspaceID)
	// In-Reply-To par défaut cite l'id de l'envoi seedé (match fort).
	assert.Contains(t, proc.processedMsg.InReplyTo, got.SeededMsgID)
}

func TestHandleColdSimulate_InboundReply_MissingFrom400(t *testing.T) {
	h := newColdSimulateHandler()
	h.SetColdSimulate(&stubColdReplyProcessor{},
		mocks.NewMockMessageHistoryRepository(gomock.NewController(t)),
		mocks.NewMockWorkspaceRepository(gomock.NewController(t)), "staging")
	rec := postColdSimulate(t, h, veridianColdSimulateRequest{
		Mode: "inbound_reply", WorkspaceID: "ws1",
	})
	assert.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestHandleColdSimulate_InboundReply_ProcessErrorPropagates500(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	msgRepo := mocks.NewMockMessageHistoryRepository(ctrl)
	wsRepo := mocks.NewMockWorkspaceRepository(ctrl)
	proc := &stubColdReplyProcessor{processErr: errors.New("db boom")}

	wsRepo.EXPECT().GetByID(gomock.Any(), "ws1").
		Return(&domain.Workspace{ID: "ws1", Settings: domain.WorkspaceSettings{SecretKey: "sk"}}, nil)
	msgRepo.EXPECT().Create(gomock.Any(), "ws1", "sk", gomock.Any()).Return(nil)

	h := newColdSimulateHandler()
	h.SetColdSimulate(proc, msgRepo, wsRepo, "staging")

	rec := postColdSimulate(t, h, veridianColdSimulateRequest{
		Mode: "inbound_reply", WorkspaceID: "ws1", From: "p@corp.com",
	})
	assert.Equal(t, http.StatusInternalServerError, rec.Code)
}

func TestHandleColdSimulate_StartOfDayIsMidnightUTC(t *testing.T) {
	// 2026-06-15T14:32:10Z → 2026-06-15T00:00:00Z.
	in := time.Date(2026, 6, 15, 14, 32, 10, 999, time.FixedZone("CEST", 2*3600))
	got := veridianColdSimulateStartOfDay(in)
	want := time.Date(2026, 6, 15, 12, 0, 0, 0, time.UTC) // 14:32 CEST = 12:32 UTC → minuit UTC du 15
	// Le jour UTC de 14:32 CEST (=12:32 UTC) est bien le 15.
	assert.Equal(t, want.Truncate(24*time.Hour), got)
	assert.Equal(t, time.UTC, got.Location())
	assert.Equal(t, 0, got.Hour())
}
