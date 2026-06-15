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
	"time"

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

// Les methodes ci-dessous sont des passthrough trivial vers l'upstream.
// On verifie pour chaque : (1) l'upstream est appele avec les memes args,
// (2) le retour upstream est propage, (3) planRepo n'est PAS sollicite (ce
// ne sont pas des points d'increment quota).

func TestVeridianMessageHistoryDecorator_Update_Passthrough(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	upstream := domainmocks.NewMockMessageHistoryRepository(ctrl)
	planRepo := domainmocks.NewMockVeridianPlanRepository(ctrl)
	d := NewVeridianMessageHistoryDecorator(upstream, planRepo, pkgmocks.NewMockLogger(ctrl))

	msg := &domain.MessageHistory{ID: "msg-u"}
	upstream.EXPECT().Update(gomock.Any(), "ws", msg).Return(errors.New("up err")).Times(1)
	err := d.Update(context.Background(), "ws", msg)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "up err")
}

func TestVeridianMessageHistoryDecorator_Get_Passthrough(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	upstream := domainmocks.NewMockMessageHistoryRepository(ctrl)
	d := NewVeridianMessageHistoryDecorator(upstream, nil, nil)

	expected := &domain.MessageHistory{ID: "msg-g"}
	upstream.EXPECT().Get(gomock.Any(), "ws", "sec", "msg-g").Return(expected, nil).Times(1)
	got, err := d.Get(context.Background(), "ws", "sec", "msg-g")
	require.NoError(t, err)
	assert.Equal(t, expected, got)
}

func TestVeridianMessageHistoryDecorator_GetByExternalID_Passthrough(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	upstream := domainmocks.NewMockMessageHistoryRepository(ctrl)
	d := NewVeridianMessageHistoryDecorator(upstream, nil, nil)

	expected := &domain.MessageHistory{ID: "msg-ext"}
	upstream.EXPECT().GetByExternalID(gomock.Any(), "ws", "sec", "ext-1").Return(expected, nil).Times(1)
	got, err := d.GetByExternalID(context.Background(), "ws", "sec", "ext-1")
	require.NoError(t, err)
	assert.Equal(t, expected, got)
}

func TestVeridianMessageHistoryDecorator_GetByContact_Passthrough(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	upstream := domainmocks.NewMockMessageHistoryRepository(ctrl)
	d := NewVeridianMessageHistoryDecorator(upstream, nil, nil)

	msgs := []*domain.MessageHistory{{ID: "1"}, {ID: "2"}}
	upstream.EXPECT().GetByContact(gomock.Any(), "ws", "sec", "foo@bar", 10, 0).
		Return(msgs, 2, nil).Times(1)
	got, total, err := d.GetByContact(context.Background(), "ws", "sec", "foo@bar", 10, 0)
	require.NoError(t, err)
	assert.Len(t, got, 2)
	assert.Equal(t, 2, total)
}

func TestVeridianMessageHistoryDecorator_GetByBroadcast_Passthrough(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	upstream := domainmocks.NewMockMessageHistoryRepository(ctrl)
	d := NewVeridianMessageHistoryDecorator(upstream, nil, nil)

	upstream.EXPECT().GetByBroadcast(gomock.Any(), "ws", "sec", "b-1", 10, 0).
		Return(nil, 0, nil).Times(1)
	_, _, err := d.GetByBroadcast(context.Background(), "ws", "sec", "b-1", 10, 0)
	require.NoError(t, err)
}

func TestVeridianMessageHistoryDecorator_ListMessages_Passthrough(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	upstream := domainmocks.NewMockMessageHistoryRepository(ctrl)
	d := NewVeridianMessageHistoryDecorator(upstream, nil, nil)

	params := domain.MessageListParams{}
	upstream.EXPECT().ListMessages(gomock.Any(), "ws", "sec", params).
		Return(nil, "cursor-next", nil).Times(1)
	_, cursor, err := d.ListMessages(context.Background(), "ws", "sec", params)
	require.NoError(t, err)
	assert.Equal(t, "cursor-next", cursor)
}

func TestVeridianMessageHistoryDecorator_SetStatusesIfNotSet_Passthrough(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	upstream := domainmocks.NewMockMessageHistoryRepository(ctrl)
	d := NewVeridianMessageHistoryDecorator(upstream, nil, nil)

	updates := []domain.MessageEventUpdate{{ID: "1", Event: domain.MessageEventOpened}}
	upstream.EXPECT().SetStatusesIfNotSet(gomock.Any(), "ws", updates).Return(nil).Times(1)
	require.NoError(t, d.SetStatusesIfNotSet(context.Background(), "ws", updates))
}

func TestVeridianMessageHistoryDecorator_SetClicked_Passthrough(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	upstream := domainmocks.NewMockMessageHistoryRepository(ctrl)
	d := NewVeridianMessageHistoryDecorator(upstream, nil, nil)

	ts := time.Now()
	upstream.EXPECT().SetClicked(gomock.Any(), "ws", "id-c", ts).Return(nil).Times(1)
	require.NoError(t, d.SetClicked(context.Background(), "ws", "id-c", ts))
}

func TestVeridianMessageHistoryDecorator_SetOpened_Passthrough(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	upstream := domainmocks.NewMockMessageHistoryRepository(ctrl)
	d := NewVeridianMessageHistoryDecorator(upstream, nil, nil)

	ts := time.Now()
	upstream.EXPECT().SetOpened(gomock.Any(), "ws", "id-o", ts).Return(nil).Times(1)
	require.NoError(t, d.SetOpened(context.Background(), "ws", "id-o", ts))
}

func TestVeridianMessageHistoryDecorator_GetBroadcastStats_Passthrough(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	upstream := domainmocks.NewMockMessageHistoryRepository(ctrl)
	d := NewVeridianMessageHistoryDecorator(upstream, nil, nil)

	expected := &domain.MessageHistoryStatusSum{TotalSent: 42}
	upstream.EXPECT().GetBroadcastStats(gomock.Any(), "ws", "b-1").Return(expected, nil).Times(1)
	got, err := d.GetBroadcastStats(context.Background(), "ws", "b-1")
	require.NoError(t, err)
	assert.Equal(t, 42, got.TotalSent)
}

func TestVeridianMessageHistoryDecorator_GetBroadcastVariationStats_Passthrough(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	upstream := domainmocks.NewMockMessageHistoryRepository(ctrl)
	d := NewVeridianMessageHistoryDecorator(upstream, nil, nil)

	upstream.EXPECT().GetBroadcastVariationStats(gomock.Any(), "ws", "b-1", "t-A").
		Return(&domain.MessageHistoryStatusSum{TotalOpened: 7}, nil).Times(1)
	got, err := d.GetBroadcastVariationStats(context.Background(), "ws", "b-1", "t-A")
	require.NoError(t, err)
	assert.Equal(t, 7, got.TotalOpened)
}

func TestVeridianMessageHistoryDecorator_DeleteForEmail_Passthrough(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	upstream := domainmocks.NewMockMessageHistoryRepository(ctrl)
	d := NewVeridianMessageHistoryDecorator(upstream, nil, nil)

	upstream.EXPECT().DeleteForEmail(gomock.Any(), "ws", "foo@bar").Return(nil).Times(1)
	require.NoError(t, d.DeleteForEmail(context.Background(), "ws", "foo@bar"))
}

func TestVeridianMessageHistoryDecorator_CountSentSinceForContact_Passthrough(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	upstream := domainmocks.NewMockMessageHistoryRepository(ctrl)
	d := NewVeridianMessageHistoryDecorator(upstream, nil, nil)

	since := time.Now()
	upstream.EXPECT().CountSentSinceForContact(gomock.Any(), "ws", "foo@bar", since).
		Return(5, nil).Times(1)
	got, err := d.CountSentSinceForContact(context.Background(), "ws", "foo@bar", since)
	require.NoError(t, err)
	assert.Equal(t, 5, got)
}

func TestVeridianMessageHistoryDecorator_CountSentSinceForDomains_Passthrough(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	upstream := domainmocks.NewMockMessageHistoryRepository(ctrl)
	d := NewVeridianMessageHistoryDecorator(upstream, nil, nil)

	since := time.Now()
	domains := []string{"gmail.com"}
	upstream.EXPECT().CountSentSinceForDomains(gomock.Any(), "ws", domains, true, since).
		Return(11, nil).Times(1)
	got, err := d.CountSentSinceForDomains(context.Background(), "ws", domains, true, since)
	require.NoError(t, err)
	assert.Equal(t, 11, got)
}

func TestVeridianMessageHistoryDecorator_FindContactEmailByMessageID_Passthrough(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	upstream := domainmocks.NewMockMessageHistoryRepository(ctrl)
	d := NewVeridianMessageHistoryDecorator(upstream, nil, nil)

	upstream.EXPECT().FindContactEmailByMessageID(gomock.Any(), "ws", "msg-1").
		Return("prospect@acme.fr", true, nil).Times(1)
	email, found, err := d.FindContactEmailByMessageID(context.Background(), "ws", "msg-1")
	require.NoError(t, err)
	assert.True(t, found)
	assert.Equal(t, "prospect@acme.fr", email)
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
