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

func TestVeridianReplyForwardTaskProcessor_CanProcessOnlyItsType(t *testing.T) {
	p := &VeridianReplyForwardTaskProcessor{}
	assert.True(t, p.CanProcess("veridian_reply_forward_retry"))
	assert.False(t, p.CanProcess("send_broadcast"))
	assert.Contains(t, getTaskTypes(), VeridianReplyForwardTaskType, "le service de taches doit connaitre le type")
}

func TestVeridianReplyForwardTaskProcessor_ProcessReplaysDueCopiesAndStaysRecurring(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	f, repo, mailer := newForwarderForTest(t, forwardWorkspace("client@client.fr"))
	mailer.failures = 1
	msg, det := humanMsg(humanRaw)
	require.Equal(t, ReplyForwardRetrying, f.Forward(context.Background(), "ws1", msg, det))

	f.now = func() time.Time { return time.Date(2026, 11, 4, 10, 2, 0, 0, time.UTC) }
	p := NewVeridianReplyForwardTaskProcessor(f, setupMockLogger(ctrl))
	done, err := p.Process(context.Background(), &domain.Task{ID: "t1", WorkspaceID: "ws1", Type: VeridianReplyForwardTaskType}, time.Now().Add(time.Minute))
	require.NoError(t, err)
	assert.True(t, done, "tache recurrente : toujours terminee, replanifiee par le service de taches")
	assert.Len(t, mailer.sent, 1)
	assert.Equal(t, domain.VeridianReplyForwardStatusSent, repo.only(t).rec.Status)
}

func TestVeridianReplyForwardTaskProcessor_ErrorNeverKillsTheRecurringTask(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	f, _, _ := newForwarderForTest(t, forwardWorkspace("client@client.fr"))
	f.workspaces = &fakeForwardWorkspaces{err: errors.New("db down")}
	p := NewVeridianReplyForwardTaskProcessor(f, setupMockLogger(ctrl))
	done, err := p.Process(context.Background(), &domain.Task{ID: "t1", WorkspaceID: "ws1"}, time.Now().Add(time.Minute))
	assert.NoError(t, err, "une erreur rendue marquerait la tache failed et le rejeu s'arreterait en silence")
	assert.True(t, done)
}

func TestVeridianReplyForwardTaskEnsurer_CreatesRecurringTaskWhenMissing(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	repo := mocks.NewMockTaskRepository(ctrl)
	repo.EXPECT().List(gomock.Any(), "ws1", gomock.Any()).DoAndReturn(func(_ context.Context, _ string, f domain.TaskFilter) ([]*domain.Task, int, error) {
		assert.Equal(t, []string{"veridian_reply_forward_retry"}, f.Type)
		return nil, 0, nil
	})
	repo.EXPECT().Create(gomock.Any(), "ws1", gomock.Any()).DoAndReturn(func(_ context.Context, _ string, task *domain.Task) error {
		assert.Equal(t, VeridianReplyForwardTaskType, task.Type)
		assert.Equal(t, domain.TaskStatusPending, task.Status)
		require.NotNil(t, task.RecurringInterval)
		assert.Equal(t, int64(60), *task.RecurringInterval)
		assert.True(t, task.IsRecurring())
		return nil
	})
	require.NoError(t, NewVeridianReplyForwardTaskEnsurer(repo).EnsureReplyForwardTask(context.Background(), "ws1"))
}

func TestVeridianReplyForwardTaskEnsurer_HealthyTaskIsLeftAlone(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	repo := mocks.NewMockTaskRepository(ctrl)
	next := time.Now().Add(30 * time.Second)
	repo.EXPECT().List(gomock.Any(), "ws1", gomock.Any()).Return([]*domain.Task{{ID: "t1", Status: domain.TaskStatusPending, NextRunAfter: &next}}, 1, nil)
	// ni Create ni Update : pas de doublon, pas de bousculade
	require.NoError(t, NewVeridianReplyForwardTaskEnsurer(repo).EnsureReplyForwardTask(context.Background(), "ws1"))
}

func TestVeridianReplyForwardTaskEnsurer_FailedTaskIsRestarted(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	repo := mocks.NewMockTaskRepository(ctrl)
	repo.EXPECT().List(gomock.Any(), "ws1", gomock.Any()).Return([]*domain.Task{{ID: "t1", Status: domain.TaskStatusFailed}}, 1, nil)
	repo.EXPECT().Update(gomock.Any(), "ws1", gomock.Any()).DoAndReturn(func(_ context.Context, _ string, task *domain.Task) error {
		assert.Equal(t, domain.TaskStatusPending, task.Status)
		require.NotNil(t, task.NextRunAfter)
		assert.WithinDuration(t, time.Now(), *task.NextRunAfter, 5*time.Second)
		return nil
	})
	require.NoError(t, NewVeridianReplyForwardTaskEnsurer(repo).EnsureReplyForwardTask(context.Background(), "ws1"))
}

func TestVeridianReplyForwardTaskEnsurer_ListErrorIsReturned(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	repo := mocks.NewMockTaskRepository(ctrl)
	repo.EXPECT().List(gomock.Any(), "ws1", gomock.Any()).Return(nil, 0, errors.New("db down"))
	assert.Error(t, NewVeridianReplyForwardTaskEnsurer(repo).EnsureReplyForwardTask(context.Background(), "ws1"))
}
