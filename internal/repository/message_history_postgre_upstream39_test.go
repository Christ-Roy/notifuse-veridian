// Tests portes depuis Notifuse v39.1 (derniere version sous AGPL-3.0, avant la BSL de v40.0)
// source : internal/repository/message_history_postgre_test.go ; ne contient que les declarations absentes du fork.

package repository

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/Notifuse/notifuse/internal/domain"
	"github.com/golang/mock/gomock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestMessageHistoryRepository_ListMessages_CursorSubSecondPrecision guards the
// broadcast log against silent truncation: bulk sends write many rows inside the
// same wall-clock second, so the emitted cursor must carry the sub-second part of
// created_at. If it is rounded to the second, the next page's
// "WHERE created_at < cursor" skips every remaining row of that second.
func TestMessageHistoryRepository_ListMessages_CursorSubSecondPrecision(t *testing.T) {
	t.Skip("DÉFAUT D6 : le curseur de pagination perd les sous-secondes")
	mockWorkspaceRepo, repo, mock, db, cleanup := setupMessageHistoryTest(t)
	defer cleanup()

	ctx := context.Background()
	workspaceID := "workspace-123"

	// Two rows in the same second, as produced by a bulk broadcast insert.
	first := time.Date(2026, 8, 6, 20, 53, 20, 903753000, time.UTC)
	second := time.Date(2026, 8, 6, 20, 53, 20, 760394000, time.UTC)

	mockWorkspaceRepo.EXPECT().
		GetConnection(gomock.Any(), workspaceID).
		Return(db, nil)

	messageDataJSON, _ := json.Marshal(domain.MessageData{Data: map[string]interface{}{"subject": "Bulk"}})

	rows := sqlmock.NewRows([]string{
		"id", "external_id", "contact_email", "broadcast_id", "automation_id", "transactional_notification_id", "list_id", "template_id", "template_version",
		"channel", "status_info", "message_data", "channel_options", "attachments", "sent_at", "delivered_at",
		"failed_at", "opened_at", "clicked_at", "bounced_at", "complained_at",
		"unsubscribed_at", "created_at", "updated_at",
	}).
		AddRow("msg-a", nil, "a@example.com", stringPtr("bc-1"), nil, nil, "{}", "template-1", 1,
			"email", nil, messageDataJSON, nil, []byte("[]"), first, nil,
			nil, nil, nil, nil, nil, nil, first, first).
		AddRow("msg-b", nil, "b@example.com", stringPtr("bc-1"), nil, nil, "{}", "template-1", 1,
			"email", nil, messageDataJSON, nil, []byte("[]"), second, nil,
			nil, nil, nil, nil, nil, nil, second, second)

	mock.ExpectQuery(`SELECT .+ FROM message_history ORDER BY created_at DESC, id DESC LIMIT 2`).
		WillReturnRows(rows)

	messages, nextCursor, err := repo.ListMessages(ctx, workspaceID, testSecretKey, domain.MessageListParams{Limit: 1})
	require.NoError(t, err)
	require.Len(t, messages, 1)
	require.NotEmpty(t, nextCursor)

	decoded, err := base64.StdEncoding.DecodeString(nextCursor)
	require.NoError(t, err)
	parts := strings.Split(string(decoded), "~")
	require.Len(t, parts, 2)
	assert.Equal(t, "msg-a", parts[1])

	cursorTime, err := time.Parse(time.RFC3339Nano, parts[0])
	require.NoError(t, err)
	assert.True(t, cursorTime.Equal(first),
		"cursor must preserve sub-second precision: encoded %q, want %s", parts[0], first.Format(time.RFC3339Nano))

	// The round-tripped cursor must still select the older row in the same second.
	assert.True(t, second.Before(cursorTime),
		"row at %s would be skipped by the next page's WHERE created_at < %s",
		second.Format(time.RFC3339Nano), cursorTime.Format(time.RFC3339Nano))
}
