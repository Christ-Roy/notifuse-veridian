package service

import (
	"context"
	"errors"
	"testing"

	"github.com/Notifuse/notifuse/pkg/hub_mail_gateway"
	pkgmocks "github.com/Notifuse/notifuse/pkg/mocks"
	"github.com/golang/mock/gomock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fakeMailGatewayClient — fake controllable pour le wrapper. On evite
// gomock ici car le client a une seule methode et on veut surtout
// contraindre la sequence des reponses par destinataire (1 reponse par
// appel, dans l'ordre des destinataires d'entree).
type fakeMailGatewayClient struct {
	calls    int
	captured []hub_mail_gateway.SendMailParams
	// responses : (*SendMailResult, error) retourne au n-ieme appel.
	// Si n >= len(responses), retourne le dernier element (steady-state).
	responses []fakeResponse
}

type fakeResponse struct {
	result *hub_mail_gateway.SendMailResult
	err    error
}

func (f *fakeMailGatewayClient) SendMailAsUser(_ context.Context, p hub_mail_gateway.SendMailParams) (*hub_mail_gateway.SendMailResult, error) {
	f.captured = append(f.captured, p)
	idx := f.calls
	f.calls++
	if idx >= len(f.responses) {
		idx = len(f.responses) - 1
	}
	if idx < 0 {
		return &hub_mail_gateway.SendMailResult{OK: true, MessageID: "default-msg"}, nil
	}
	return f.responses[idx].result, f.responses[idx].err
}

// baseParams — params de reference reutilisables pour les tests. Le To
// est vide intentionnellement : le wrapper le remplace par recipient
// individuel a chaque iteration.
func baseParams() hub_mail_gateway.SendMailParams {
	return hub_mail_gateway.SendMailParams{
		UserID:   "hub-user-uuid",
		Subject:  "Broadcast subject",
		BodyText: "Hi there",
		// IdempotencyKey ignore par le wrapper (genere par destinataire)
	}
}

// newHandler — construit un handler avec un newIdempotencyKey deterministe
// pour faciliter les assertions sur captured params.
func newHandler(t *testing.T, fake *fakeMailGatewayClient, mockLogger *pkgmocks.MockLogger) *broadcastRateLimitHandler {
	t.Helper()
	keyCounter := 0
	return &broadcastRateLimitHandler{
		mailGateway: fake,
		logger:      mockLogger,
		newIdempotencyKey: func() string {
			keyCounter++
			// Format UUID-ish stable pour assertions (pas valide v4 mais peu
			// importe — le Hub n'est pas appele en test).
			return "idem-test-key-" + intToString(keyCounter)
		},
	}
}

func intToString(n int) string {
	if n == 0 {
		return "0"
	}
	digits := []byte{}
	for n > 0 {
		digits = append([]byte{byte('0' + n%10)}, digits...)
		n /= 10
	}
	return string(digits)
}

func setupBroadcastRateLimitLogger(t *testing.T) (*pkgmocks.MockLogger, func()) {
	t.Helper()
	ctrl := gomock.NewController(t)
	mockLogger := pkgmocks.NewMockLogger(ctrl)
	mockLogger.EXPECT().WithFields(gomock.Any()).Return(mockLogger).AnyTimes()
	mockLogger.EXPECT().Warn(gomock.Any()).AnyTimes()
	mockLogger.EXPECT().Info(gomock.Any()).AnyTimes()
	mockLogger.EXPECT().Debug(gomock.Any()).AnyTimes()
	return mockLogger, ctrl.Finish
}

// TestSendToRecipients_AllSuccess — happy path. 3 destinataires, tous OK,
// 3 results OK + MessageID + ordre preserve + idempotency_key unique par
// destinataire.
func TestSendToRecipients_AllSuccess(t *testing.T) {
	mockLogger, cleanup := setupBroadcastRateLimitLogger(t)
	defer cleanup()

	fake := &fakeMailGatewayClient{
		responses: []fakeResponse{
			{result: &hub_mail_gateway.SendMailResult{OK: true, MessageID: "msg-1"}, err: nil},
			{result: &hub_mail_gateway.SendMailResult{OK: true, MessageID: "msg-2"}, err: nil},
			{result: &hub_mail_gateway.SendMailResult{OK: true, MessageID: "msg-3"}, err: nil},
		},
	}
	h := newHandler(t, fake, mockLogger)

	recipients := []string{"a@example.com", "b@example.com", "c@example.com"}
	results := h.SendToRecipients(context.Background(), baseParams(), recipients)

	require.Len(t, results, 3)
	for i, r := range results {
		assert.True(t, r.OK, "result[%d] should be OK", i)
		assert.Equal(t, recipients[i], r.Recipient)
		assert.Empty(t, r.Reason)
		assert.Zero(t, r.RetryAfterSeconds)
	}
	assert.Equal(t, "msg-1", results[0].MessageID)
	assert.Equal(t, "msg-2", results[1].MessageID)
	assert.Equal(t, "msg-3", results[2].MessageID)

	// Verifier que chaque appel a recu UN seul destinataire + idempotency_key
	// unique. Anti-aliasing : To[0] != captured[0].To[0] si bug.
	require.Len(t, fake.captured, 3)
	for i, captured := range fake.captured {
		require.Len(t, captured.To, 1, "captured[%d].To must contain exactly 1 recipient", i)
		assert.Equal(t, recipients[i], captured.To[0])
		assert.NotEmpty(t, captured.IdempotencyKey)
	}
	// Unicite idempotency_key
	keys := map[string]struct{}{}
	for _, c := range fake.captured {
		keys[c.IdempotencyKey] = struct{}{}
	}
	assert.Len(t, keys, 3, "idempotency keys must be unique per recipient")
}

// TestSendToRecipients_OneRateLimited_OthersContinue — cas central de la
// vague 7. 3 destinataires, le 2nd retourne 429 recipient_rate_limited.
// Verdicts attendus : result[0] OK, result[1] not OK + Reason +
// RetryAfterSeconds preserve, result[2] OK. Le batch n'a PAS plante.
func TestSendToRecipients_OneRateLimited_OthersContinue(t *testing.T) {
	mockLogger, cleanup := setupBroadcastRateLimitLogger(t)
	defer cleanup()

	fake := &fakeMailGatewayClient{
		responses: []fakeResponse{
			{result: &hub_mail_gateway.SendMailResult{OK: true, MessageID: "msg-1"}, err: nil},
			{result: &hub_mail_gateway.SendMailResult{
				OK:                false,
				Reason:            hub_mail_gateway.ReasonRecipientRateLimited,
				RetryAfterSeconds: 1200, // 20 min
				Recipient:         "b@example.com",
				HTTPStatus:        429,
			}, err: nil},
			{result: &hub_mail_gateway.SendMailResult{OK: true, MessageID: "msg-3"}, err: nil},
		},
	}
	h := newHandler(t, fake, mockLogger)

	recipients := []string{"a@example.com", "b@example.com", "c@example.com"}
	results := h.SendToRecipients(context.Background(), baseParams(), recipients)

	require.Len(t, results, 3)

	assert.True(t, results[0].OK)
	assert.Equal(t, "msg-1", results[0].MessageID)

	assert.False(t, results[1].OK, "rate-limited recipient must be not OK")
	assert.Equal(t, "b@example.com", results[1].Recipient)
	assert.Equal(t, hub_mail_gateway.ReasonRecipientRateLimited, results[1].Reason)
	assert.Equal(t, 1200, results[1].RetryAfterSeconds)
	assert.Empty(t, results[1].MessageID)

	assert.True(t, results[2].OK, "post-rate-limited recipient must be processed")
	assert.Equal(t, "msg-3", results[2].MessageID)

	// Tous les destinataires ont bien ete appeles (preuve que le batch n'a pas court-circuite)
	assert.Equal(t, 3, fake.calls)
}

// TestSendToRecipients_NeedsReauth_Continues — autre Reason (needs_reauth)
// doit aussi etre tolere : on capture le Reason, on continue.
func TestSendToRecipients_NeedsReauth_Continues(t *testing.T) {
	mockLogger, cleanup := setupBroadcastRateLimitLogger(t)
	defer cleanup()

	fake := &fakeMailGatewayClient{
		responses: []fakeResponse{
			{result: &hub_mail_gateway.SendMailResult{OK: true, MessageID: "msg-1"}, err: nil},
			{result: &hub_mail_gateway.SendMailResult{
				OK:         false,
				Reason:     hub_mail_gateway.ReasonNeedsReauth,
				HTTPStatus: 412,
			}, err: nil},
			{result: &hub_mail_gateway.SendMailResult{OK: true, MessageID: "msg-3"}, err: nil},
		},
	}
	h := newHandler(t, fake, mockLogger)

	results := h.SendToRecipients(context.Background(), baseParams(), []string{"a@x.com", "b@x.com", "c@x.com"})

	require.Len(t, results, 3)
	assert.True(t, results[0].OK)
	assert.False(t, results[1].OK)
	assert.Equal(t, hub_mail_gateway.ReasonNeedsReauth, results[1].Reason)
	assert.Zero(t, results[1].RetryAfterSeconds, "needs_reauth doit pas peupler RetryAfterSeconds")
	assert.True(t, results[2].OK)
	assert.Equal(t, 3, fake.calls)
}

// TestSendToRecipients_EmptyList — 0 destinataires : results vide, pas
// de panic, pas d'appel au gateway.
func TestSendToRecipients_EmptyList(t *testing.T) {
	mockLogger, cleanup := setupBroadcastRateLimitLogger(t)
	defer cleanup()

	fake := &fakeMailGatewayClient{}
	h := newHandler(t, fake, mockLogger)

	results := h.SendToRecipients(context.Background(), baseParams(), []string{})
	assert.Empty(t, results)
	assert.Equal(t, 0, fake.calls)

	// Pareil avec nil slice — defensif
	results = h.SendToRecipients(context.Background(), baseParams(), nil)
	assert.Empty(t, results)
	assert.Equal(t, 0, fake.calls)
}

// TestSendToRecipients_GatewayError_CapturedAsSendError — si le client
// retourne err (params invalides, ErrMailGatewayDisabled, marshal fail),
// on capture send_error et on continue. Pas de panic.
func TestSendToRecipients_GatewayError_CapturedAsSendError(t *testing.T) {
	mockLogger, cleanup := setupBroadcastRateLimitLogger(t)
	defer cleanup()

	fake := &fakeMailGatewayClient{
		responses: []fakeResponse{
			{result: nil, err: errors.New("disabled")},
			{result: &hub_mail_gateway.SendMailResult{OK: true, MessageID: "msg-2"}, err: nil},
		},
	}
	h := newHandler(t, fake, mockLogger)

	results := h.SendToRecipients(context.Background(), baseParams(), []string{"a@x.com", "b@x.com"})

	require.Len(t, results, 2)
	assert.False(t, results[0].OK)
	assert.Equal(t, "send_error", results[0].Reason)
	assert.True(t, results[1].OK)
	assert.Equal(t, "msg-2", results[1].MessageID)
}

// TestSendToRecipients_NilGateway_DegradedMode — handler instantie sans
// mailGateway (cas tests / mode degrade fail-soft). Tous les recipients
// retournent send_error sans panic.
func TestSendToRecipients_NilGateway_DegradedMode(t *testing.T) {
	mockLogger, cleanup := setupBroadcastRateLimitLogger(t)
	defer cleanup()

	h := &broadcastRateLimitHandler{
		mailGateway:       nil, // explicit
		logger:            mockLogger,
		newIdempotencyKey: func() string { return "irrelevant" },
	}

	results := h.SendToRecipients(context.Background(), baseParams(), []string{"a@x.com", "b@x.com"})

	require.Len(t, results, 2)
	for _, r := range results {
		assert.False(t, r.OK)
		assert.Equal(t, "send_error", r.Reason)
	}
}

// TestSendToRecipients_ToFieldAntiAliasing — bug subtil : si on partage le
// slice `To` entre params et iteration, les destinataires precedents sont
// ecrases dans les call captures. Ce test prouve que chaque appel recoit
// un slice independant avec UN seul destinataire.
func TestSendToRecipients_ToFieldAntiAliasing(t *testing.T) {
	mockLogger, cleanup := setupBroadcastRateLimitLogger(t)
	defer cleanup()

	fake := &fakeMailGatewayClient{
		responses: []fakeResponse{
			{result: &hub_mail_gateway.SendMailResult{OK: true, MessageID: "m"}, err: nil},
		},
	}
	h := newHandler(t, fake, mockLogger)

	recipients := []string{"x@x.com", "y@y.com", "z@z.com"}
	_ = h.SendToRecipients(context.Background(), baseParams(), recipients)

	require.Len(t, fake.captured, 3)
	for i, captured := range fake.captured {
		require.Len(t, captured.To, 1)
		assert.Equal(t, recipients[i], captured.To[0])
	}
}

// TestNewBroadcastRateLimitHandler_DefaultIdempotencyKey — constructeur
// public doit cabler newIdempotencyKey vers uuid.New().String(). On
// verifie que les keys sont non-vides et distinctes (pas un singleton).
func TestNewBroadcastRateLimitHandler_DefaultIdempotencyKey(t *testing.T) {
	mockLogger, cleanup := setupBroadcastRateLimitLogger(t)
	defer cleanup()

	fake := &fakeMailGatewayClient{
		responses: []fakeResponse{
			{result: &hub_mail_gateway.SendMailResult{OK: true, MessageID: "m"}, err: nil},
		},
	}
	h := NewBroadcastRateLimitHandler(fake, mockLogger)
	_ = h.SendToRecipients(context.Background(), baseParams(), []string{"a@x.com", "b@x.com"})

	require.Len(t, fake.captured, 2)
	assert.NotEmpty(t, fake.captured[0].IdempotencyKey)
	assert.NotEmpty(t, fake.captured[1].IdempotencyKey)
	assert.NotEqual(t, fake.captured[0].IdempotencyKey, fake.captured[1].IdempotencyKey)
}
