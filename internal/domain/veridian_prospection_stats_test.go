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
		// Raisons posées par la porte d'envoi et le premier mail (rapport du 27/10 : 16 contre 17).
		"contact_replied":           VeridianExitClassReplied,
		"list_status_unsubscribed":  VeridianExitClassUnsubscribed,
		"list_status_bounced":       VeridianExitClassRejected,
		"list_status_complained":    VeridianExitClassRejected,
		"first_mail_bounced":        VeridianExitClassRejected,
		"first_mail_policy_refused": VeridianExitClassRejected,
		"excluded_list":             VeridianExitClassExcluded,
		"contact_not_on_list":       VeridianExitClassOther,
		"manual":                    VeridianExitClassOther,
		"automation_deleted":        VeridianExitClassOther,
		"":                          VeridianExitClassOther,
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
	assert.Contains(t, string(body), `"recent_human_replies":[]`)

	// Les dernieres reponses sont bornees a 10 et rendues en UTC.
	var recent []VeridianRecentReply
	for i := 0; i < 15; i++ {
		recent = append(recent, VeridianRecentReply{ContactEmail: "c@x.fr", RepliedAt: time.Date(2026, 10, 8, 14, 0, 0, 0, time.FixedZone("p", 7200))})
	}
	capped := VeridianBuildProspectionStats(&VeridianProspectionRaw{RecentReplies: recent}, time.Time{}, time.Time{}, time.Now())
	require.Len(t, capped.RecentHumanReplies, VeridianRecentRepliesLimit)
	assert.Equal(t, 12, capped.RecentHumanReplies[0].RepliedAt.Hour())
}

// E4 (audit UI 19/10/2026) : « Stock restant » = 101 pour 51 contacts. Un contact inscrit a
// deux listes etait compte deux fois par la somme des lignes par liste. Le total doit etre
// le nombre de PERSONNES distinctes fourni par le depot, les lignes par liste restent par liste.
func TestVeridianBuildProspectionStats_StockCountsPeopleNotSubscriptions(t *testing.T) {
	people := 51
	raw := &VeridianProspectionRaw{
		Lists: []VeridianProspectionListRow{
			{ID: "a", Name: "A", Active: 51, NeverContacted: 51},
			{ID: "b", Name: "B", Active: 50, NeverContacted: 50}, // 50 des 51 sont aussi dans A
		},
		StockDistinct: &people,
	}
	out := VeridianBuildProspectionStats(raw, time.Time{}, time.Time{}, time.Now())
	assert.Equal(t, 51, out.Totals.StockRemaining, "51 personnes, pas 101 abonnements")
	assert.Equal(t, 51, out.Segments[0].NeverContacted)
	assert.Equal(t, 50, out.Segments[1].NeverContacted)
}

// Campagnes lisibles : template_ids par étape, dédupliqués, triés, sans chaîne vide.
func TestVeridianSequenceStages_TemplateIDs(t *testing.T) {
	nodes := []AutomationNode{
		{ID: "split", Type: NodeTypeABTest, Config: map[string]interface{}{"variants": []interface{}{
			map[string]interface{}{"id": "A", "next_node_id": "j0a"},
			map[string]interface{}{"id": "B", "next_node_id": "j0b"},
			map[string]interface{}{"id": "C", "next_node_id": "j0c"},
		}}},
		{ID: "j0a", Type: NodeTypeEmail, Config: map[string]interface{}{"template_id": "tplB"}, NextNodeID: prospectionNext("wait1")},
		{ID: "j0b", Type: NodeTypeEmail, Config: map[string]interface{}{"template_id": "tplA"}, NextNodeID: prospectionNext("wait1")},
		{ID: "j0c", Type: NodeTypeEmail, Config: map[string]interface{}{"template_id": "tplB"}, NextNodeID: prospectionNext("wait1")},
		{ID: "wait1", Type: NodeTypeDelay, Config: map[string]interface{}{"unit": "days", "duration": float64(4)}, NextNodeID: prospectionNext("j4")},
		{ID: "j4", Type: NodeTypeEmail, NextNodeID: prospectionNext("wait2")}, // pas de config du tout
		{ID: "wait2", Type: NodeTypeDelay, Config: map[string]interface{}{"unit": "days", "duration": float64(6)}, NextNodeID: prospectionNext("j10")},
		{ID: "j10", Type: NodeTypeEmail, Config: map[string]interface{}{"template_id": ""}}, // vide : ignoré
	}
	ns := veridianSequenceStages(VeridianProspectionAutomation{ID: "a", RootNodeID: "split", Nodes: nodes})
	require.Len(t, ns.stages, 3)
	assert.Equal(t, []string{"tplA", "tplB"}, ns.stages[0].TemplateIDs, "dédupliqué et trié")
	assert.NotNil(t, ns.stages[1].TemplateIDs, "jamais nil")
	assert.Empty(t, ns.stages[1].TemplateIDs, "nœud sans template_id : pas de chaîne vide")
	assert.NotNil(t, ns.stages[2].TemplateIDs)
	assert.Empty(t, ns.stages[2].TemplateIDs, "template_id vide : pas de chaîne vide")

	// Valeur non textuelle : ignorée, sans panique.
	odd := []AutomationNode{{ID: "e", Type: NodeTypeEmail, Config: map[string]interface{}{"template_id": float64(12)}}}
	ns = veridianSequenceStages(VeridianProspectionAutomation{RootNodeID: "e", Nodes: odd})
	require.Len(t, ns.stages, 1)
	assert.Empty(t, ns.stages[0].TemplateIDs)
	assert.NotNil(t, ns.stages[0].TemplateIDs)
}

// Réponses humaines cumulées par étape et sorties par classe après l'étape exacte.
func TestVeridianBuildProspectionStats_StageRepliesAndExitClasses(t *testing.T) {
	nodes := prospectionTestNodes("devenir")
	raw := &VeridianProspectionRaw{
		Automations: []VeridianProspectionAutomation{{ID: "devenir", RootNodeID: "split", Nodes: nodes}},
		Progress: []VeridianProspectionProgressRow{
			{AutomationID: "devenir", Status: "active", CurrentNodeID: "j4", LastEmailNodeID: "j0a", Count: 50},
			{AutomationID: "devenir", Status: "active", CurrentNodeID: "j10", LastEmailNodeID: "j4", Count: 20},
			{AutomationID: "devenir", Status: "completed", LastEmailNodeID: "j10", Count: 10},
			{AutomationID: "devenir", Status: "exited", ExitReason: "replied", LastEmailNodeID: "j0a", Count: 9},
			{AutomationID: "devenir", Status: "exited", ExitReason: "replied_manual_20261006", LastEmailNodeID: "j0b", Count: 1},
			{AutomationID: "devenir", Status: "exited", ExitReason: "replied", LastEmailNodeID: "j4", Count: 2},
			{AutomationID: "devenir", Status: "exited", ExitReason: "bounced", LastEmailNodeID: "j0b", Count: 3},
			{AutomationID: "devenir", Status: "exited", ExitReason: "unsubscribed", LastEmailNodeID: "j0b", Count: 4},
			{AutomationID: "devenir", Status: "exited", ExitReason: "unsubscribed", LastEmailNodeID: "j4", Count: 5},
			{AutomationID: "devenir", Status: "exited", ExitReason: "bounced", LastEmailNodeID: "j10", Count: 6},
			{AutomationID: "devenir", Status: "exited", ExitReason: "manual", LastEmailNodeID: "j4", Count: 7},
			// Sorti sur le nœud de son mail encore en file : aucun mail parti, aucune classe d'étape.
			{AutomationID: "devenir", Status: "exited", ExitReason: "replied", CurrentNodeID: "j0a", LastEmailNodeID: "j0a", Count: 8},
		},
		AutomationStepReplies: []VeridianProspectionStepReplyRow{
			{Key: "devenir", NodeID: "j0a", Human: 1},
			{Key: "devenir", NodeID: "j0b", Human: 2},
			{Key: "devenir", NodeID: "j4", Human: 4},
			{Key: "devenir", NodeID: "j10", Human: 8},
			{Key: "devenir", NodeID: "", Human: 100},        // contact sans mail parti : jamais compté
			{Key: "devenir", NodeID: "fantome", Human: 200}, // nœud inconnu de la séquence : ignoré
			{Key: "autre", NodeID: "j0a", Human: 400},       // autre séquence : ignorée
		},
	}
	out := VeridianBuildProspectionStats(raw, time.Time{}, time.Time{}, time.Now())
	require.Len(t, out.Sequences, 1)
	st := out.Sequences[0].Stages
	require.Len(t, st, 3)

	// Cumul : l'étape s reçoit les réponses dont le dernier mail parti est à l'étape k >= s.
	assert.Equal(t, 1+2+4+8, st[0].RepliesHuman)
	assert.Equal(t, 4+8, st[1].RepliesHuman)
	assert.Equal(t, 8, st[2].RepliesHuman)

	// Sorties par classe : exactes à l'étape du dernier mail parti, pas cumulées.
	assert.Equal(t, 9+1, st[0].ExitedReplied)
	assert.Equal(t, 3, st[0].ExitedRejected)
	assert.Equal(t, 4, st[0].ExitedUnsubscribed)
	assert.Equal(t, 9+1+3+4, st[0].ExitedAfter, "exited_after reste le total toutes classes")
	assert.Equal(t, 2, st[1].ExitedReplied)
	assert.Equal(t, 0, st[1].ExitedRejected)
	assert.Equal(t, 5, st[1].ExitedUnsubscribed)
	assert.Equal(t, 2+5+7, st[1].ExitedAfter, "la sortie « manual » est dans le total mais dans aucune classe")
	assert.Equal(t, 0, st[2].ExitedReplied)
	assert.Equal(t, 6, st[2].ExitedRejected)
	assert.Equal(t, 0, st[2].ExitedUnsubscribed)
	assert.Equal(t, 6, st[2].ExitedAfter)

	// La sémantique des champs existants ne bouge pas.
	assert.Equal(t, 50+20+10+9+1+2+3+4+5+6+7, st[0].Sent)
	assert.Equal(t, 8, out.Sequences[0].Exits.BeforeFirstMail)
}

// Contrat JSON : les nouveaux champs sont toujours présents, tableaux jamais null.
func TestVeridianBuildProspectionStats_StageNewFieldsJSON(t *testing.T) {
	raw := &VeridianProspectionRaw{
		Automations: []VeridianProspectionAutomation{{ID: "a", RootNodeID: "e1", Nodes: []AutomationNode{{ID: "e1", Type: NodeTypeEmail}}}},
	}
	out := VeridianBuildProspectionStats(raw, time.Time{}, time.Time{}, time.Now())
	body, err := json.Marshal(out)
	require.NoError(t, err)
	var decoded struct {
		Sequences []struct {
			Stages []map[string]interface{} `json:"stages"`
		} `json:"sequences"`
	}
	require.NoError(t, json.Unmarshal(body, &decoded))
	require.Len(t, decoded.Sequences, 1)
	require.Len(t, decoded.Sequences[0].Stages, 1)
	stage := decoded.Sequences[0].Stages[0]
	assert.Equal(t, []interface{}{}, stage["template_ids"], "[] et non null")
	for _, k := range []string{"replies_human", "exited_replied", "exited_unsubscribed", "exited_rejected"} {
		v, present := stage[k]
		assert.True(t, present, k)
		assert.Equal(t, float64(0), v, k)
	}
}

func TestVeridianProspectionStatsRequest_JSONShape(t *testing.T) {
	out, err := json.Marshal(VeridianProspectionStatsRequest{WorkspaceID: "ws", Since: time.Now(), Until: time.Now()})
	require.NoError(t, err)
	assert.JSONEq(t, `{"workspace_id":"ws"}`, string(out))
}

// Cas du 27/10 : « J0 envoyés » 2 947 servi contre 2 891 réellement partis. 56 contacts
// avaient leur mail J0 « terminé » (mis en file) puis se sont désinscrits avant l'envoi :
// aucun mail n'est parti, ils ne sont pas « envoyés ».
func TestVeridianBuildProspectionStats_ExitedBeforeSendIsNotSent(t *testing.T) {
	no, yes := false, true
	raw := &VeridianProspectionRaw{
		Automations: []VeridianProspectionAutomation{{ID: "devenir", Name: "À devenir", Status: "live", ListID: "l", RootNodeID: "split", Nodes: prospectionTestNodes("devenir")}},
		Progress: []VeridianProspectionProgressRow{
			{AutomationID: "devenir", Status: "active", CurrentNodeID: "j4", LastEmailNodeID: "j0a", Count: 2800},
			{AutomationID: "devenir", Status: "exited", ExitReason: "unsubscribed", LastEmailNodeID: "j0a", MailLeft: &no, Count: 33},
			{AutomationID: "devenir", Status: "exited", ExitReason: "unsubscribed", LastEmailNodeID: "j0b", MailLeft: &no, Count: 23},
			{AutomationID: "devenir", Status: "exited", ExitReason: "replied", LastEmailNodeID: "j0a", MailLeft: &yes, Count: 91},
		},
	}
	seq := VeridianBuildProspectionStats(raw, time.Time{}, time.Time{}, time.Now()).Sequences[0]
	assert.Equal(t, 2800+91, seq.Stages[0].Sent, "les 56 désinscrits avant l'envoi ne comptent pas")
	assert.Equal(t, 91, seq.Stages[0].ExitedAfter)
	assert.Equal(t, 56, seq.Exits.BeforeFirstMail)
	assert.Equal(t, 2800+56+91, seq.Enrolled)
}
