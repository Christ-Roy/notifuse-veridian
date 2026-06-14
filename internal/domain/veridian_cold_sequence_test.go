package domain

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestIsColdExitReason(t *testing.T) {
	tests := []struct {
		name   string
		reason string
		want   bool
	}{
		{"replied is cold exit", ExitReasonReplied, true},
		{"bounced is cold exit", ExitReasonBounced, true},
		{"completed is not", "completed", false},
		{"unsubscribed is not", "unsubscribed", false},
		{"filter_rejected is not", "filter_rejected", false},
		{"empty is not", "", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, IsColdExitReason(tt.reason))
		})
	}
}

func TestExitReasonConstants(t *testing.T) {
	// Garde-fou contractuel : ces littéraux sont consommés par l'executor (markAsExited)
	// et lus par le Hub/UI. Un rename silencieux casserait le contrat cross-couche.
	assert.Equal(t, "replied", ExitReasonReplied)
	assert.Equal(t, "bounced", ExitReasonBounced)
}

func TestDefaultColdSequenceSteps(t *testing.T) {
	steps := DefaultColdSequenceSteps("tpl-initial", "tpl-r1", "tpl-r2")

	require.Len(t, steps, 3)

	// Étape 0 : email initial, J+3 avant relance 1.
	assert.Equal(t, "tpl-initial", steps[0].TemplateID)
	assert.Equal(t, DefaultColdRelance1DelayDays, steps[0].DelayDays)
	assert.Equal(t, 3, steps[0].DelayDays)

	// Étape 1 : relance 1, J+4 avant relance 2 (cumul J+7).
	assert.Equal(t, "tpl-r1", steps[1].TemplateID)
	assert.Equal(t, DefaultColdRelance2DelayDays, steps[1].DelayDays)
	assert.Equal(t, 4, steps[1].DelayDays)

	// Étape 2 : relance 2, fin de cadence (pas de délai derrière).
	assert.Equal(t, "tpl-r2", steps[2].TemplateID)
	assert.Equal(t, 0, steps[2].DelayDays)

	// La cadence cumulée doit valoir J+0 → J+3 → J+7.
	assert.Equal(t, 7, steps[0].DelayDays+steps[1].DelayDays)
}

func TestColdSequenceOptions_Validate(t *testing.T) {
	last := func(d int) []ColdSequenceStep {
		return []ColdSequenceStep{{TemplateID: "t1", DelayDays: d}}
	}

	tests := []struct {
		name    string
		opts    ColdSequenceOptions
		wantErr string
	}{
		{
			name:    "missing automation_id",
			opts:    ColdSequenceOptions{Steps: last(0)},
			wantErr: "automation_id is required",
		},
		{
			name:    "no steps",
			opts:    ColdSequenceOptions{AutomationID: "auto1"},
			wantErr: "at least one step is required",
		},
		{
			name: "step missing template_id",
			opts: ColdSequenceOptions{
				AutomationID: "auto1",
				Steps:        []ColdSequenceStep{{TemplateID: "", DelayDays: 0}},
			},
			wantErr: "template_id is required",
		},
		{
			name: "negative delay",
			opts: ColdSequenceOptions{
				AutomationID: "auto1",
				Steps: []ColdSequenceStep{
					{TemplateID: "t1", DelayDays: -1},
					{TemplateID: "t2", DelayDays: 0},
				},
			},
			wantErr: "cannot be negative",
		},
		{
			name: "delay on last step rejected",
			opts: ColdSequenceOptions{
				AutomationID: "auto1",
				Steps:        []ColdSequenceStep{{TemplateID: "t1", DelayDays: 3}},
			},
			wantErr: "delay_days must be 0 on the last step",
		},
		{
			name: "valid single step",
			opts: ColdSequenceOptions{
				AutomationID: "auto1",
				Steps:        []ColdSequenceStep{{TemplateID: "t1", DelayDays: 0}},
			},
		},
		{
			name: "valid 3-step cadence",
			opts: ColdSequenceOptions{
				AutomationID: "auto1",
				Steps:        DefaultColdSequenceSteps("a", "b", "c"),
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.opts.Validate()
			if tt.wantErr != "" {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.wantErr)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

func TestBuildColdSequence_InvalidOptions(t *testing.T) {
	nodes, root, err := BuildColdSequence(ColdSequenceOptions{})
	require.Error(t, err)
	assert.Nil(t, nodes)
	assert.Empty(t, root)
}

func TestBuildColdSequence_SingleStep(t *testing.T) {
	nodes, root, err := BuildColdSequence(ColdSequenceOptions{
		AutomationID: "auto1",
		Steps:        []ColdSequenceStep{{TemplateID: "tpl-only", DelayDays: 0}},
	})
	require.NoError(t, err)

	// Un seul email, terminal, pas de delay.
	require.Len(t, nodes, 1)
	assert.Equal(t, root, nodes[0].ID)
	assert.Equal(t, NodeTypeEmail, nodes[0].Type)
	assert.Equal(t, "tpl-only", nodes[0].Config["template_id"])
	assert.Nil(t, nodes[0].NextNodeID, "single email must be terminal")
	assert.Equal(t, "auto1", nodes[0].AutomationID)
}

func TestBuildColdSequence_ThreeStepCadence(t *testing.T) {
	integration := "smtp-warmup-1"
	subject := "Suite à mon précédent message"

	opts := ColdSequenceOptions{
		AutomationID: "auto-cold",
		Steps: []ColdSequenceStep{
			{TemplateID: "tpl-0", DelayDays: 3},
			{TemplateID: "tpl-1", DelayDays: 4, IntegrationID: &integration, SubjectOverride: &subject},
			{TemplateID: "tpl-2", DelayDays: 0},
		},
	}

	nodes, root, err := BuildColdSequence(opts)
	require.NoError(t, err)

	// 3 emails + 2 delays = 5 nodes.
	require.Len(t, nodes, 5)

	byID := make(map[string]*AutomationNode, len(nodes))
	for _, n := range nodes {
		byID[n.ID] = n
		assert.Equal(t, "auto-cold", n.AutomationID, "all nodes share automation_id")
		require.NotNil(t, n.Config)
	}

	// Le root est le premier email.
	rootNode := byID[root]
	require.NotNil(t, rootNode)
	assert.Equal(t, NodeTypeEmail, rootNode.Type)
	assert.Equal(t, "tpl-0", rootNode.Config["template_id"])

	// Parcours linéaire complet : email0 → delay0 → email1 → delay1 → email2 (terminal).
	// On suit la chaîne via NextNodeID et on vérifie types + configs.
	var seenEmails, seenDelays int
	var lastEmailTemplate string
	cur := rootNode
	visited := make(map[string]bool)
	for cur != nil {
		require.False(t, visited[cur.ID], "no cycle in the cadence chain")
		visited[cur.ID] = true

		switch cur.Type {
		case NodeTypeEmail:
			seenEmails++
			lastEmailTemplate, _ = cur.Config["template_id"].(string)
		case NodeTypeDelay:
			seenDelays++
			assert.Equal(t, "days", cur.Config["unit"], "cold delays are in days")
			// duration > 0 sur tous les delays.
			dur, ok := cur.Config["duration"].(int)
			require.True(t, ok)
			assert.Greater(t, dur, 0)
		default:
			t.Fatalf("unexpected node type in cold cadence: %s", cur.Type)
		}

		if cur.NextNodeID == nil {
			break
		}
		next := byID[*cur.NextNodeID]
		require.NotNil(t, next, "NextNodeID %s must reference an existing node", *cur.NextNodeID)
		cur = next
	}

	assert.Equal(t, 3, seenEmails, "3 emails reachable in order")
	assert.Equal(t, 2, seenDelays, "2 delays reachable in order")
	assert.Equal(t, "tpl-2", lastEmailTemplate, "chain ends on the last relance")

	// Vérifie que les overrides de l'étape 1 sont bien posés sur SON email node.
	var email1 *AutomationNode
	for _, n := range nodes {
		if n.Type == NodeTypeEmail && n.Config["template_id"] == "tpl-1" {
			email1 = n
		}
	}
	require.NotNil(t, email1)
	assert.Equal(t, "smtp-warmup-1", email1.Config["integration_id"])
	assert.Equal(t, "Suite à mon précédent message", email1.Config["subject_override"])

	// Les delays totalisent bien J+7.
	totalDays := 0
	for _, n := range nodes {
		if n.Type == NodeTypeDelay {
			totalDays += n.Config["duration"].(int)
		}
	}
	assert.Equal(t, 7, totalDays)

	// Le node terminal (dernier email) n'a pas de suite.
	var terminal *AutomationNode
	for _, n := range nodes {
		if n.Type == NodeTypeEmail && n.Config["template_id"] == "tpl-2" {
			terminal = n
		}
	}
	require.NotNil(t, terminal)
	assert.Nil(t, terminal.NextNodeID)
}

func TestBuildColdSequence_ProducesValidNodes(t *testing.T) {
	// La cadence générée doit passer la validation upstream des nodes
	// (sinon Automation.Validate refuserait l'automation construite à partir d'elle).
	nodes, _, err := BuildColdSequence(ColdSequenceOptions{
		AutomationID: "auto1",
		Steps:        DefaultColdSequenceSteps("a", "b", "c"),
	})
	require.NoError(t, err)

	for _, n := range nodes {
		assert.NoError(t, n.Validate(), "node %s (%s) must be valid", n.ID, n.Type)
	}
}
