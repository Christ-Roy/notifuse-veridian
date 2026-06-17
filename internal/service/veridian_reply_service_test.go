package service

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/Notifuse/notifuse/internal/domain"
	"github.com/Notifuse/notifuse/internal/domain/mocks"
	"github.com/golang/mock/gomock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type replyTestMocks struct {
	replyRepo    *mocks.MockVeridianContactReplyRepository
	messageRepo  *mocks.MockMessageHistoryRepository
	contactRepo  *mocks.MockContactRepository
	autoRepo     *mocks.MockAutomationRepository
	timelineRepo *mocks.MockContactTimelineRepository
	svc          *VeridianReplyService
}

func newReplyTestMocks(t *testing.T) (*replyTestMocks, *gomock.Controller) {
	ctrl := gomock.NewController(t)
	m := &replyTestMocks{
		replyRepo:    mocks.NewMockVeridianContactReplyRepository(ctrl),
		messageRepo:  mocks.NewMockMessageHistoryRepository(ctrl),
		contactRepo:  mocks.NewMockContactRepository(ctrl),
		autoRepo:     mocks.NewMockAutomationRepository(ctrl),
		timelineRepo: mocks.NewMockContactTimelineRepository(ctrl),
	}
	m.svc = NewVeridianReplyService(
		m.replyRepo, m.messageRepo, m.contactRepo, m.autoRepo, m.timelineRepo,
		setupMockLogger(ctrl),
	)
	return m, ctrl
}

const replyWS = "ws1"

// ============ DetectReply ============

func TestVeridianReply_Detect_MatchByInReplyTo(t *testing.T) {
	m, ctrl := newReplyTestMocks(t)
	defer ctrl.Finish()

	msg := &domain.VeridianIMAPMessage{
		WorkspaceID: replyWS,
		From:        "Prospect <prospect@acme.fr>",
		Subject:     "Re: votre proposition",
		InReplyTo:   "<sent-uuid-1@send.veridian.site>",
	}
	// Le Message-ID cité correspond à notre envoi vers ce contact.
	m.messageRepo.EXPECT().
		FindContactEmailByMessageID(gomock.Any(), replyWS, "sent-uuid-1").
		Return("prospect@acme.fr", true, nil)

	det, err := m.svc.DetectReply(context.Background(), msg)
	require.NoError(t, err)
	assert.True(t, det.IsReply)
	assert.Equal(t, domain.VeridianReplyMatchMessageID, det.MatchType)
	assert.Equal(t, "prospect@acme.fr", det.ContactEmail)
	assert.Equal(t, "sent-uuid-1", det.MatchedMessageID)
}

func TestVeridianReply_Detect_MatchByReferences(t *testing.T) {
	m, ctrl := newReplyTestMocks(t)
	defer ctrl.Finish()

	msg := &domain.VeridianIMAPMessage{
		WorkspaceID: replyWS,
		From:        "prospect@acme.fr",
		Subject:     "Re: suivi",
		// Pas d'In-Reply-To ; le match vient des References.
		References: []string{"<unknown-thread-root@gmail.com> <sent-uuid-2@send.veridian.site>"},
	}
	// 1er candidat (root gmail) inconnu, 2e (notre envoi) trouvé.
	m.messageRepo.EXPECT().
		FindContactEmailByMessageID(gomock.Any(), replyWS, "unknown-thread-root").
		Return("", false, nil)
	m.messageRepo.EXPECT().
		FindContactEmailByMessageID(gomock.Any(), replyWS, "sent-uuid-2").
		Return("prospect@acme.fr", true, nil)

	det, err := m.svc.DetectReply(context.Background(), msg)
	require.NoError(t, err)
	assert.True(t, det.IsReply)
	assert.Equal(t, domain.VeridianReplyMatchMessageID, det.MatchType)
	assert.Equal(t, "sent-uuid-2", det.MatchedMessageID)
}

func TestVeridianReply_Detect_MessageIDOursButDifferentSender_NoMatch_FallbackTried(t *testing.T) {
	m, ctrl := newReplyTestMocks(t)
	defer ctrl.Finish()

	msg := &domain.VeridianIMAPMessage{
		WorkspaceID: replyWS,
		From:        "someone-else@other.com", // transfert / thread tiers
		Subject:     "Fwd: une offre",
		InReplyTo:   "<sent-uuid-3@send.veridian.site>",
	}
	// Notre envoi, mais vers un AUTRE contact que l'expéditeur actuel.
	m.messageRepo.EXPECT().
		FindContactEmailByMessageID(gomock.Any(), replyWS, "sent-uuid-3").
		Return("original-prospect@acme.fr", true, nil)
	// Match fort écarté (sender ≠) → fallback : someone-else n'est pas un contact connu.
	m.contactRepo.EXPECT().
		GetContactByEmail(gomock.Any(), replyWS, "someone-else@other.com").
		Return(nil, errors.New("not found"))

	det, err := m.svc.DetectReply(context.Background(), msg)
	require.NoError(t, err)
	assert.False(t, det.IsReply)
}

func TestVeridianReply_Detect_FallbackBySender_KnownContact(t *testing.T) {
	m, ctrl := newReplyTestMocks(t)
	defer ctrl.Finish()

	msg := &domain.VeridianIMAPMessage{
		WorkspaceID: replyWS,
		From:        "Known <known@lead.fr>",
		Subject:     "une question", // pas de Re:, pas de threading
	}
	// Aucun In-Reply-To/References → fallback direct sur le From.
	m.contactRepo.EXPECT().
		GetContactByEmail(gomock.Any(), replyWS, "known@lead.fr").
		Return(&domain.Contact{Email: "known@lead.fr"}, nil)

	det, err := m.svc.DetectReply(context.Background(), msg)
	require.NoError(t, err)
	assert.True(t, det.IsReply)
	assert.Equal(t, domain.VeridianReplyMatchSenderFallback, det.MatchType)
	assert.Equal(t, "known@lead.fr", det.ContactEmail)
	assert.Empty(t, det.MatchedMessageID)
}

func TestVeridianReply_Detect_RandomMail_NoMatch(t *testing.T) {
	m, ctrl := newReplyTestMocks(t)
	defer ctrl.Finish()

	msg := &domain.VeridianIMAPMessage{
		WorkspaceID: replyWS,
		From:        "stranger@nowhere.com",
		Subject:     "newsletter",
	}
	m.contactRepo.EXPECT().
		GetContactByEmail(gomock.Any(), replyWS, "stranger@nowhere.com").
		Return(nil, errors.New("contact not found"))

	det, err := m.svc.DetectReply(context.Background(), msg)
	require.NoError(t, err)
	assert.False(t, det.IsReply)
}

func TestVeridianReply_Detect_NDR_Ignored(t *testing.T) {
	m, ctrl := newReplyTestMocks(t)
	defer ctrl.Finish()

	// NDR MAILER-DAEMON : ne doit JAMAIS être traité comme une réponse (c'est le
	// bounce-loop Lot 2 qui s'en charge). Aucun lookup message/contact attendu.
	msg := &domain.VeridianIMAPMessage{
		WorkspaceID: replyWS,
		From:        "MAILER-DAEMON@send.veridian.site",
		Subject:     "Undelivered Mail Returned to Sender",
		InReplyTo:   "<sent-uuid-x@send.veridian.site>", // présent mais ignoré (NDR d'abord)
	}

	det, err := m.svc.DetectReply(context.Background(), msg)
	require.NoError(t, err)
	assert.False(t, det.IsReply)
}

func TestVeridianReply_Detect_NDRWithBody_FromHumanLikeAddress_Ignored(t *testing.T) {
	// NDR dont le From n'est PAS un daemon évident (relai qui réécrit l'enveloppe),
	// mais le CORPS est un vrai delivery-status report. Le garde-fou daemon ne suffit
	// pas ; c'est le parseur veridian_ndr (sur le body) qui doit le classer NDR.
	m, ctrl := newReplyTestMocks(t)
	defer ctrl.Finish()

	rawNDR := []byte("From: bounce-handler@relay.example\r\n" +
		"Subject: Undelivered Mail Returned to Sender\r\n" +
		"Content-Type: multipart/report; report-type=delivery-status; boundary=\"b\"\r\n\r\n" +
		"--b\r\nContent-Type: message/delivery-status\r\n\r\n" +
		"Final-Recipient: rfc822; dead@target.com\r\n" +
		"Action: failed\r\nStatus: 5.1.1\r\n\r\n--b--\r\n")

	msg := &domain.VeridianIMAPMessage{
		WorkspaceID: replyWS,
		From:        "bounce-handler@relay.example",
		Subject:     "Undelivered Mail Returned to Sender",
		InReplyTo:   "<sent-uuid-z@send.veridian.site>", // présent mais ignoré (NDR)
		RawBody:     rawNDR,
	}

	det, err := m.svc.DetectReply(context.Background(), msg)
	require.NoError(t, err)
	assert.False(t, det.IsReply, "a real delivery-status report must never be a reply")
}

func TestVeridianReply_Detect_LookupError_FallsThrough(t *testing.T) {
	m, ctrl := newReplyTestMocks(t)
	defer ctrl.Finish()

	msg := &domain.VeridianIMAPMessage{
		WorkspaceID: replyWS,
		From:        "prospect@acme.fr",
		Subject:     "Re: hello",
		InReplyTo:   "<sent-uuid-9@send.veridian.site>",
	}
	// Erreur DB sur le lookup fort → best-effort : on tente le fallback.
	m.messageRepo.EXPECT().
		FindContactEmailByMessageID(gomock.Any(), replyWS, "sent-uuid-9").
		Return("", false, errors.New("db blip"))
	m.contactRepo.EXPECT().
		GetContactByEmail(gomock.Any(), replyWS, "prospect@acme.fr").
		Return(&domain.Contact{Email: "prospect@acme.fr"}, nil)

	det, err := m.svc.DetectReply(context.Background(), msg)
	require.NoError(t, err)
	assert.True(t, det.IsReply)
	assert.Equal(t, domain.VeridianReplyMatchSenderFallback, det.MatchType)
}

// ============ ProcessInboundMessage (action) ============

func TestVeridianReply_Process_MarksSignal_TimelineEvent_ExitsAutomation(t *testing.T) {
	m, ctrl := newReplyTestMocks(t)
	defer ctrl.Finish()

	repliedDate := time.Date(2026, 6, 15, 9, 0, 0, 0, time.UTC)
	msg := &domain.VeridianIMAPMessage{
		WorkspaceID: replyWS,
		From:        "prospect@acme.fr",
		Subject:     "Re: proposition",
		InReplyTo:   "<sent-uuid-1@send.veridian.site>",
		Date:        repliedDate,
	}

	// Détection match fort.
	m.messageRepo.EXPECT().
		FindContactEmailByMessageID(gomock.Any(), replyWS, "sent-uuid-1").
		Return("prospect@acme.fr", true, nil)
	// Pas encore replied.
	m.replyRepo.EXPECT().HasReplied(gomock.Any(), replyWS, "prospect@acme.fr").Return(false, nil)
	// Signal posé.
	m.replyRepo.EXPECT().
		MarkReplied(gomock.Any(), replyWS, gomock.AssignableToTypeOf(&domain.VeridianContactReply{})).
		DoAndReturn(func(_ context.Context, _ string, r *domain.VeridianContactReply) error {
			assert.Equal(t, "prospect@acme.fr", r.ContactEmail)
			assert.Equal(t, domain.VeridianReplyMatchMessageID, r.MatchType)
			assert.Equal(t, "sent-uuid-1", r.MatchedMessageID)
			assert.Equal(t, repliedDate, r.RepliedAt)
			return nil
		})
	// Timeline email.replied.
	m.timelineRepo.EXPECT().
		Create(gomock.Any(), replyWS, gomock.AssignableToTypeOf(&domain.ContactTimelineEntry{})).
		DoAndReturn(func(_ context.Context, _ string, e *domain.ContactTimelineEntry) error {
			assert.Equal(t, "email.replied", e.Kind)
			assert.Equal(t, "prospect@acme.fr", e.Email)
			return nil
		})
	// Exit actif : 1 automation active trouvée → exitée avec reason replied.
	activeCA := &domain.ContactAutomation{
		ID:           "ca1",
		AutomationID: "auto1",
		ContactEmail: "prospect@acme.fr",
		Status:       domain.ContactAutomationStatusActive,
	}
	m.autoRepo.EXPECT().
		ListContactAutomations(gomock.Any(), replyWS, gomock.Any()).
		Return([]*domain.ContactAutomation{activeCA}, 1, nil)
	m.autoRepo.EXPECT().
		UpdateContactAutomation(gomock.Any(), replyWS, gomock.Any()).
		DoAndReturn(func(_ context.Context, _ string, ca *domain.ContactAutomation) error {
			assert.Equal(t, domain.ContactAutomationStatusExited, ca.Status)
			require.NotNil(t, ca.ExitReason)
			assert.Equal(t, domain.ExitReasonReplied, *ca.ExitReason)
			assert.Nil(t, ca.CurrentNodeID)
			assert.Nil(t, ca.ScheduledAt)
			return nil
		})
	m.autoRepo.EXPECT().IncrementAutomationStat(gomock.Any(), replyWS, "auto1", "exited").Return(nil)
	// automation.end timeline event lors de l'exit.
	m.timelineRepo.EXPECT().
		Create(gomock.Any(), replyWS, gomock.Any()).
		DoAndReturn(func(_ context.Context, _ string, e *domain.ContactTimelineEntry) error {
			assert.Equal(t, "automation.end", e.Kind)
			return nil
		})

	err := m.svc.ProcessInboundMessage(context.Background(), msg)
	require.NoError(t, err)
}

func TestVeridianReply_Process_EmitsRepliedToHub(t *testing.T) {
	m, ctrl := newReplyTestMocks(t)
	defer ctrl.Finish()

	emitter := mocks.NewMockWebhookEmitter(ctrl)
	m.svc.SetVeridianWebhookEmitter(emitter)

	repliedDate := time.Date(2026, 6, 17, 10, 0, 0, 0, time.UTC)
	msg := &domain.VeridianIMAPMessage{
		WorkspaceID: replyWS,
		From:        "prospect@acme.fr",
		Subject:     "Re: proposition",
		InReplyTo:   "<sent-uuid-9@send.veridian.site>",
		Date:        repliedDate,
	}

	m.messageRepo.EXPECT().
		FindContactEmailByMessageID(gomock.Any(), replyWS, "sent-uuid-9").
		Return("prospect@acme.fr", true, nil)
	m.replyRepo.EXPECT().HasReplied(gomock.Any(), replyWS, "prospect@acme.fr").Return(false, nil)
	m.replyRepo.EXPECT().MarkReplied(gomock.Any(), replyWS, gomock.Any()).Return(nil)
	m.timelineRepo.EXPECT().Create(gomock.Any(), replyWS, gomock.Any()).Return(nil)
	m.autoRepo.EXPECT().
		ListContactAutomations(gomock.Any(), replyWS, gomock.Any()).
		Return([]*domain.ContactAutomation{}, 0, nil)

	// L'event email.replied DOIT être poussé vers le Hub avec contact_email + message_id.
	emitter.EXPECT().
		Emit(gomock.Any(), domain.EventEmailReplied, replyWS, gomock.Any()).
		DoAndReturn(func(_ context.Context, _ domain.VeridianEvent, _ string, data map[string]interface{}) {
			assert.Equal(t, "prospect@acme.fr", data["contact_email"])
			assert.Equal(t, "sent-uuid-9", data["message_id"])
			assert.NotEmpty(t, data["occurred_at"])
		})

	require.NoError(t, m.svc.ProcessInboundMessage(context.Background(), msg))
}

func TestVeridianReply_Process_NoEmitterIsNoop(t *testing.T) {
	// Sans emitter injecté, ProcessInboundMessage ne tente aucune émission (nil-safe).
	m, ctrl := newReplyTestMocks(t)
	defer ctrl.Finish()

	msg := &domain.VeridianIMAPMessage{
		WorkspaceID: replyWS,
		From:        "prospect@acme.fr",
		InReplyTo:   "<sent-uuid-10@send.veridian.site>",
	}
	m.messageRepo.EXPECT().
		FindContactEmailByMessageID(gomock.Any(), replyWS, "sent-uuid-10").
		Return("prospect@acme.fr", true, nil)
	m.replyRepo.EXPECT().HasReplied(gomock.Any(), replyWS, "prospect@acme.fr").Return(false, nil)
	m.replyRepo.EXPECT().MarkReplied(gomock.Any(), replyWS, gomock.Any()).Return(nil)
	m.timelineRepo.EXPECT().Create(gomock.Any(), replyWS, gomock.Any()).Return(nil)
	m.autoRepo.EXPECT().
		ListContactAutomations(gomock.Any(), replyWS, gomock.Any()).
		Return([]*domain.ContactAutomation{}, 0, nil)

	require.NoError(t, m.svc.ProcessInboundMessage(context.Background(), msg))
}

func TestVeridianReply_Process_Idempotent_AlreadyReplied_NoOp(t *testing.T) {
	m, ctrl := newReplyTestMocks(t)
	defer ctrl.Finish()

	msg := &domain.VeridianIMAPMessage{
		WorkspaceID: replyWS,
		From:        "prospect@acme.fr",
		Subject:     "Re: again",
		InReplyTo:   "<sent-uuid-1@send.veridian.site>",
	}
	m.messageRepo.EXPECT().
		FindContactEmailByMessageID(gomock.Any(), replyWS, "sent-uuid-1").
		Return("prospect@acme.fr", true, nil)
	// Déjà marqué replied → court-circuit : ni MarkReplied, ni timeline, ni exit.
	m.replyRepo.EXPECT().HasReplied(gomock.Any(), replyWS, "prospect@acme.fr").Return(true, nil)

	err := m.svc.ProcessInboundMessage(context.Background(), msg)
	require.NoError(t, err)
	// Aucune autre attente : gomock vérifie qu'on n'a PAS appelé MarkReplied/exit.
}

func TestVeridianReply_Process_NotAReply_NoOp(t *testing.T) {
	m, ctrl := newReplyTestMocks(t)
	defer ctrl.Finish()

	// NDR → pas une réponse → ProcessInboundMessage ne fait rien.
	msg := &domain.VeridianIMAPMessage{
		WorkspaceID: replyWS,
		From:        "postmaster@send.veridian.site",
		Subject:     "Delivery Status Notification (Failure)",
	}
	err := m.svc.ProcessInboundMessage(context.Background(), msg)
	require.NoError(t, err)
}

func TestVeridianReply_Process_TwiceSameReply_OneExit(t *testing.T) {
	// Simule un re-dispatch IMAP (MarkSeen raté) : 2 appels Process sur la même
	// réponse → au 2e, HasReplied=true coupe tout. Idempotence métier garantie.
	m, ctrl := newReplyTestMocks(t)
	defer ctrl.Finish()

	msg := &domain.VeridianIMAPMessage{
		WorkspaceID: replyWS,
		From:        "prospect@acme.fr",
		Subject:     "Re: x",
		InReplyTo:   "<sent-uuid-1@send.veridian.site>",
	}
	// 1er passage : détection + signal + exit (0 automation active pour simplifier).
	gomock.InOrder(
		m.messageRepo.EXPECT().FindContactEmailByMessageID(gomock.Any(), replyWS, "sent-uuid-1").
			Return("prospect@acme.fr", true, nil),
		m.replyRepo.EXPECT().HasReplied(gomock.Any(), replyWS, "prospect@acme.fr").Return(false, nil),
		m.replyRepo.EXPECT().MarkReplied(gomock.Any(), replyWS, gomock.Any()).Return(nil),
	)
	m.timelineRepo.EXPECT().Create(gomock.Any(), replyWS, gomock.Any()).Return(nil)
	m.autoRepo.EXPECT().ListContactAutomations(gomock.Any(), replyWS, gomock.Any()).
		Return([]*domain.ContactAutomation{}, 0, nil)

	require.NoError(t, m.svc.ProcessInboundMessage(context.Background(), msg))

	// 2e passage : détection identique, mais HasReplied=true → STOP, aucun MarkReplied.
	m.messageRepo.EXPECT().FindContactEmailByMessageID(gomock.Any(), replyWS, "sent-uuid-1").
		Return("prospect@acme.fr", true, nil)
	m.replyRepo.EXPECT().HasReplied(gomock.Any(), replyWS, "prospect@acme.fr").Return(true, nil)

	require.NoError(t, m.svc.ProcessInboundMessage(context.Background(), msg))
}

func TestVeridianReply_HasReplied_DelegatesToRepo(t *testing.T) {
	m, ctrl := newReplyTestMocks(t)
	defer ctrl.Finish()

	// Implémente bien le contrat ColdReplyChecker du Lot 9.
	var _ ColdReplyChecker = m.svc

	m.replyRepo.EXPECT().HasReplied(gomock.Any(), replyWS, "prospect@acme.fr").Return(true, nil)
	ok, err := m.svc.HasReplied(context.Background(), replyWS, "Prospect@ACME.fr")
	require.NoError(t, err)
	assert.True(t, ok)
}

func TestVeridianReply_resolveRepliedAt(t *testing.T) {
	m, ctrl := newReplyTestMocks(t)
	defer ctrl.Finish()

	// Date plausible conservée.
	plausible := time.Date(2026, 6, 15, 8, 0, 0, 0, time.UTC)
	assert.Equal(t, plausible, m.svc.resolveRepliedAt(plausible))

	// Date zéro → now (à la seconde près).
	got := m.svc.resolveRepliedAt(time.Time{})
	assert.WithinDuration(t, time.Now().UTC(), got, 5*time.Second)

	// Date aberrante (an 1980) → now.
	got = m.svc.resolveRepliedAt(time.Date(1980, 1, 1, 0, 0, 0, 0, time.UTC))
	assert.WithinDuration(t, time.Now().UTC(), got, 5*time.Second)

	// Date trop loin dans le futur → now.
	got = m.svc.resolveRepliedAt(time.Now().Add(72 * time.Hour))
	assert.WithinDuration(t, time.Now().UTC(), got, 5*time.Second)
}
