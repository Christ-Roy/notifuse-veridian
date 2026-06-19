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

	// PIÈGE omitempty : la clé would_be_capped DOIT être physiquement présente
	// dans le JSON même quand elle vaut false (sinon le client cold-lifecycle.spec
	// lit `undefined` au lieu de `false` quand l'envoi est autorisé). On vérifie
	// le JSON BRUT, pas la struct désérialisée (qui masquerait l'absence de clé).
	assert.Contains(t, rec.Body.String(), `"would_be_capped":false`,
		"would_be_capped=false doit rester présent dans le JSON (pas d'omitempty)")
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

// === mode seed_sent + sender_email : pose veridian_sender_email (cap per-sender) ===

func TestHandleColdSimulate_SeedSent_WithSenderEmail(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	msgRepo := mocks.NewMockMessageHistoryRepository(ctrl)
	wsRepo := mocks.NewMockWorkspaceRepository(ctrl)

	wsRepo.EXPECT().GetByID(gomock.Any(), "ws1").
		Return(&domain.Workspace{ID: "ws1", Settings: domain.WorkspaceSettings{SecretKey: "sk"}}, nil)
	// Le sender doit être posé (lowercased) sur l'entrée → le cap per-sender le lit.
	msgRepo.EXPECT().Create(gomock.Any(), "ws1", "sk", gomock.Any()).
		DoAndReturn(func(_ context.Context, _ string, _ string, m *domain.MessageHistory) error {
			assert.Equal(t, "bot1@agences.fr", m.VeridianSenderEmail, "sender posé lowercase pour le cap émetteur")
			return nil
		}).Times(2)
	msgRepo.EXPECT().CountSentSinceForContact(gomock.Any(), "ws1", "p@corp.com", gomock.Any()).Return(2, nil)

	h := newColdSimulateHandler()
	h.SetColdSimulate(&stubColdReplyProcessor{}, msgRepo, wsRepo, "staging")

	rec := postColdSimulate(t, h, veridianColdSimulateRequest{
		Mode: "seed_sent", WorkspaceID: "ws1", ContactEmail: "p@corp.com", Count: 2,
		SenderEmail: "Bot1@Agences.fr",
	})
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	var got veridianColdSimulateResponse
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &got))
	assert.Equal(t, 2, got.Seeded)
}

// === mode class_cap_decision : prédicat exact du cap CLASSE (CountSentSinceForDomains >= cap) ===

func TestHandleColdSimulate_ClassCapDecision_AtCapBlocks(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	msgRepo := mocks.NewMockMessageHistoryRepository(ctrl)
	// google : domains non vides (gmail.com…), 1 envoi aujourd'hui, cap 1 → bloqué.
	msgRepo.EXPECT().
		CountSentSinceForDomains(gomock.Any(), "ws1", gomock.Any(), gomock.Any(), gomock.Any()).
		Return(1, nil)

	h := newColdSimulateHandler()
	h.SetColdSimulate(&stubColdReplyProcessor{}, msgRepo, nil, "staging")
	rec := postColdSimulate(t, h, veridianColdSimulateRequest{
		Mode: "class_cap_decision", WorkspaceID: "ws1", ProviderClass: "google", ClassCap: 1,
	})
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	var got veridianColdSimulateResponse
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &got))
	assert.Equal(t, 1, got.SentToday)
	assert.True(t, got.WouldBeCapped, "count 1 >= cap 1 → bloqué (réputation classe)")
}

func TestHandleColdSimulate_ClassCapDecision_BelowCap(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	msgRepo := mocks.NewMockMessageHistoryRepository(ctrl)
	msgRepo.EXPECT().
		CountSentSinceForDomains(gomock.Any(), "ws1", gomock.Any(), gomock.Any(), gomock.Any()).
		Return(0, nil)

	h := newColdSimulateHandler()
	h.SetColdSimulate(&stubColdReplyProcessor{}, msgRepo, nil, "staging")
	rec := postColdSimulate(t, h, veridianColdSimulateRequest{
		Mode: "class_cap_decision", WorkspaceID: "ws1", ProviderClass: "google", ClassCap: 5,
	})
	require.Equal(t, http.StatusOK, rec.Code)
	var got veridianColdSimulateResponse
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &got))
	assert.False(t, got.WouldBeCapped, "0 < cap 5 → autorisé")
	assert.Contains(t, rec.Body.String(), `"would_be_capped":false`, "bool présent (pas d'omitempty)")
}

func TestHandleColdSimulate_ClassCapDecision_Validation400(t *testing.T) {
	h := newColdSimulateHandler()
	h.SetColdSimulate(&stubColdReplyProcessor{}, mocks.NewMockMessageHistoryRepository(gomock.NewController(t)), nil, "staging")
	for _, req := range []veridianColdSimulateRequest{
		{Mode: "class_cap_decision", WorkspaceID: "ws1", ClassCap: 1},                       // classe manquante
		{Mode: "class_cap_decision", WorkspaceID: "ws1", ProviderClass: "google", ClassCap: 0}, // cap non positif
	} {
		rec := postColdSimulate(t, h, req)
		assert.Equal(t, http.StatusBadRequest, rec.Code)
	}
}

// Sans sender_domain : le chemin reste le COUNT workspace-global (legacy) →
// CountSentSinceForDomains, et per_infra = false (présent dans le JSON).
func TestHandleColdSimulate_ClassCapDecision_NoSenderDomainIsGlobal(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	msgRepo := mocks.NewMockMessageHistoryRepository(ctrl)
	// Chemin global EXACT : aucune attente sur CountSentSinceForDomainsAndSenderDomain.
	msgRepo.EXPECT().
		CountSentSinceForDomains(gomock.Any(), "ws1", gomock.Any(), gomock.Any(), gomock.Any()).
		Return(0, nil)

	h := newColdSimulateHandler()
	h.SetColdSimulate(&stubColdReplyProcessor{}, msgRepo, nil, "staging")
	rec := postColdSimulate(t, h, veridianColdSimulateRequest{
		Mode: "class_cap_decision", WorkspaceID: "ws1", ProviderClass: "google", ClassCap: 1,
	})
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	var got veridianColdSimulateResponse
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &got))
	assert.False(t, got.PerInfra, "sans sender_domain → COUNT workspace-global")
	assert.Empty(t, got.SenderDomain)
	assert.Contains(t, rec.Body.String(), `"per_infra":false`, "per_infra=false présent dans le JSON")
}

// AVEC sender_domain : le COUNT est keyé PAR INFRA ÉMETTRICE →
// CountSentSinceForDomainsAndSenderDomain reçoit le domaine émetteur, le COUNT
// global N'est PAS appelé. C'est le prédicat exact de veridianCountClassForInfra.
func TestHandleColdSimulate_ClassCapDecision_PerInfraAtCapBlocks(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	msgRepo := mocks.NewMockMessageHistoryRepository(ctrl)
	// infra-a.fr a déjà 1 envoi google aujourd'hui, cap 1 → bloqué pour CETTE infra.
	msgRepo.EXPECT().
		CountSentSinceForDomainsAndSenderDomain(gomock.Any(), "ws1", gomock.Any(), gomock.Any(), "infra-a.fr", gomock.Any()).
		Return(1, nil)

	h := newColdSimulateHandler()
	h.SetColdSimulate(&stubColdReplyProcessor{}, msgRepo, nil, "staging")
	rec := postColdSimulate(t, h, veridianColdSimulateRequest{
		Mode: "class_cap_decision", WorkspaceID: "ws1", ProviderClass: "google", ClassCap: 1,
		SenderDomain: "infra-a.fr",
	})
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	var got veridianColdSimulateResponse
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &got))
	assert.Equal(t, 1, got.SentToday)
	assert.True(t, got.WouldBeCapped, "count 1 >= cap 1 → bloqué pour infra-a.fr")
	assert.True(t, got.PerInfra, "sender_domain présent → COUNT par infra")
	assert.Equal(t, "infra-a.fr", got.SenderDomain)
}

// L'ISOLATION par infra : une SECONDE infra (infra-b.fr) frappant la même classe
// a son PROPRE compteur (0) → PAS bloquée, alors que infra-a.fr l'était. C'est la
// preuve unitaire que le compteur est séparé par domaine émetteur (le scénario
// E2E 2-infras le confirme contre la vraie DB).
func TestHandleColdSimulate_ClassCapDecision_PerInfraIsolatedBelowCap(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	msgRepo := mocks.NewMockMessageHistoryRepository(ctrl)
	msgRepo.EXPECT().
		CountSentSinceForDomainsAndSenderDomain(gomock.Any(), "ws1", gomock.Any(), gomock.Any(), "infra-b.fr", gomock.Any()).
		Return(0, nil)

	h := newColdSimulateHandler()
	h.SetColdSimulate(&stubColdReplyProcessor{}, msgRepo, nil, "staging")
	rec := postColdSimulate(t, h, veridianColdSimulateRequest{
		Mode: "class_cap_decision", WorkspaceID: "ws1", ProviderClass: "google", ClassCap: 1,
		SenderDomain: "infra-b.fr",
	})
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	var got veridianColdSimulateResponse
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &got))
	assert.Equal(t, 0, got.SentToday)
	assert.False(t, got.WouldBeCapped, "infra-b.fr a son propre compteur (0) → pas bloquée")
	assert.True(t, got.PerInfra)
	assert.Equal(t, "infra-b.fr", got.SenderDomain)
}

// sender_domain accepte une ADRESSE complète : on extrait le domaine (= ce que le
// gate fait via veridianEmailDomain sur FromAddress). Vérifie aussi la normalisation
// (lowercase / trim / display name).
func TestHandleColdSimulate_ClassCapDecision_SenderDomainFromFullAddress(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	msgRepo := mocks.NewMockMessageHistoryRepository(ctrl)
	// "Bot1@Infra-A.FR" → domaine "infra-a.fr" passé au repo.
	msgRepo.EXPECT().
		CountSentSinceForDomainsAndSenderDomain(gomock.Any(), "ws1", gomock.Any(), gomock.Any(), "infra-a.fr", gomock.Any()).
		Return(0, nil)

	h := newColdSimulateHandler()
	h.SetColdSimulate(&stubColdReplyProcessor{}, msgRepo, nil, "staging")
	rec := postColdSimulate(t, h, veridianColdSimulateRequest{
		Mode: "class_cap_decision", WorkspaceID: "ws1", ProviderClass: "google", ClassCap: 1,
		SenderDomain: "Bot1@Infra-A.FR",
	})
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	var got veridianColdSimulateResponse
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &got))
	assert.Equal(t, "infra-a.fr", got.SenderDomain, "domaine extrait + normalisé")
}

// veridianColdSimulateSenderDomain : normalisation alignée sur queue.veridianEmailDomain.
func TestVeridianColdSimulateSenderDomain(t *testing.T) {
	cases := []struct{ in, want string }{
		{"infra-a.fr", "infra-a.fr"},
		{"  Infra-A.FR  ", "infra-a.fr"},
		{"bot@infra-a.fr", "infra-a.fr"},
		{"Bot1@Infra-A.FR", "infra-a.fr"},
		{"infra-a.fr.", "infra-a.fr"}, // point FQDN final retiré
		{"", ""},
		{"   ", ""},
		{"bot@", ""}, // adresse sans domaine → rien d'exploitable
	}
	for _, c := range cases {
		assert.Equal(t, c.want, veridianColdSimulateSenderDomain(c.in), "in=%q", c.in)
	}
}

// === mode per_sender_cap_decision : prédicat exact du cap ÉMETTEUR (CountSentSinceForSender >= cap) ===

func TestHandleColdSimulate_PerSenderCapDecision_AtCapBlocks(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	msgRepo := mocks.NewMockMessageHistoryRepository(ctrl)
	// La requête doit cibler le sender NORMALISÉ (lowercase) — le worker stocke lowercase.
	msgRepo.EXPECT().
		CountSentSinceForSender(gomock.Any(), "ws1", "bot1@agences.fr", gomock.Any()).
		Return(20, nil)

	h := newColdSimulateHandler()
	h.SetColdSimulate(&stubColdReplyProcessor{}, msgRepo, nil, "staging")
	rec := postColdSimulate(t, h, veridianColdSimulateRequest{
		Mode: "per_sender_cap_decision", WorkspaceID: "ws1", SenderEmail: "Bot1@Agences.fr", PerSenderCap: 20,
	})
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	var got veridianColdSimulateResponse
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &got))
	assert.Equal(t, 20, got.SentToday)
	assert.True(t, got.WouldBeCapped, "count 20 >= cap 20 → warmup IP bloque (anti-cramage)")
}

func TestHandleColdSimulate_PerSenderCapDecision_Validation400(t *testing.T) {
	h := newColdSimulateHandler()
	h.SetColdSimulate(&stubColdReplyProcessor{}, mocks.NewMockMessageHistoryRepository(gomock.NewController(t)), nil, "staging")
	for _, req := range []veridianColdSimulateRequest{
		{Mode: "per_sender_cap_decision", WorkspaceID: "ws1", PerSenderCap: 1},                   // sender manquant
		{Mode: "per_sender_cap_decision", WorkspaceID: "ws1", SenderEmail: "b@a.fr", PerSenderCap: 0}, // cap non positif
	} {
		rec := postColdSimulate(t, h, req)
		assert.Equal(t, http.StatusBadRequest, rec.Code)
	}
}

// === mode warmup_cap_decision : prédicat exact de la branche warmup du gate ===
// (CountSentSinceForSenderDomain TOTAL par domaine émetteur, toutes classes confondues)

func TestHandleColdSimulate_WarmupCapDecision_AtCapBlocks(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	msgRepo := mocks.NewMockMessageHistoryRepository(ctrl)
	// COUNT TOTAL par domaine émetteur (aucun filtre de classe destinataire).
	msgRepo.EXPECT().
		CountSentSinceForSenderDomain(gomock.Any(), "ws1", "agences-veridian.fr", gomock.Any()).
		Return(2, nil)

	h := newColdSimulateHandler()
	h.SetColdSimulate(&stubColdReplyProcessor{}, msgRepo, nil, "staging")
	rec := postColdSimulate(t, h, veridianColdSimulateRequest{
		Mode: "warmup_cap_decision", WorkspaceID: "ws1",
		SenderDomain: "bot@Agences-Veridian.fr", WarmupCap: 2, // adresse complète → domaine extrait+lowercé
	})
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	var got veridianColdSimulateResponse
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &got))
	assert.Equal(t, 2, got.SentToday)
	assert.True(t, got.WouldBeCapped, "total 2 >= warmup cap 2 → infra plafonnée (toutes classes)")
	assert.Equal(t, "agences-veridian.fr", got.SenderDomain)
	assert.True(t, got.PerInfra)
}

func TestHandleColdSimulate_WarmupCapDecision_BelowCap(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	msgRepo := mocks.NewMockMessageHistoryRepository(ctrl)
	msgRepo.EXPECT().
		CountSentSinceForSenderDomain(gomock.Any(), "ws1", "send.fr", gomock.Any()).
		Return(1, nil)

	h := newColdSimulateHandler()
	h.SetColdSimulate(&stubColdReplyProcessor{}, msgRepo, nil, "staging")
	rec := postColdSimulate(t, h, veridianColdSimulateRequest{
		Mode: "warmup_cap_decision", WorkspaceID: "ws1", SenderDomain: "send.fr", WarmupCap: 5,
	})
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	var got veridianColdSimulateResponse
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &got))
	assert.Equal(t, 1, got.SentToday)
	assert.False(t, got.WouldBeCapped, "1 < cap 5 → passe")
	// Piège omitempty bool : would_be_capped doit rester présent dans le JSON.
	assert.Contains(t, rec.Body.String(), `"would_be_capped":false`)
}

func TestHandleColdSimulate_WarmupCapDecision_NoSenderDomainNotEnforced(t *testing.T) {
	// Sans domaine émetteur → le gate dégrade en pass (pas d'attribution infra) :
	// la décision reflète ce comportement (jamais capé, 0 COUNT, aucun appel repo).
	msgRepo := mocks.NewMockMessageHistoryRepository(gomock.NewController(t))
	// AUCUN EXPECT : le handler ne doit PAS appeler le repo sans domaine émetteur.

	h := newColdSimulateHandler()
	h.SetColdSimulate(&stubColdReplyProcessor{}, msgRepo, nil, "staging")
	rec := postColdSimulate(t, h, veridianColdSimulateRequest{
		Mode: "warmup_cap_decision", WorkspaceID: "ws1", SenderDomain: "", WarmupCap: 1,
	})
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	var got veridianColdSimulateResponse
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &got))
	assert.False(t, got.WouldBeCapped)
	assert.False(t, got.PerInfra)
	assert.Equal(t, 0, got.SentToday)
}

func TestHandleColdSimulate_WarmupCapDecision_Validation400(t *testing.T) {
	h := newColdSimulateHandler()
	h.SetColdSimulate(&stubColdReplyProcessor{}, mocks.NewMockMessageHistoryRepository(gomock.NewController(t)), nil, "staging")
	rec := postColdSimulate(t, h, veridianColdSimulateRequest{
		Mode: "warmup_cap_decision", WorkspaceID: "ws1", SenderDomain: "send.fr", WarmupCap: 0, // cap non positif
	})
	assert.Equal(t, http.StatusBadRequest, rec.Code)
}

// === mode sending_window_decision : prédicat exact du gate fenêtre (IsWithinWindow) ===

func TestHandleColdSimulate_SendingWindowDecision_OutsideSkips(t *testing.T) {
	h := newColdSimulateHandler()
	h.SetColdSimulate(&stubColdReplyProcessor{}, nil, nil, "staging")
	// Fenêtre 0h00-0h01 UTC : hors fenêtre quasi tout le temps (sauf à minuit pile).
	// On la teste avec un timezone fixe UTC. La probabilité d'être dans la 1ère
	// minute du jour UTC pile pendant le test est négligeable, mais pour être
	// déterministe on assert sur la cohérence within/would_be_skipped, pas sur une
	// valeur fixe (would_be_skipped = !within toujours).
	rec := postColdSimulate(t, h, veridianColdSimulateRequest{
		Mode: "sending_window_decision", WorkspaceID: "ws1",
		SendingWindow: &domain.VeridianSendingWindow{StartHour: 0, EndHour: 0, EndMinute: 1, Timezone: "UTC"},
	})
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	var got veridianColdSimulateResponse
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &got))
	assert.Equal(t, !got.Within, got.WouldBeSkipped, "would_be_skipped == !within (contrat du gate)")
	if got.WouldBeSkipped {
		assert.Greater(t, got.NextOpeningUnix, int64(0), "hors fenêtre → next_opening renseigné")
	}
}

func TestHandleColdSimulate_SendingWindowDecision_AllDayLetsThrough(t *testing.T) {
	h := newColdSimulateHandler()
	h.SetColdSimulate(&stubColdReplyProcessor{}, nil, nil, "staging")
	// Fenêtre 0h-24h tous les jours = toujours dans la fenêtre (non-régression : 24/7).
	rec := postColdSimulate(t, h, veridianColdSimulateRequest{
		Mode: "sending_window_decision", WorkspaceID: "ws1",
		SendingWindow: &domain.VeridianSendingWindow{StartHour: 0, EndHour: 24, Timezone: "UTC"},
	})
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	var got veridianColdSimulateResponse
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &got))
	assert.True(t, got.Within, "fenêtre 0-24h → toujours dans la fenêtre")
	assert.False(t, got.WouldBeSkipped)
	// would_be_skipped DOIT être présent dans le JSON même à false (piège omitempty).
	assert.Contains(t, rec.Body.String(), `"would_be_skipped":false`)
}

func TestHandleColdSimulate_SendingWindowDecision_MissingWindow400(t *testing.T) {
	h := newColdSimulateHandler()
	h.SetColdSimulate(&stubColdReplyProcessor{}, nil, nil, "staging")
	rec := postColdSimulate(t, h, veridianColdSimulateRequest{
		Mode: "sending_window_decision", WorkspaceID: "ws1",
	})
	assert.Equal(t, http.StatusBadRequest, rec.Code)
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
