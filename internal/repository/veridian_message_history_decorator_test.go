package repository

// === Veridian patch === Tests colocalises pour
// veridian_message_history_decorator.go.
//
// Couverture :
//   - Create OK → IncrementEmailsSent appele 1x avec delta=1
//   - Create fail upstream → IncrementEmailsSent jamais appele
//   - Create OK + IncrementEmailsSent fail (not found) → Debug log, retour nil
//   - Create OK + IncrementEmailsSent fail (autre erreur) → Warn log, retour nil
//   - Upsert OK → IncrementEmailsSent JAMAIS appele (retry handling, cf. design note)
//   - planRepo nil → decorator est passthrough sans crash

import (
	"context"
	"errors"
	"testing"

	"github.com/Notifuse/notifuse/internal/domain"
	domainmocks "github.com/Notifuse/notifuse/internal/domain/mocks"
	pkgmocks "github.com/Notifuse/notifuse/pkg/mocks"
	"github.com/golang/mock/gomock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestVeridianMessageHistoryDecorator_Create_IncrementsQuota(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	upstream := domainmocks.NewMockMessageHistoryRepository(ctrl)
	planRepo := domainmocks.NewMockVeridianPlanRepository(ctrl)
	log := pkgmocks.NewMockLogger(ctrl)

	ctx := context.Background()
	msg := &domain.MessageHistory{ID: "msg-1", ContactEmail: "foo@bar.com"}

	upstream.EXPECT().Create(ctx, "ws-1", "secret", msg).Return(nil).Times(1)
	planRepo.EXPECT().IncrementEmailsSent(ctx, "ws-1", int64(1)).Return(nil).Times(1)

	d := NewVeridianMessageHistoryDecorator(upstream, planRepo, log)
	err := d.Create(ctx, "ws-1", "secret", msg)
	require.NoError(t, err)
}

func TestVeridianMessageHistoryDecorator_Create_UpstreamFail_NoIncrement(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	upstream := domainmocks.NewMockMessageHistoryRepository(ctrl)
	planRepo := domainmocks.NewMockVeridianPlanRepository(ctrl)
	log := pkgmocks.NewMockLogger(ctrl)

	ctx := context.Background()
	msg := &domain.MessageHistory{ID: "msg-2"}
	wantErr := errors.New("db connection lost")

	upstream.EXPECT().Create(ctx, "ws-2", "secret", msg).Return(wantErr).Times(1)
	// IncrementEmailsSent ne doit JAMAIS etre appele si Create echoue.
	// gomock fail le test si un appel non-expected arrive.

	d := NewVeridianMessageHistoryDecorator(upstream, planRepo, log)
	err := d.Create(ctx, "ws-2", "secret", msg)
	require.ErrorIs(t, err, wantErr)
}

func TestVeridianMessageHistoryDecorator_Create_IncrementNotFound_DebugLog(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	upstream := domainmocks.NewMockMessageHistoryRepository(ctrl)
	planRepo := domainmocks.NewMockVeridianPlanRepository(ctrl)
	log := pkgmocks.NewMockLogger(ctrl)
	withFieldsRecorder := pkgmocks.NewMockLogger(ctrl)

	ctx := context.Background()
	msg := &domain.MessageHistory{ID: "msg-3"}
	notFoundErr := errors.New("veridian_plan: workspace ws-3 not found")

	upstream.EXPECT().Create(ctx, "ws-3", "secret", msg).Return(nil).Times(1)
	planRepo.EXPECT().IncrementEmailsSent(ctx, "ws-3", int64(1)).Return(notFoundErr).Times(1)
	log.EXPECT().WithFields(gomock.Any()).Return(withFieldsRecorder).Times(1)
	withFieldsRecorder.EXPECT().Debug(gomock.Any()).Times(1)

	d := NewVeridianMessageHistoryDecorator(upstream, planRepo, log)
	err := d.Create(ctx, "ws-3", "secret", msg)
	// Create reussit meme si l'increment "not found" (workspace non-Veridian).
	require.NoError(t, err)
}

func TestVeridianMessageHistoryDecorator_Create_IncrementOtherError_WarnLog(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	upstream := domainmocks.NewMockMessageHistoryRepository(ctrl)
	planRepo := domainmocks.NewMockVeridianPlanRepository(ctrl)
	log := pkgmocks.NewMockLogger(ctrl)
	withFieldsRecorder := pkgmocks.NewMockLogger(ctrl)

	ctx := context.Background()
	msg := &domain.MessageHistory{ID: "msg-4"}
	dbErr := errors.New("transient db error")

	upstream.EXPECT().Create(ctx, "ws-4", "secret", msg).Return(nil).Times(1)
	planRepo.EXPECT().IncrementEmailsSent(ctx, "ws-4", int64(1)).Return(dbErr).Times(1)
	log.EXPECT().WithFields(gomock.Any()).Return(withFieldsRecorder).Times(1)
	withFieldsRecorder.EXPECT().Warn(gomock.Any()).Times(1)

	d := NewVeridianMessageHistoryDecorator(upstream, planRepo, log)
	err := d.Create(ctx, "ws-4", "secret", msg)
	// Create reussit (best-effort sur l'increment).
	require.NoError(t, err)
}

func TestVeridianMessageHistoryDecorator_Upsert_NoIncrement(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	upstream := domainmocks.NewMockMessageHistoryRepository(ctrl)
	planRepo := domainmocks.NewMockVeridianPlanRepository(ctrl)
	log := pkgmocks.NewMockLogger(ctrl)

	ctx := context.Background()
	msg := &domain.MessageHistory{ID: "msg-5"}

	upstream.EXPECT().Upsert(ctx, "ws-5", "secret", msg).Return(nil).Times(1)
	// Pas d'increment attendu sur Upsert (retry handling, design note).

	d := NewVeridianMessageHistoryDecorator(upstream, planRepo, log)
	err := d.Upsert(ctx, "ws-5", "secret", msg)
	require.NoError(t, err)
}

func TestVeridianMessageHistoryDecorator_Create_NilPlanRepo_Passthrough(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	upstream := domainmocks.NewMockMessageHistoryRepository(ctrl)
	log := pkgmocks.NewMockLogger(ctrl)

	ctx := context.Background()
	msg := &domain.MessageHistory{ID: "msg-6"}

	upstream.EXPECT().Create(ctx, "ws-6", "secret", msg).Return(nil).Times(1)
	// planRepo nil → pas d'appel increment ni de log → aucune EXPECT.

	d := NewVeridianMessageHistoryDecorator(upstream, nil, log)
	err := d.Create(ctx, "ws-6", "secret", msg)
	require.NoError(t, err)
}

func TestIsWorkspaceNotFoundErr(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want bool
	}{
		{"nil error", nil, false},
		{"workspace not found", errors.New("veridian_plan: workspace abc not found"), true},
		{"random db error", errors.New("connection refused"), false},
		{"partial match only", errors.New("workspace not found"), false},
		{"prefix only", errors.New("veridian_plan: workspace xyz"), false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, isWorkspaceNotFoundErr(tc.err))
		})
	}
}
