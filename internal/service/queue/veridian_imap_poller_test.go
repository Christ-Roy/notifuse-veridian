package queue

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/Notifuse/notifuse/internal/domain"
	"github.com/Notifuse/notifuse/internal/domain/mocks"
	pkgmocks "github.com/Notifuse/notifuse/pkg/mocks"
	"github.com/golang/mock/gomock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// --- Fakes for the IMAP client/dialer abstraction ---

type fakeIMAPClient struct {
	uidValidity uint32
	messages    []*domain.VeridianIMAPMessage
	fetchErr    error
	closed      bool
}

func (c *fakeIMAPClient) UIDValidity() uint32 { return c.uidValidity }
func (c *fakeIMAPClient) FetchSince(ctx context.Context, since time.Time, limit int) ([]*domain.VeridianIMAPMessage, error) {
	if c.fetchErr != nil {
		return nil, c.fetchErr
	}
	return c.messages, nil
}
func (c *fakeIMAPClient) Close() error { c.closed = true; return nil }

type fakeIMAPDialer struct {
	client       *fakeIMAPClient
	dialErr      error
	dialCount    int
	lastSettings *domain.IMAPSettings
}

func (d *fakeIMAPDialer) Dial(ctx context.Context, settings *domain.IMAPSettings) (veridianIMAPClient, error) {
	d.dialCount++
	d.lastSettings = settings
	if d.dialErr != nil {
		return nil, d.dialErr
	}
	return d.client, nil
}

// recordingConsumer records every message it receives.
type recordingConsumer struct {
	name      string
	mu        sync.Mutex
	received  []*domain.VeridianIMAPMessage
	returnErr error
	panicNow  bool
}

func (c *recordingConsumer) Name() string { return c.name }
func (c *recordingConsumer) OnNewMessage(msg *domain.VeridianIMAPMessage) error {
	c.mu.Lock()
	c.received = append(c.received, msg)
	c.mu.Unlock()
	if c.panicNow {
		panic("boom")
	}
	return c.returnErr
}
func (c *recordingConsumer) count() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.received)
}

func newTestLogger(ctrl *gomock.Controller) *pkgmocks.MockLogger {
	log := pkgmocks.NewMockLogger(ctrl)
	log.EXPECT().WithFields(gomock.Any()).Return(log).AnyTimes()
	log.EXPECT().WithField(gomock.Any(), gomock.Any()).Return(log).AnyTimes()
	log.EXPECT().Debug(gomock.Any()).AnyTimes()
	log.EXPECT().Info(gomock.Any()).AnyTimes()
	log.EXPECT().Warn(gomock.Any()).AnyTimes()
	log.EXPECT().Error(gomock.Any()).AnyTimes()
	return log
}

func imapIntegrationWorkspace(wsID, integrationID string, settings *domain.IMAPSettings) *domain.Workspace {
	return &domain.Workspace{
		ID: wsID,
		Integrations: domain.Integrations{
			{ID: integrationID, Name: "Bounce box", Type: domain.IntegrationTypeIMAP, IMAPSettings: settings},
		},
	}
}

func validIMAPSettings() *domain.IMAPSettings {
	return &domain.IMAPSettings{
		Host: "imap.example.com", Port: 993, Username: "u",
		Password: "decrypted-pass", UseTLS: true, Folder: "INBOX",
	}
}

func TestVeridianIMAPPoller_RegisterConsumer(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	log := newTestLogger(ctrl)

	p := NewVeridianIMAPPollerService(
		mocks.NewMockWorkspaceRepository(ctrl),
		mocks.NewMockVeridianIMAPUIDSeenRepository(ctrl),
		log, 0, 0,
	)

	c1 := &recordingConsumer{name: "bounce-loop"}
	c2 := &recordingConsumer{name: "stop-on-reply"}
	p.RegisterConsumer(c1)
	p.RegisterConsumer(c2)
	p.RegisterConsumer(nil) // ignored

	assert.Len(t, p.snapshotConsumers(), 2)

	// Re-registering same Name replaces, not appends.
	c1bis := &recordingConsumer{name: "bounce-loop"}
	p.RegisterConsumer(c1bis)
	assert.Len(t, p.snapshotConsumers(), 2)
}

func TestVeridianIMAPPoller_DispatchesNewMessages(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	log := newTestLogger(ctrl)

	wsRepo := mocks.NewMockWorkspaceRepository(ctrl)
	uidRepo := mocks.NewMockVeridianIMAPUIDSeenRepository(ctrl)

	settings := validIMAPSettings()
	ws := imapIntegrationWorkspace("ws1", "int1", settings)
	wsRepo.EXPECT().List(gomock.Any()).Return([]*domain.Workspace{ws}, nil)

	msgs := []*domain.VeridianIMAPMessage{
		{UID: 10, Subject: "bounce 1"},
		{UID: 11, Subject: "reply 1"},
	}
	dialer := &fakeIMAPDialer{client: &fakeIMAPClient{uidValidity: 42, messages: msgs}}

	// All UIDs are unseen → both dispatched, then both marked seen.
	uidRepo.EXPECT().
		FilterUnseen(gomock.Any(), "ws1", "int1", "INBOX", uint32(42), []uint32{10, 11}).
		Return([]uint32{10, 11}, nil)
	uidRepo.EXPECT().
		MarkSeen(gomock.Any(), "ws1", "int1", "INBOX", uint32(42), []uint32{10, 11}).
		Return(nil)

	p := NewVeridianIMAPPollerService(wsRepo, uidRepo, log, 0, 0)
	p.dialer = dialer

	consumer := &recordingConsumer{name: "bounce-loop"}
	p.RegisterConsumer(consumer)

	p.runOnce(context.Background())

	require.Equal(t, 2, consumer.count())
	// Context (workspace/integration) is enriched on dispatch.
	assert.Equal(t, "ws1", consumer.received[0].WorkspaceID)
	assert.Equal(t, "int1", consumer.received[0].IntegrationID)
	assert.True(t, dialer.client.closed, "client must be closed after poll")
}

func TestVeridianIMAPPoller_IdempotentSkipsSeenUIDs(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	log := newTestLogger(ctrl)

	wsRepo := mocks.NewMockWorkspaceRepository(ctrl)
	uidRepo := mocks.NewMockVeridianIMAPUIDSeenRepository(ctrl)

	ws := imapIntegrationWorkspace("ws1", "int1", validIMAPSettings())
	wsRepo.EXPECT().List(gomock.Any()).Return([]*domain.Workspace{ws}, nil)

	msgs := []*domain.VeridianIMAPMessage{
		{UID: 10}, {UID: 11}, {UID: 12},
	}
	dialer := &fakeIMAPDialer{client: &fakeIMAPClient{uidValidity: 7, messages: msgs}}

	// UID 11 already seen → only 10 and 12 are dispatched & marked.
	uidRepo.EXPECT().
		FilterUnseen(gomock.Any(), "ws1", "int1", "INBOX", uint32(7), []uint32{10, 11, 12}).
		Return([]uint32{10, 12}, nil)
	uidRepo.EXPECT().
		MarkSeen(gomock.Any(), "ws1", "int1", "INBOX", uint32(7), []uint32{10, 12}).
		Return(nil)

	p := NewVeridianIMAPPollerService(wsRepo, uidRepo, log, 0, 0)
	p.dialer = dialer
	consumer := &recordingConsumer{name: "c"}
	p.RegisterConsumer(consumer)

	p.runOnce(context.Background())

	require.Equal(t, 2, consumer.count())
	got := []uint32{consumer.received[0].UID, consumer.received[1].UID}
	assert.ElementsMatch(t, []uint32{10, 12}, got)
}

func TestVeridianIMAPPoller_NoUnseenSkipsMarkSeen(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	log := newTestLogger(ctrl)

	wsRepo := mocks.NewMockWorkspaceRepository(ctrl)
	uidRepo := mocks.NewMockVeridianIMAPUIDSeenRepository(ctrl)

	ws := imapIntegrationWorkspace("ws1", "int1", validIMAPSettings())
	wsRepo.EXPECT().List(gomock.Any()).Return([]*domain.Workspace{ws}, nil)

	dialer := &fakeIMAPDialer{client: &fakeIMAPClient{uidValidity: 1, messages: []*domain.VeridianIMAPMessage{{UID: 10}}}}
	uidRepo.EXPECT().FilterUnseen(gomock.Any(), "ws1", "int1", "INBOX", uint32(1), []uint32{10}).Return([]uint32{}, nil)
	// MarkSeen must NOT be called when nothing is unseen.

	p := NewVeridianIMAPPollerService(wsRepo, uidRepo, log, 0, 0)
	p.dialer = dialer
	consumer := &recordingConsumer{name: "c"}
	p.RegisterConsumer(consumer)

	p.runOnce(context.Background())
	assert.Equal(t, 0, consumer.count())
}

func TestVeridianIMAPPoller_DialFailureIsBestEffort(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	log := newTestLogger(ctrl)

	wsRepo := mocks.NewMockWorkspaceRepository(ctrl)
	uidRepo := mocks.NewMockVeridianIMAPUIDSeenRepository(ctrl)

	ws := imapIntegrationWorkspace("ws1", "int1", validIMAPSettings())
	wsRepo.EXPECT().List(gomock.Any()).Return([]*domain.Workspace{ws}, nil)

	dialer := &fakeIMAPDialer{dialErr: errors.New("connection refused")}
	// No repo calls expected — dial failed before any fetch.

	p := NewVeridianIMAPPollerService(wsRepo, uidRepo, log, 0, 0)
	p.dialer = dialer
	consumer := &recordingConsumer{name: "c"}
	p.RegisterConsumer(consumer)

	// Must not panic, must not call consumer.
	assert.NotPanics(t, func() { p.runOnce(context.Background()) })
	assert.Equal(t, 0, consumer.count())
}

func TestVeridianIMAPPoller_FetchFailureIsBestEffort(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	log := newTestLogger(ctrl)

	wsRepo := mocks.NewMockWorkspaceRepository(ctrl)
	uidRepo := mocks.NewMockVeridianIMAPUIDSeenRepository(ctrl)

	ws := imapIntegrationWorkspace("ws1", "int1", validIMAPSettings())
	wsRepo.EXPECT().List(gomock.Any()).Return([]*domain.Workspace{ws}, nil)

	client := &fakeIMAPClient{uidValidity: 1, fetchErr: errors.New("fetch boom")}
	dialer := &fakeIMAPDialer{client: client}

	p := NewVeridianIMAPPollerService(wsRepo, uidRepo, log, 0, 0)
	p.dialer = dialer
	consumer := &recordingConsumer{name: "c"}
	p.RegisterConsumer(consumer)

	assert.NotPanics(t, func() { p.runOnce(context.Background()) })
	assert.Equal(t, 0, consumer.count())
	assert.True(t, client.closed, "client closed even on fetch error")
}

func TestVeridianIMAPPoller_FilterUnseenErrorSkipsBox(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	log := newTestLogger(ctrl)

	wsRepo := mocks.NewMockWorkspaceRepository(ctrl)
	uidRepo := mocks.NewMockVeridianIMAPUIDSeenRepository(ctrl)

	ws := imapIntegrationWorkspace("ws1", "int1", validIMAPSettings())
	wsRepo.EXPECT().List(gomock.Any()).Return([]*domain.Workspace{ws}, nil)

	dialer := &fakeIMAPDialer{client: &fakeIMAPClient{uidValidity: 1, messages: []*domain.VeridianIMAPMessage{{UID: 5}}}}
	uidRepo.EXPECT().FilterUnseen(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).
		Return(nil, errors.New("db down"))
	// MarkSeen must NOT be called when filter fails (skip to avoid double-dispatch).

	p := NewVeridianIMAPPollerService(wsRepo, uidRepo, log, 0, 0)
	p.dialer = dialer
	consumer := &recordingConsumer{name: "c"}
	p.RegisterConsumer(consumer)

	p.runOnce(context.Background())
	assert.Equal(t, 0, consumer.count())
}

func TestVeridianIMAPPoller_ConsumerPanicIsolated(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	log := newTestLogger(ctrl)

	wsRepo := mocks.NewMockWorkspaceRepository(ctrl)
	uidRepo := mocks.NewMockVeridianIMAPUIDSeenRepository(ctrl)

	ws := imapIntegrationWorkspace("ws1", "int1", validIMAPSettings())
	wsRepo.EXPECT().List(gomock.Any()).Return([]*domain.Workspace{ws}, nil)

	dialer := &fakeIMAPDialer{client: &fakeIMAPClient{uidValidity: 1, messages: []*domain.VeridianIMAPMessage{{UID: 5}}}}
	uidRepo.EXPECT().FilterUnseen(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Return([]uint32{5}, nil)
	// The panic is isolated so other consumers still run, but the UID remains
	// unseen because one required consumer did not complete.

	p := NewVeridianIMAPPollerService(wsRepo, uidRepo, log, 0, 0)
	p.dialer = dialer
	panicker := &recordingConsumer{name: "bad", panicNow: true}
	good := &recordingConsumer{name: "good"}
	p.RegisterConsumer(panicker)
	p.RegisterConsumer(good)

	assert.NotPanics(t, func() { p.runOnce(context.Background()) })
	// The good consumer still received the message despite the panicker.
	assert.Equal(t, 1, good.count())
}

func TestVeridianIMAPPoller_ConsumerErrorLeavesUIDUnseen(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	log := newTestLogger(ctrl)

	wsRepo := mocks.NewMockWorkspaceRepository(ctrl)
	uidRepo := mocks.NewMockVeridianIMAPUIDSeenRepository(ctrl)

	ws := imapIntegrationWorkspace("ws1", "int1", validIMAPSettings())
	wsRepo.EXPECT().List(gomock.Any()).Return([]*domain.Workspace{ws}, nil)

	dialer := &fakeIMAPDialer{client: &fakeIMAPClient{uidValidity: 1, messages: []*domain.VeridianIMAPMessage{{UID: 9}}}}
	uidRepo.EXPECT().FilterUnseen(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Return([]uint32{9}, nil)
	// MarkSeen must not be called: the durable business write failed and the UID
	// must be retried on the next poll.

	p := NewVeridianIMAPPollerService(wsRepo, uidRepo, log, 0, 0)
	p.dialer = dialer
	consumer := &recordingConsumer{name: "c", returnErr: errors.New("metier error")}
	p.RegisterConsumer(consumer)

	p.runOnce(context.Background())
	assert.Equal(t, 1, consumer.count(), "message dispatched")
}

func TestVeridianIMAPPoller_SkipsNonIMAPIntegrations(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	log := newTestLogger(ctrl)

	wsRepo := mocks.NewMockWorkspaceRepository(ctrl)
	uidRepo := mocks.NewMockVeridianIMAPUIDSeenRepository(ctrl)

	ws := &domain.Workspace{
		ID: "ws1",
		Integrations: domain.Integrations{
			{ID: "email1", Name: "SMTP", Type: domain.IntegrationTypeEmail},
			{ID: "imapNoSettings", Name: "broken", Type: domain.IntegrationTypeIMAP, IMAPSettings: nil},
		},
	}
	wsRepo.EXPECT().List(gomock.Any()).Return([]*domain.Workspace{ws}, nil)

	dialer := &fakeIMAPDialer{client: &fakeIMAPClient{}}
	p := NewVeridianIMAPPollerService(wsRepo, uidRepo, log, 0, 0)
	p.dialer = dialer

	p.runOnce(context.Background())
	assert.Equal(t, 0, dialer.dialCount, "no dial for email integration or imap-without-settings")
}

func TestVeridianIMAPPoller_ListErrorIsBestEffort(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	log := newTestLogger(ctrl)

	wsRepo := mocks.NewMockWorkspaceRepository(ctrl)
	uidRepo := mocks.NewMockVeridianIMAPUIDSeenRepository(ctrl)
	wsRepo.EXPECT().List(gomock.Any()).Return(nil, errors.New("list down"))

	p := NewVeridianIMAPPollerService(wsRepo, uidRepo, log, 0, 0)
	assert.NotPanics(t, func() { p.runOnce(context.Background()) })
}

func TestVeridianIMAPPoller_DueForPoll(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	log := newTestLogger(ctrl)

	p := NewVeridianIMAPPollerService(
		mocks.NewMockWorkspaceRepository(ctrl),
		mocks.NewMockVeridianIMAPUIDSeenRepository(ctrl),
		log, 0, 0,
	)
	settings := &domain.IMAPSettings{PollingIntervalSeconds: 120}
	now := time.Now().UTC()

	// Never polled → due.
	assert.True(t, p.dueForPoll("ws1", "int1", settings, now))

	// Just polled → not due before interval.
	p.setLastPolled("ws1", "int1", now)
	assert.False(t, p.dueForPoll("ws1", "int1", settings, now.Add(60*time.Second)))

	// After interval elapsed → due again.
	assert.True(t, p.dueForPoll("ws1", "int1", settings, now.Add(121*time.Second)))
}

func TestVeridianIMAPPoller_StartNoopWhenDepsNil(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	log := newTestLogger(ctrl)

	// Nil uidSeenRepo → Start is a no-op (no goroutine work, no panic).
	p := NewVeridianIMAPPollerService(nil, nil, log, 0, 0)
	assert.NotPanics(t, func() { p.Start(context.Background()) })
}

func TestVeridianIMAPPoller_StartNoopWhenNoConsumer(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	log := newTestLogger(ctrl)

	// Deps present but NO consumer registered → Start must not launch a polling
	// goroutine (no List call, ever). We assert the workspace repo is never hit:
	// the mock has no EXPECT, so any call would fail the test.
	wsRepo := mocks.NewMockWorkspaceRepository(ctrl)
	uidRepo := mocks.NewMockVeridianIMAPUIDSeenRepository(ctrl)

	p := NewVeridianIMAPPollerService(wsRepo, uidRepo, log, time.Millisecond, 0)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	assert.NotPanics(t, func() { p.Start(ctx) })
	// Give a hypothetical goroutine time to (wrongly) fire; none should exist.
	time.Sleep(20 * time.Millisecond)
}

func TestNewVeridianIMAPPollerService_Defaults(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	log := newTestLogger(ctrl)

	p := NewVeridianIMAPPollerService(
		mocks.NewMockWorkspaceRepository(ctrl),
		mocks.NewMockVeridianIMAPUIDSeenRepository(ctrl),
		log, 0, 0,
	)
	assert.Equal(t, defaultIMAPTickInterval, p.tickInterval)
	assert.Equal(t, defaultIMAPLookbackWindow, p.lookbackWindow)
	assert.NotNil(t, p.dialer)
}
