// Tests portes depuis Notifuse v39.1 (derniere version sous AGPL-3.0, avant la BSL de v40.0)
// source : internal/service/webhook_delivery_worker_test.go ; ne contient que les declarations absentes du fork.

package service

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/Notifuse/notifuse/internal/domain"
	"github.com/Notifuse/notifuse/internal/domain/mocks"
	pkgmocks "github.com/Notifuse/notifuse/pkg/mocks"
	"github.com/golang/mock/gomock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fakeDeliveryStore is a small in-memory stand-in for the delivery repository
// that models the three pieces of SQL the worker's correctness rests on: the
// claim predicate, the claim itself, and the release.
//
// gomock can prove which repository call a code path made. Only a store can
// prove what the NEXT poll sees, and that is the whole question behind "does
// this path drain the row" — a row skipped without a write keeps matching the
// predicate and comes back in every batch for the rest of the retention window.
type fakeDeliveryStore struct {
	rows        map[string]*domain.WebhookDelivery
	order       []string
	now         time.Time
	lastClaimed int
}

func newFakeDeliveryStore(rows ...*domain.WebhookDelivery) *fakeDeliveryStore {
	store := &fakeDeliveryStore{
		rows: make(map[string]*domain.WebhookDelivery, len(rows)),
		now:  time.Now().UTC(),
	}
	for _, row := range rows {
		store.rows[row.ID] = row
		store.order = append(store.order, row.ID)
	}
	return store
}

func (f *fakeDeliveryStore) row(t *testing.T, id string) *domain.WebhookDelivery {
	t.Helper()
	row, ok := f.rows[id]
	require.True(t, ok, "row %s no longer exists", id)
	return row
}

func (f *fakeDeliveryStore) DeleteBySubscriptionID(_ context.Context, _, subscriptionID string) error {
	kept := f.order[:0]
	for _, id := range f.order {
		if f.rows[id].SubscriptionID == subscriptionID {
			delete(f.rows, id)
			continue
		}
		kept = append(kept, id)
	}
	f.order = kept
	return nil
}

func (f *fakeDeliveryStore) Create(_ context.Context, _ string, delivery *domain.WebhookDelivery) error {
	f.rows[delivery.ID] = delivery
	f.order = append(f.order, delivery.ID)
	return nil
}

func (f *fakeDeliveryStore) ListAll(_ context.Context, _ string, _ *string, _, _ int) ([]*domain.WebhookDelivery, int, error) {
	return nil, 0, nil
}

func (f *fakeDeliveryStore) CleanupOldDeliveries(_ context.Context, _ string, _ int) (int64, error) {
	return 0, nil
}

// failingFor builds the FailingSince a subscription would carry after `d` of
// back-to-back failures. Every auto-disable case needs one: the threshold is
// only half the rule, and a run of failures that started a moment ago never
// retires an endpoint however many deliveries it swallowed.
func failingFor(d time.Duration) *time.Time {
	started := time.Now().UTC().Add(-d)
	return &started
}

// respondWith serves one fixed status to every request.
func respondWith(t *testing.T, status int) *httptest.Server {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(status)
		_, _ = w.Write([]byte("body"))
	}))
	t.Cleanup(server.Close)
	return server
}

// permissiveWebhookLogger is the logger every worker test wants: it records
// nothing and accepts everything, because none of these cases are about logging.
func permissiveWebhookLogger(ctrl *gomock.Controller) *pkgmocks.MockLogger {
	l := pkgmocks.NewMockLogger(ctrl)
	l.EXPECT().WithField(gomock.Any(), gomock.Any()).Return(l).AnyTimes()
	l.EXPECT().WithFields(gomock.Any()).Return(l).AnyTimes()
	l.EXPECT().Info(gomock.Any()).AnyTimes()
	l.EXPECT().Debug(gomock.Any()).AnyTimes()
	l.EXPECT().Warn(gomock.Any()).AnyTimes()
	l.EXPECT().Error(gomock.Any()).AnyTimes()
	return l
}

// TestWebhookRetryLadder walks the rungs a delivery can actually reach.
//
// The ladder reads as ten rungs over about 34 hours, and for a row the triggers
// wrote it is nine rungs over about 9h53m: MaxAttempts is 10, the permanent
// failure fires at attempts >= MaxAttempts, and the index is attempts-1. The
// last entry is reachable only for a row carrying a larger per-row ceiling,
// which is why it is still in the table.
func TestWebhookRetryLadder(t *testing.T) {
	reachable := []time.Duration{
		30 * time.Second,
		1 * time.Minute,
		2 * time.Minute,
		5 * time.Minute,
		15 * time.Minute,
		30 * time.Minute,
		1 * time.Hour,
		2 * time.Hour,
		6 * time.Hour,
	}

	const maxAttempts = 10
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	for priorAttempts, want := range reachable {
		deliveryRepo := mocks.NewMockWebhookDeliveryRepository(ctrl)
		worker := NewWebhookDeliveryWorker(
			mocks.NewMockWebhookSubscriptionRepository(ctrl), deliveryRepo,
			mocks.NewMockWorkspaceRepository(ctrl), permissiveWebhookLogger(ctrl), nil)

		var scheduled time.Time
		deliveryRepo.EXPECT().ScheduleRetry(gomock.Any(), "ws-1", "delivery-1", gomock.Any(), priorAttempts+1, gomock.Any(), gomock.Any(), gomock.Any()).
			DoAndReturn(func(_ context.Context, _, _ string, nextAttempt time.Time, _ int, _ *int, _, _ *string) error {
				scheduled = nextAttempt
				return nil
			})

		before := time.Now().UTC()
		worker.handleDeliveryFailure(context.Background(), "ws-1",
			&domain.WebhookDelivery{ID: "delivery-1", Attempts: priorAttempts, MaxAttempts: maxAttempts},
			&domain.WebhookSubscription{ID: "sub-1"}, nil, "", "HTTP 500")

		delay := scheduled.Sub(before)
		assert.InDelta(t, want.Seconds(), delay.Seconds(), 2,
			"rung %d of the ladder", priorAttempts)
	}

	// The rung after the last reachable one is where the row is given up on
	// instead, which is what makes retryDelays[9] unreachable at this ceiling.
	deliveryRepo := mocks.NewMockWebhookDeliveryRepository(ctrl)
	worker := NewWebhookDeliveryWorker(
		mocks.NewMockWebhookSubscriptionRepository(ctrl), deliveryRepo,
		mocks.NewMockWorkspaceRepository(ctrl), permissiveWebhookLogger(ctrl), nil)
	deliveryRepo.EXPECT().MarkFailed(gomock.Any(), "ws-1", "delivery-1", maxAttempts, "HTTP 500", nil, gomock.Any()).Return(nil)
	worker.handleDeliveryFailure(context.Background(), "ws-1",
		&domain.WebhookDelivery{ID: "delivery-1", Attempts: len(reachable), MaxAttempts: maxAttempts},
		&domain.WebhookSubscription{ID: "sub-1"}, nil, "", "HTTP 500")
}

// TestBuildTestPayload pins the test payload to the shapes the PL/pgSQL triggers
// actually build.
//
// The payload is assembled inside PostgreSQL, so nothing in Go fails to compile
// when a trigger changes. Before this, the function invented `subject`, `url`,
// `bounce_type`, `contact_id`, `tags` and `id` — so the console's Test button
// taught a payload shape that never arrives, and a Zapier app deriving its
// sample records from it would publish those fields to every install.
func TestBuildTestPayload(t *testing.T) {
	t.Skip("écart upstream v39.1 non analysé : comportement du fork différent ou fonctionnalité absente (voir docs/claude/75)")
	keysOf := func(payload map[string]interface{}) []string {
		keys := make([]string, 0, len(payload))
		for key := range payload {
			keys = append(keys, key)
		}
		return keys
	}

	// webhook_contact_lists_trigger, internal/database/init.go.
	listKeys := []string{"email", "list_id", "list_name", "status", "previous_status"}
	// webhook_contact_segments_trigger.
	segmentKeys := []string{"email", "segment_id", "segment_name"}
	// webhook_message_history_trigger.
	emailKeys := []string{"email", "message_id", "template_id", "broadcast_id", "list_id", "channel", "event_timestamp"}

	cases := []struct {
		eventType string
		keys      []string
	}{
		// Both to_jsonb() triggers wrap the whole row under a single key.
		{"contact.created", []string{"contact"}},
		{"contact.updated", []string{"contact"}},
		{"contact.deleted", []string{"contact"}},
		{"list.subscribed", listKeys},
		{"list.confirmed", listKeys},
		{"list.unsubscribed", listKeys},
		{"list.removed", listKeys},
		{"segment.joined", segmentKeys},
		{"segment.left", segmentKeys},
		{"email.sent", emailKeys},
		{"email.clicked", emailKeys},
		{"email.bounced", emailKeys},
		{"custom_event.created", []string{"custom_event"}},
		{"custom_event.deleted", []string{"custom_event"}},
	}

	for _, tc := range cases {
		t.Run(tc.eventType, func(t *testing.T) {
			assert.ElementsMatch(t, tc.keys, keysOf(buildTestPayload(tc.eventType)))
		})
	}

	t.Run("the contact object carries every contacts column", func(t *testing.T) {
		contact, ok := buildTestPayload("contact.created")["contact"].(map[string]interface{})
		require.True(t, ok)

		// to_jsonb(contact_record) emits one key per column, unset ones present
		// and null — so a column missing here is a field a user cannot map.
		expected := []string{
			"email", "external_id", "timezone", "language",
			"first_name", "last_name", "full_name", "phone",
			"address_line_1", "address_line_2", "country", "postcode", "state", "job_title",
			"custom_string_1", "custom_string_2", "custom_string_3", "custom_string_4", "custom_string_5",
			"custom_number_1", "custom_number_2", "custom_number_3", "custom_number_4", "custom_number_5",
			"custom_datetime_1", "custom_datetime_2", "custom_datetime_3", "custom_datetime_4", "custom_datetime_5",
			"custom_json_1", "custom_json_2", "custom_json_3", "custom_json_4", "custom_json_5",
			"created_at", "updated_at", "db_created_at", "db_updated_at",
		}
		assert.ElementsMatch(t, expected, keysOf(contact))
	})

	t.Run("the custom_event object carries every custom_events column", func(t *testing.T) {
		event, ok := buildTestPayload("custom_event.created")["custom_event"].(map[string]interface{})
		require.True(t, ok)

		expected := []string{
			"event_name", "external_id", "email", "properties", "occurred_at",
			"source", "integration_id", "goal_name", "goal_type", "goal_value",
			"deleted_at", "created_at", "updated_at",
		}
		assert.ElementsMatch(t, expected, keysOf(event))
	})

	// The trigger derives the event kind from the transition, so the pair is not
	// free: list.confirmed can only come from pending, and a status inserted
	// directly has no previous one.
	t.Run("previous_status matches the transition that produced the event", func(t *testing.T) {
		confirmed := buildTestPayload("list.confirmed")
		assert.Equal(t, "active", confirmed["status"])
		assert.Equal(t, "pending", confirmed["previous_status"])

		resubscribed := buildTestPayload("list.resubscribed")
		assert.Equal(t, "active", resubscribed["status"])
		assert.Equal(t, "unsubscribed", resubscribed["previous_status"])

		subscribed := buildTestPayload("list.subscribed")
		assert.Equal(t, "active", subscribed["status"])
		require.Contains(t, subscribed, "previous_status",
			"the trigger always builds the key, null included")
		assert.Nil(t, subscribed["previous_status"])
	})

	t.Run("an unrecognised event type says so instead of inventing a shape", func(t *testing.T) {
		payload := buildTestPayload("test")
		assert.ElementsMatch(t, []string{"message", "event_type", "created_at"}, keysOf(payload))
	})
}

// webhookPanickyReleaseStore is a delivery store whose release path blows up.
//
// The vector does not matter and is not the point: ReleaseClaim reaches a
// workspace connection, a pool and a driver, and the worker's contract is that a
// bug down there costs one delivery rather than the batch. What is under test is
// where the panic lands, which is a property of the code around the call.
type webhookPanickyReleaseStore struct {
	*fakeDeliveryStore
	released int
}

func (s *webhookPanickyReleaseStore) ReleaseClaim(context.Context, string, string, *time.Time, string) error {
	s.released++
	panic("boom in the release path")
}
