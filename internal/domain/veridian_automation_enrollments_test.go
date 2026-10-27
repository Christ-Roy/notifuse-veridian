package domain

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParseVeridianEnrollmentStatus(t *testing.T) {
	tests := []struct {
		name    string
		in      string
		want    []ContactAutomationStatus
		wantErr bool
	}{
		{name: "empty means no filter", in: "", want: nil},
		{name: "active includes sending", in: "active", want: []ContactAutomationStatus{ContactAutomationStatusActive, ContactAutomationStatusSending}},
		{name: "completed", in: "completed", want: []ContactAutomationStatus{ContactAutomationStatusCompleted}},
		{name: "exited", in: "exited", want: []ContactAutomationStatus{ContactAutomationStatusExited}},
		{name: "failed", in: "failed", want: []ContactAutomationStatus{ContactAutomationStatusFailed}},
		{name: "unknown value rejected", in: "bogus", wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ParseVeridianEnrollmentStatus(tt.in)
			if tt.wantErr {
				assert.Error(t, err)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestVeridianEnrollmentCursor_RoundTrip(t *testing.T) {
	at := time.Date(2026, 10, 27, 9, 30, 15, 123456000, time.UTC)
	c := EncodeVeridianEnrollmentCursor(at, "ca-42")
	gotAt, gotID, err := DecodeVeridianEnrollmentCursor(c)
	require.NoError(t, err)
	assert.True(t, at.Equal(gotAt), "instant conserve")
	assert.Equal(t, "ca-42", gotID)
}

func TestVeridianEnrollmentCursor_RejectsGarbage(t *testing.T) {
	for _, bad := range []string{"%%%", "bm90LWEtY3Vyc29y", ""} {
		_, _, err := DecodeVeridianEnrollmentCursor(bad)
		assert.Error(t, err, "curseur %q doit etre rejete", bad)
	}
}

func TestVeridianAutomationEnrollmentFilter_Validate(t *testing.T) {
	f := VeridianAutomationEnrollmentFilter{WorkspaceID: "ws", AutomationID: ""}
	assert.Error(t, f.Validate(), "automation_id obligatoire")

	f = VeridianAutomationEnrollmentFilter{WorkspaceID: "ws", AutomationID: "auto"}
	require.NoError(t, f.Validate())
	assert.Equal(t, VeridianEnrollmentDefaultLimit, f.Limit, "limite par defaut")

	f = VeridianAutomationEnrollmentFilter{WorkspaceID: "ws", AutomationID: "auto", Limit: VeridianEnrollmentMaxLimit + 1}
	assert.Error(t, f.Validate(), "au-dela du plafond, rejet explicite")
}
