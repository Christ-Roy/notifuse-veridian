package domain

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestVeridianQueueEntriesFilter_Validate(t *testing.T) {
	f := VeridianQueueEntriesFilter{WorkspaceID: "ws"}
	assert.Error(t, f.Validate(), "automation_id obligatoire : jamais toute la file du workspace")

	f = VeridianQueueEntriesFilter{WorkspaceID: "ws", AutomationID: "a", Status: "sent"}
	assert.Error(t, f.Validate(), "« sent » n'existe pas : une entree envoyee est supprimee de la file")

	f = VeridianQueueEntriesFilter{WorkspaceID: "ws", AutomationID: "a", Status: "pending"}
	require.NoError(t, f.Validate())
	assert.Equal(t, VeridianQueueEntriesDefaultLimit, f.Limit)

	f = VeridianQueueEntriesFilter{WorkspaceID: "ws", AutomationID: "a", Limit: VeridianQueueEntriesMaxLimit + 1}
	assert.Error(t, f.Validate(), "au-dela du plafond : erreur explicite")

	f = VeridianQueueEntriesFilter{WorkspaceID: "ws", AutomationID: "a", Cursor: "%%%"}
	assert.Error(t, f.Validate(), "curseur invalide rejete")
}

func TestVeridianQueueCancelRequest_Validate(t *testing.T) {
	base := func() VeridianQueueCancelRequest {
		return VeridianQueueCancelRequest{WorkspaceID: "ws", AutomationID: "a", Reason: "desabonnement demande", Limit: 10}
	}

	r := base()
	r.Reason = "  "
	assert.Error(t, r.Validate(), "une annulation sans motif n'est pas acceptee (journal)")

	r = base()
	r.Reason = strings.Repeat("x", VeridianQueueCancelReasonMaxLen+1)
	assert.Error(t, r.Validate(), "motif trop long")

	r = base()
	r.AutomationID = ""
	assert.Error(t, r.Validate(), "sans automation_id ni entry_ids : refus")
	r.EntryIDs = []string{"e1"}
	assert.NoError(t, r.Validate(), "des entry_ids suffisent")

	r = base()
	r.Status = "processing"
	assert.Error(t, r.Validate(), "une entree en cours d'envoi n'est pas annulable")

	r = base()
	r.Limit = 0
	require.NoError(t, r.Validate())
	assert.Equal(t, VeridianQueueCancelDefaultLimit, r.Limit)

	r = base()
	r.Limit = VeridianQueueCancelMaxLimit + 1
	assert.Error(t, r.Validate())
}
