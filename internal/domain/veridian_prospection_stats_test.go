package domain

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func prospectionTestNodes(id string) []AutomationNode {
	next := func(s string) *string { return &s }
	return []AutomationNode{
		{ID: "split", Type: NodeTypeABTest, Config: map[string]interface{}{"variants": []interface{}{
			map[string]interface{}{"id": "A", "next_node_id": "j0a"},
			map[string]interface{}{"id": "B", "next_node_id": "j0b"},
		}}},
		{ID: "j0a", Type: NodeTypeEmail, NextNodeID: next("wait1")},
		{ID: "j0b", Type: NodeTypeEmail, NextNodeID: next("wait1")},
		{ID: "wait1", Type: NodeTypeDelay, Config: map[string]interface{}{"unit": "days", "duration": float64(4)}, NextNodeID: next("j4")},
		{ID: "j4", Type: NodeTypeEmail, NextNodeID: next("wait2")},
		{ID: "wait2", Type: NodeTypeDelay, Config: map[string]interface{}{"unit": "days", "duration": float64(6)}, NextNodeID: next("j10")},
		{ID: "j10", Type: NodeTypeEmail},
	}
}

func TestVeridianClassifyExitReason(t *testing.T) {
	cases := map[string]string{
		"replied":                 VeridianExitClassReplied,
		"replied_manual_20261006": VeridianExitClassReplied,
		"bounced":                 VeridianExitClassRejected,
		"unsubscribed":            VeridianExitClassUnsubscribed,
		"excluded_provider_class:security_gateway":     VeridianExitClassExcluded,
		"pre-filtered recipient: undeliverable_domain": VeridianExitClassExcluded,
		"manual":             VeridianExitClassOther,
		"automation_deleted": VeridianExitClassOther,
		"":                   VeridianExitClassOther,
	}
	for reason, want := range cases {
		assert.Equal(t, want, VeridianClassifyExitReason(reason), reason)
	}
}

func TestVeridianSequenceStages_LabelsComeFromCumulativeDelays(t *testing.T) {
	ns := veridianSequenceStages(VeridianProspectionAutomation{ID: "a", RootNodeID: "split", Nodes: prospectionTestNodes("a")})
	require.Len(t, ns.stages, 3)
	assert.Equal(t, []string{"J0", "J+4", "J+10"}, []string{ns.stages[0].Label, ns.stages[1].Label, ns.stages[2].Label})
	assert.Equal(t, []string{"j0a", "j0b"}, ns.stages[0].NodeIDs, "les deux variantes A/B forment la même étape")
	assert.Equal(t, 1, ns.nodeStage["j0b"])
	assert.Equal(t, 2, ns.nodeStage["j4"])
	assert.Equal(t, 3, ns.nodeStage["j10"])

	// Racine absente : on retombe sur le premier nœud, jamais de panique.
	ns = veridianSequenceStages(VeridianProspectionAutomation{ID: "a", RootNodeID: "", Nodes: prospectionTestNodes("a")})
	assert.Len(t, ns.stages, 3)
	// Délai en heures, non multiple d'un jour.
	nodes := []AutomationNode{
		{ID: "e1", Type: NodeTypeEmail, NextNodeID: prospectionNext("d")},
		{ID: "d", Type: NodeTypeDelay, Config: map[string]interface{}{"unit": "hours", "duration": float64(36)}, NextNodeID: prospectionNext("e2")},
		{ID: "e2", Type: NodeTypeEmail},
	}
	ns = veridianSequenceStages(VeridianProspectionAutomation{RootNodeID: "e1", Nodes: nodes})
	assert.Equal(t, "+36h", ns.stages[1].Label)
	// Séquence sans mail : aucune étape.
	assert.Empty(t, veridianSequenceStages(VeridianProspectionAutomation{}).stages)
}

func prospectionNext(s string) *string { return &s }

// Les chiffres reprennent un relevé réel de la séquence « E-commerce à devenir »
// (08/10/2026) : 2791 contacts « sending » (mail en file, pas parti), 715 en attente
// de J+4, 10 avec J+4 en file, et des sorties de toutes natures.
func TestVeridianBuildProspectionStats_SequenceProgress(t *testing.T) {
	raw := &VeridianProspectionRaw{
		Automations: []VeridianProspectionAutomation{{ID: "devenir", Name: "À devenir", Status: "live", ListID: "ecomdevenir", RootNodeID: "split", Nodes: prospectionTestNodes("devenir")}},
		Progress: []VeridianProspectionProgressRow{
			{AutomationID: "devenir", Status: "sending", CurrentNodeID: "j0a", LastEmailNodeID: "j0a", Count: 1310},
			{AutomationID: "devenir", Status: "sending", CurrentNodeID: "j0b", LastEmailNodeID: "j0b", Count: 1471},
			{AutomationID: "devenir", Status: "sending", CurrentNodeID: "j4", LastEmailNodeID: "j4", Count: 10},
			{AutomationID: "devenir", Status: "active", CurrentNodeID: "j4", LastEmailNodeID: "j0a", Count: 300},
			{AutomationID: "devenir", Status: "active", CurrentNodeID: "j4", LastEmailNodeID: "j0b", Count: 415},
			{AutomationID: "devenir", Status: "exited", ExitReason: "excluded_provider_class:ionos", CurrentNodeID: "j0a", LastEmailNodeID: "j0a", Count: 15},
			{AutomationID: "devenir", Status: "exited", ExitReason: "pre-filtered recipient: undeliverable_domain", CurrentNodeID: "j0b", LastEmailNodeID: "j0b", Count: 2},
			{AutomationID: "devenir", Status: "exited", ExitReason: "replied", LastEmailNodeID: "j0a", Count: 9},
			{AutomationID: "devenir", Status: "exited", ExitReason: "replied", LastEmailNodeID: "j4", Count: 2},
			{AutomationID: "devenir", Status: "exited", ExitReason: "bounced", LastEmailNodeID: "j0b", Count: 3},
			{AutomationID: "devenir", Status: "exited", ExitReason: "unsubscribed", LastEmailNodeID: "j0b", Count: 4},
			{AutomationID: "devenir", Status: "failed", CurrentNodeID: "split", Count: 1},
			{AutomationID: "supprimee", Status: "active", Count: 99}, // séquence non vivante : ignorée
		},
		AutomationReplies: []VeridianProspectionReplyRow{{Key: "devenir", Human: 11, Auto: 1}},
		AutomationSent:    []VeridianProspectionKeyCount{{Key: "devenir", Count: 220}},
	}
	out := VeridianBuildProspectionStats(raw, time.Time{}, time.Time{}, time.Date(2026, 10, 8, 9, 0, 0, 0, time.UTC))
	require.Len(t, out.Sequences, 1)
	seq := out.Sequences[0]

	assert.Equal(t, 1310+1471+10+300+415+15+2+9+2+3+4+1, seq.Enrolled, "la séquence supprimée n'est pas comptée")
	assert.Equal(t, 1, seq.Failed)
	require.Len(t, seq.Stages, 3)
	j0, j4, j10 := seq.Stages[0], seq.Stages[1], seq.Stages[2]

	// J0 : en file = les « sending » du premier mail ; parti = tous ceux qui sont plus loin ou sortis après.
	assert.Equal(t, 1310+1471, j0.Queued)
	assert.Equal(t, 10+300+415+9+2+3+4, j0.Sent, "un contact dont J+4 est en file a bien reçu J0")
	assert.Equal(t, 9+3+4, j0.ExitedAfter)
	assert.Equal(t, 0, j0.Waiting)
	// J+4 : 715 attendent l'échéance, 10 sont en file, 2 sont sortis après l'avoir reçu.
	assert.Equal(t, 300+415, j4.Waiting)
	assert.Equal(t, 10, j4.Queued)
	assert.Equal(t, 2, j4.Sent)
	assert.Equal(t, 2, j4.ExitedAfter)
	assert.Equal(t, 0, j10.Sent)

	// Sorties par raison.
	assert.Equal(t, 9+2, seq.Exits.Replied)
	assert.Equal(t, 3, seq.Exits.Rejected)
	assert.Equal(t, 4, seq.Exits.Unsubscribed)
	assert.Equal(t, 15+2, seq.Exits.Excluded)
	assert.Equal(t, 17, seq.Exits.BeforeFirstMail, "les exclus ont leur mail mis en file puis écarté : aucun mail n'est parti")
	assert.Equal(t, seq.Exits.Replied+seq.Exits.Rejected+seq.Exits.Unsubscribed+seq.Exits.Excluded+seq.Exits.Other, seq.Exits.Total)

	// Réponses et taux : réponses humaines de la fenêtre / contacts joints de la fenêtre.
	assert.Equal(t, 11, seq.RepliesHuman)
	assert.Equal(t, 1, seq.RepliesAuto)
	require.NotNil(t, seq.ReplyRateHuman)
	assert.InDelta(t, 0.05, *seq.ReplyRateHuman, 1e-9)
	assert.Equal(t, 1310+1471, out.Totals.QueuedFirst)
}

func TestVeridianBuildProspectionStats_SegmentsAndStock(t *testing.T) {
	raw := &VeridianProspectionRaw{
		Automations: []VeridianProspectionAutomation{{ID: "devenir", ListID: "ecomdevenir", RootNodeID: "split", Nodes: prospectionTestNodes("devenir")}},
		Lists: []VeridianProspectionListRow{
			{ID: "ecomdevenir", Name: "À devenir", Active: 3300, Bounced: 133, Unsubscribed: 181, NeverContacted: 2500},
			{ID: "vitrinefrance", Name: "Vitrine", Active: 16287, Unsubscribed: 873, NeverContacted: 16000}, // sans séquence vivante mais des actifs
			{ID: "suppressionglobale", Name: "Suppression", Bounced: 12, Unsubscribed: 76},                  // liste de suppression : ni actifs ni séquence
		},
		ListReplies: []VeridianProspectionReplyRow{{Key: "ecomdevenir", Human: 6, Auto: 0}},
		ListSent:    []VeridianProspectionKeyCount{{Key: "ecomdevenir", Count: 120}},
	}
	out := VeridianBuildProspectionStats(raw, time.Date(2026, 9, 28, 0, 0, 0, 0, time.UTC), time.Time{}, time.Now())
	require.Len(t, out.Segments, 2, "la liste de suppression n'est pas un segment de prospection")
	assert.Equal(t, "ecomdevenir", out.Segments[0].ListID)
	assert.Equal(t, []string{"devenir"}, out.Segments[0].SequenceIDs)
	assert.Equal(t, 2500, out.Segments[0].NeverContacted)
	require.NotNil(t, out.Segments[0].ReplyRateHuman)
	assert.InDelta(t, 0.05, *out.Segments[0].ReplyRateHuman, 1e-9)
	assert.Nil(t, out.Segments[1].ReplyRateHuman, "aucun envoi dans la fenêtre : pas de taux inventé")
	assert.Equal(t, 2500+16000, out.Totals.StockRemaining)
	require.NotNil(t, out.Since)
	assert.Nil(t, out.Until)

	// Rien du tout : listes vides, jamais nil (JSON [] et non null).
	empty := VeridianBuildProspectionStats(nil, time.Time{}, time.Time{}, time.Now())
	body, err := json.Marshal(empty)
	require.NoError(t, err)
	assert.Contains(t, string(body), `"sequences":[]`)
	assert.Contains(t, string(body), `"segments":[]`)
}

func TestVeridianProspectionStatsRequest_JSONShape(t *testing.T) {
	out, err := json.Marshal(VeridianProspectionStatsRequest{WorkspaceID: "ws", Since: time.Now(), Until: time.Now()})
	require.NoError(t, err)
	assert.JSONEq(t, `{"workspace_id":"ws"}`, string(out))
}
