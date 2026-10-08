package domain

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// Lot 4 : la dimension message_type separe commercial et transactionnel avec la meme
// definition que les compteurs et le journal (veridian_message_type, a defaut
// transactional_notification_id).
func TestAnalyticsMessageHistoryHasMessageTypeDimension(t *testing.T) {
	schema := PredefinedSchemas["message_history"]
	dim, ok := schema.Dimensions["message_type"]
	assert.True(t, ok, "la dimension message_type doit exister")
	assert.Equal(t, "string", dim.Type)
	assert.Contains(t, dim.SQL, "veridian_message_type = 'transactional'")
	assert.Contains(t, dim.SQL, "transactional_notification_id IS NOT NULL")
	assert.Contains(t, dim.SQL, "ELSE 'commercial'")
}
