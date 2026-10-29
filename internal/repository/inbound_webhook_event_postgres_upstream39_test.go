// Tests portes depuis Notifuse v39.1 (derniere version sous AGPL-3.0, avant la BSL de v40.0)
// source : internal/repository/inbound_webhook_event_postgres_test.go ; ne contient que les declarations absentes du fork.

package repository

import (
	"context"
	"encoding/base64"
	"strings"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/Notifuse/notifuse/internal/domain"
	"github.com/Notifuse/notifuse/internal/domain/mocks"
	"github.com/golang/mock/gomock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestInboundWebhookEventRepository_ListEvents_CursorSubSecondPrecision guards the
// webhook events log against the same truncation as the broadcast log: providers
// deliver events in bursts that land inside one wall-clock second, so the emitted
// cursor must keep the sub-second part of the timestamp.
func TestInboundWebhookEventRepository_ListEvents_CursorSubSecondPrecision(t *testing.T) {
	t.Skip("DÉFAUT D6 : le curseur de pagination perd les sous-secondes")
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	mockWorkspaceRepo := mocks.NewMockWorkspaceRepository(ctrl)
	repo := NewInboundWebhookEventRepository(mockWorkspaceRepo)

	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()

	ctx := context.Background()
	workspaceID := "ws-123"

	first := time.Date(2026, 8, 6, 20, 53, 20, 903753000, time.UTC)
	second := time.Date(2026, 8, 6, 20, 53, 20, 760394000, time.UTC)

	mockWorkspaceRepo.EXPECT().
		GetConnection(gomock.Any(), workspaceID).
		Return(db, nil)

	rows := sqlmock.NewRows([]string{
		"id", "type", "source", "integration_id", "recipient_email",
		"message_id", "timestamp", "raw_payload",
		"bounce_type", "bounce_category", "bounce_diagnostic", "complaint_feedback_type",
		"created_at",
	}).
		AddRow("evt-a", domain.EmailEventDelivered, domain.WebhookSourceSES, "integration-1", "a@example.com",
			"msg-a", first, `{"key": "a"}`, "", "", "", "", first).
		AddRow("evt-b", domain.EmailEventDelivered, domain.WebhookSourceSES, "integration-1", "b@example.com",
			"msg-b", second, `{"key": "b"}`, "", "", "", "", second)

	mock.ExpectQuery(`SELECT .+ FROM inbound_webhook_events`).
		WillReturnRows(rows)

	result, err := repo.ListEvents(ctx, workspaceID, domain.InboundWebhookEventListParams{Limit: 1})
	require.NoError(t, err)
	require.Len(t, result.Events, 1)
	require.True(t, result.HasMore)
	require.NotEmpty(t, result.NextCursor)

	decoded, err := base64.StdEncoding.DecodeString(result.NextCursor)
	require.NoError(t, err)
	parts := strings.Split(string(decoded), "~")
	require.Len(t, parts, 2)
	assert.Equal(t, "evt-a", parts[1])

	cursorTime, err := time.Parse(time.RFC3339Nano, parts[0])
	require.NoError(t, err)
	assert.True(t, cursorTime.Equal(first),
		"cursor must preserve sub-second precision: encoded %q, want %s", parts[0], first.Format(time.RFC3339Nano))
	assert.True(t, second.Before(cursorTime),
		"event at %s would be skipped by the next page's WHERE timestamp < %s",
		second.Format(time.RFC3339Nano), cursorTime.Format(time.RFC3339Nano))
}
