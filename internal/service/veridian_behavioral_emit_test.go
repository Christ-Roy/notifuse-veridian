package service

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/Notifuse/notifuse/internal/domain"
	"github.com/Notifuse/notifuse/internal/domain/mocks"
	pkgmocks "github.com/Notifuse/notifuse/pkg/mocks"
	"github.com/golang/mock/gomock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newBehavioralEmailService(ctrl *gomock.Controller) (*EmailService, *mocks.MockMessageHistoryRepository) {
	msgRepo := mocks.NewMockMessageHistoryRepository(ctrl)
	logger := pkgmocks.NewMockLogger(ctrl)
	// Le logger n'est touché qu'en cas d'erreur/miss lookup ; on l'autorise large.
	logger.EXPECT().WithFields(gomock.Any()).Return(logger).AnyTimes()
	logger.EXPECT().Warn(gomock.Any()).AnyTimes()
	logger.EXPECT().Debug(gomock.Any()).AnyTimes()
	svc := &EmailService{
		logger:      logger,
		messageRepo: msgRepo,
	}
	return svc, msgRepo
}

func TestEmailService_VisitLink_EmitsClickedToHub(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	svc, msgRepo := newBehavioralEmailService(ctrl)
	emitter := mocks.NewMockWebhookEmitter(ctrl)
	svc.SetVeridianWebhookEmitter(emitter)

	ctx := context.Background()
	ws, msgID := "ws-1", "msg-1"

	msgRepo.EXPECT().SetClicked(ctx, ws, msgID, gomock.Any()).Return(nil)
	msgRepo.EXPECT().
		FindContactEmailByMessageID(ctx, ws, msgID).
		Return("Lead@Acme.fr", true, nil)

	emitter.EXPECT().
		Emit(ctx, domain.EventEmailClicked, ws, gomock.Any()).
		DoAndReturn(func(_ context.Context, _ domain.VeridianEvent, _ string, data map[string]interface{}) {
			// contact_email normalisé (lowercase/trim) = clé de jointure V1.
			assert.Equal(t, "lead@acme.fr", data["contact_email"])
			assert.Equal(t, msgID, data["message_id"])
			assert.NotEmpty(t, data["occurred_at"])
		})

	require.NoError(t, svc.VisitLink(ctx, msgID, ws))
}

func TestEmailService_OpenEmail_EmitsOpenedToHub(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	svc, msgRepo := newBehavioralEmailService(ctrl)
	emitter := mocks.NewMockWebhookEmitter(ctrl)
	svc.SetVeridianWebhookEmitter(emitter)

	ctx := context.Background()
	ws, msgID := "ws-2", "msg-2"

	msgRepo.EXPECT().SetOpened(ctx, ws, msgID, gomock.Any()).Return(nil)
	msgRepo.EXPECT().
		FindContactEmailByMessageID(ctx, ws, msgID).
		Return("prospect@gmail.com", true, nil)
	emitter.EXPECT().Emit(ctx, domain.EventEmailOpened, ws, gomock.Any())

	require.NoError(t, svc.OpenEmail(ctx, msgID, ws))
}

func TestEmailService_OpenEmail_NoEmitterIsNoop(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	svc, msgRepo := newBehavioralEmailService(ctrl)
	// Pas d'emitter → aucune émission, aucun lookup contact_email (court-circuit).
	ctx := context.Background()
	msgRepo.EXPECT().SetOpened(ctx, "ws", "m", gomock.Any()).Return(nil)
	// Pas d'EXPECT sur FindContactEmailByMessageID : ne doit pas être appelé.

	require.NoError(t, svc.OpenEmail(ctx, "m", "ws"))
}

func TestEmailService_VisitLink_LookupFailStillEmitsBestEffort(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	svc, msgRepo := newBehavioralEmailService(ctrl)
	emitter := mocks.NewMockWebhookEmitter(ctrl)
	svc.SetVeridianWebhookEmitter(emitter)

	ctx := context.Background()
	ws, msgID := "ws-3", "msg-3"

	msgRepo.EXPECT().SetClicked(ctx, ws, msgID, gomock.Any()).Return(nil)
	// Lookup contact_email échoue → on émet quand même (sans clé de jointure), best-effort.
	msgRepo.EXPECT().
		FindContactEmailByMessageID(ctx, ws, msgID).
		Return("", false, errors.New("db down"))
	emitter.EXPECT().
		Emit(ctx, domain.EventEmailClicked, ws, gomock.Any()).
		DoAndReturn(func(_ context.Context, _ domain.VeridianEvent, _ string, data map[string]interface{}) {
			_, hasEmail := data["contact_email"]
			assert.False(t, hasEmail, "pas de contact_email quand le lookup échoue")
			assert.Equal(t, msgID, data["message_id"])
		})

	require.NoError(t, svc.VisitLink(ctx, msgID, ws))
}

func TestEmailService_EmitBehavioral_ClickWithLinkURL(t *testing.T) {
	// Le helper accepte des champs extra (ex. link_url) — vérifie la propagation.
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	svc, msgRepo := newBehavioralEmailService(ctrl)
	emitter := mocks.NewMockWebhookEmitter(ctrl)
	svc.SetVeridianWebhookEmitter(emitter)

	ctx := context.Background()
	msgRepo.EXPECT().
		FindContactEmailByMessageID(ctx, "ws", "m").
		Return("x@y.fr", true, nil)
	emitter.EXPECT().
		Emit(ctx, domain.EventEmailClicked, "ws", gomock.Any()).
		DoAndReturn(func(_ context.Context, _ domain.VeridianEvent, _ string, data map[string]interface{}) {
			assert.Equal(t, "https://dest.example/audit", data["link_url"])
		})

	svc.veridianEmitBehavioral(ctx, domain.EventEmailClicked, "ws", "m", map[string]interface{}{
		"link_url": "https://dest.example/audit",
	})
	_ = time.Now
}
