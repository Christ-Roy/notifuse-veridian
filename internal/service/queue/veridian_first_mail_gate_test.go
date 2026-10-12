package queue

import (
	"errors"
	"testing"
	"time"

	"github.com/Notifuse/notifuse/internal/domain"
	"github.com/Notifuse/notifuse/internal/domain/mocks"
	"github.com/golang/mock/gomock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Veridian (12/10/2026) : une relance ne part pas si le J0 n'a pas ete livre.
// Cf. veridian_first_mail_gate.go.

const fmAuto = "auto-1"

func fmJ0(mut func(m *domain.MessageHistory)) *domain.MessageHistory {
	a := fmAuto
	sent := time.Date(2026, 10, 8, 9, 0, 0, 0, time.UTC)
	m := &domain.MessageHistory{
		ID: "ws-1_j0", AutomationID: &a, Channel: "email", TemplateID: "tpl-j0",
		SentAt: &sent, CreatedAt: sent.Add(-time.Minute),
		VeridianSenderEmail: "robert@agences-veridian.fr", VeridianProfileID: "nord",
	}
	if mut != nil {
		mut(m)
	}
	return m
}

func fmStr(s string) *string { return &s }

func fmNow() *time.Time { t := time.Date(2026, 10, 8, 9, 5, 0, 0, time.UTC); return &t }

func TestVeridianFirstMailDelivery(t *testing.T) {
	cases := []struct {
		name      string
		j0        func(m *domain.MessageHistory)
		noHistory bool
		delivered bool
		reason    string
	}{
		{name: "J0 livre", delivered: true},
		{name: "J0 rebond mou : la relance part", j0: func(m *domain.MessageHistory) { m.BounceType = domain.VeridianBounceTypeSoft }, delivered: true},
		{name: "J0 rebond mou avec bounced_at pose : la relance part", j0: func(m *domain.MessageHistory) {
			m.BounceType = domain.VeridianBounceTypeSoft
			m.BouncedAt = fmNow()
		}, delivered: true},
		{name: "J0 echec d'envoi", j0: func(m *domain.MessageHistory) { m.FailedAt = fmNow(); m.StatusInfo = fmStr("550 mailbox unavailable") }, reason: domain.VeridianReasonFirstMailFailed},
		{name: "J0 rebond dur (bounced_at)", j0: func(m *domain.MessageHistory) {
			m.BouncedAt = fmNow()
			m.BounceType = domain.VeridianBounceTypeHard
			m.StatusInfo = fmStr("Bounce 5.4.1 Recipient address rejected: Access denied")
		}, reason: domain.VeridianReasonFirstMailBounced},
		{name: "J0 rebond dur sans libelle de type", j0: func(m *domain.MessageHistory) { m.BouncedAt = fmNow() }, reason: domain.VeridianReasonFirstMailBounced},
		{name: "J0 refus de politique 5.7.x", j0: func(m *domain.MessageHistory) {
			m.BounceType = domain.VeridianBounceTypePolicy
			m.StatusInfo = fmStr("Bounce 5.7.1 smtp; 554 5.7.1 rejected by rspamd filter")
		}, reason: domain.VeridianReasonFirstMailRefused},
		{name: "J0 jamais parti", j0: func(m *domain.MessageHistory) { m.SentAt = nil }, reason: domain.VeridianReasonFirstMailNotSent},
		{name: "aucun J0", noHistory: true, reason: domain.VeridianReasonFirstMailMissing},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var hist []*domain.MessageHistory
			if !tc.noHistory {
				hist = []*domain.MessageHistory{fmJ0(tc.j0)}
			}
			v := veridianFirstMailDelivery(hist, fmAuto, "ws-1_relance")
			assert.Equal(t, tc.delivered, v.Delivered)
			assert.Equal(t, tc.reason, v.Reason)
			if !tc.delivered {
				assert.NotEmpty(t, v.Detail)
			}
		})
	}
}

func TestVeridianFirstMailDelivery_Selection(t *testing.T) {
	t.Run("le J0 est le plus ancien : un J4 livre n'efface pas un J0 refuse", func(t *testing.T) {
		j0 := fmJ0(func(m *domain.MessageHistory) { m.BounceType = domain.VeridianBounceTypePolicy })
		j4 := fmJ0(func(m *domain.MessageHistory) {
			m.ID = "ws-1_j4"
			m.CreatedAt = m.CreatedAt.Add(96 * time.Hour)
		})
		v := veridianFirstMailDelivery([]*domain.MessageHistory{j4, j0}, fmAuto, "ws-1_j10")
		assert.False(t, v.Delivered)
		assert.Equal(t, "ws-1_j0", v.FirstID)
	})
	t.Run("l'entree courante, une autre automation, un SMS et un transactionnel sont ignores", func(t *testing.T) {
		other := "auto-AUTRE"
		cur := fmJ0(func(m *domain.MessageHistory) { m.ID = "cur"; m.FailedAt = fmNow() })
		o := fmJ0(func(m *domain.MessageHistory) { m.ID = "o"; m.AutomationID = &other; m.FailedAt = fmNow() })
		sms := fmJ0(func(m *domain.MessageHistory) { m.ID = "sms"; m.Channel = "sms"; m.FailedAt = fmNow() })
		tr := fmJ0(func(m *domain.MessageHistory) {
			m.ID = "tr"
			m.VeridianMessageType = domain.VeridianMessageTypeTransactional
			m.FailedAt = fmNow()
		})
		good := fmJ0(func(m *domain.MessageHistory) { m.CreatedAt = m.CreatedAt.Add(time.Hour) })
		v := veridianFirstMailDelivery([]*domain.MessageHistory{cur, o, sms, tr, good, nil}, fmAuto, "cur")
		assert.True(t, v.Delivered)
		assert.Equal(t, "ws-1_j0", v.FirstID)
	})
}

func fmAutomation(status domain.AutomationStatus) *domain.Automation {
	next := func(s string) *string { return &s }
	return &domain.Automation{
		ID: fmAuto, Status: status,
		Nodes: []*domain.AutomationNode{
			{ID: "split", Type: domain.NodeTypeABTest, Config: map[string]interface{}{"variants": []interface{}{
				map[string]interface{}{"id": "A", "next_node_id": "j0a"},
				map[string]interface{}{"id": "B", "next_node_id": "j0b"},
			}}},
			{ID: "j0a", Type: domain.NodeTypeEmail, Config: map[string]interface{}{"template_id": "tpl-a"}, NextNodeID: next("wait1")},
			{ID: "j0b", Type: domain.NodeTypeEmail, Config: map[string]interface{}{"template_id": "tpl-b"}, NextNodeID: next("wait1")},
			{ID: "wait1", Type: domain.NodeTypeDelay, Config: map[string]interface{}{}, NextNodeID: next("j4")},
			{ID: "j4", Type: domain.NodeTypeEmail, Config: map[string]interface{}{"template_id": "tpl-relance"}, NextNodeID: next("wait2")},
			{ID: "wait2", Type: domain.NodeTypeDelay, Config: map[string]interface{}{}, NextNodeID: next("j10")},
			{ID: "j10", Type: domain.NodeTypeEmail, Config: map[string]interface{}{"template_id": "tpl-relance-10"}},
		},
	}
}

func TestVeridianEntryIsFollowUp(t *testing.T) {
	a := fmAutomation(domain.AutomationStatusLive)
	e := func(node, tpl string) *domain.EmailQueueEntry {
		return &domain.EmailQueueEntry{NodeID: node, TemplateID: tpl}
	}
	assert.False(t, veridianEntryIsFollowUp(a, e("j0a", "tpl-a")), "premier mail, branche A")
	assert.False(t, veridianEntryIsFollowUp(a, e("j0b", "tpl-b")), "premier mail, branche B")
	assert.True(t, veridianEntryIsFollowUp(a, e("j4", "tpl-relance")))
	assert.True(t, veridianEntryIsFollowUp(a, e("j10", "tpl-relance-10")))
	// Entree posee avant V62 : pas de noeud, le modele identifie le noeud.
	assert.False(t, veridianEntryIsFollowUp(a, e("", "tpl-a")))
	assert.True(t, veridianEntryIsFollowUp(a, e("", "tpl-relance")))
	// Au moindre doute : pas une relance, donc jamais supprimee.
	assert.False(t, veridianEntryIsFollowUp(a, e("", "tpl-inconnu")))
	assert.False(t, veridianEntryIsFollowUp(a, e("nope", "")))
	assert.False(t, veridianEntryIsFollowUp(nil, e("j4", "")))
	assert.False(t, veridianEntryIsFollowUp(a, nil))
}

type fmEnv struct {
	worker   *EmailQueueWorker
	history  *mocks.MockMessageHistoryRepository
	queue    *mocks.MockEmailQueueRepository
	autos    *mocks.MockAutomationRepository
	journal  *fakeDecisionLog
	exits    []string
	exitMsgs []string
}

func newFMEnv(t *testing.T) *fmEnv {
	base := newVeridianThrottleTestEnv(t)
	ctrl := gomock.NewController(t)
	t.Cleanup(ctrl.Finish)
	env := &fmEnv{
		worker:  base.worker,
		history: base.mockMessageHistoryRepo,
		queue:   base.mockQueueRepo,
		autos:   mocks.NewMockAutomationRepository(ctrl),
		journal: &fakeDecisionLog{},
	}
	env.worker.SetAutomationSendGuard(env.autos, mocks.NewMockContactListRepository(ctrl), mocks.NewMockVeridianContactReplyRepository(ctrl))
	env.worker.SetDecisionLog(env.journal)
	env.worker.SetCallbacks(nil, func(_ string, _ domain.EmailQueueSourceType, _ string, email string, _ string, err error, permanent bool) {
		if permanent {
			env.exits = append(env.exits, email)
			env.exitMsgs = append(env.exitMsgs, err.Error())
		}
	})
	return env
}

func fmEntry(node, tpl string) *domain.EmailQueueEntry {
	return &domain.EmailQueueEntry{
		ID: "q-1", SourceType: domain.EmailQueueSourceAutomation, SourceID: fmAuto,
		ContactEmail: "lead@example.com", MessageID: "ws-1_relance", NodeID: node, TemplateID: tpl,
		IntegrationID: "int-1",
	}
}

func TestVeridianFirstMailGate_Worker(t *testing.T) {
	ws := &domain.Workspace{ID: "ws-1"}

	type tc struct {
		name       string
		j0         func(m *domain.MessageHistory)
		noHistory  bool
		entry      *domain.EmailQueueEntry
		status     domain.AutomationStatus
		wantSend   bool // true : la porte laisse passer
		wantReason string
	}
	cases := []tc{
		{name: "J0 refuse : la relance sort, raison politique", j0: func(m *domain.MessageHistory) {
			m.BounceType = domain.VeridianBounceTypePolicy
			m.StatusInfo = fmStr("Bounce 5.7.1 rejected by rspamd filter")
		}, entry: fmEntry("j4", "tpl-relance"), status: domain.AutomationStatusLive, wantReason: domain.VeridianReasonFirstMailRefused},
		{name: "J0 rebondi : la relance sort", j0: func(m *domain.MessageHistory) { m.BouncedAt = fmNow(); m.BounceType = domain.VeridianBounceTypeHard }, entry: fmEntry("j4", "tpl-relance"), status: domain.AutomationStatusLive, wantReason: domain.VeridianReasonFirstMailBounced},
		{name: "J0 en echec : la relance sort", j0: func(m *domain.MessageHistory) { m.FailedAt = fmNow() }, entry: fmEntry("j4", "tpl-relance"), status: domain.AutomationStatusLive, wantReason: domain.VeridianReasonFirstMailFailed},
		{name: "J0 absent : la relance sort", noHistory: true, entry: fmEntry("j4", "tpl-relance"), status: domain.AutomationStatusLive, wantReason: domain.VeridianReasonFirstMailMissing},
		{name: "J10 aussi (entree d'avant V62, sans noeud)", j0: func(m *domain.MessageHistory) { m.BounceType = domain.VeridianBounceTypePolicy }, entry: fmEntry("", "tpl-relance-10"), status: domain.AutomationStatusLive, wantReason: domain.VeridianReasonFirstMailRefused},
		{name: "J0 rebond MOU : la relance part", j0: func(m *domain.MessageHistory) { m.BounceType = domain.VeridianBounceTypeSoft }, entry: fmEntry("j4", "tpl-relance"), status: domain.AutomationStatusLive, wantSend: true},
		{name: "J0 livre : la relance part", entry: fmEntry("j4", "tpl-relance"), status: domain.AutomationStatusLive, wantSend: true},
		{name: "le premier mail lui-meme (pas de J0, normal) part", noHistory: true, entry: fmEntry("j0a", "tpl-a"), status: domain.AutomationStatusLive, wantSend: true},
		{name: "automation en pause : on ne touche a rien", j0: func(m *domain.MessageHistory) { m.BounceType = domain.VeridianBounceTypePolicy }, entry: fmEntry("j4", "tpl-relance"), status: domain.AutomationStatusPaused, wantSend: true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			env := newFMEnv(t)
			var hist []*domain.MessageHistory
			if !c.noHistory {
				hist = []*domain.MessageHistory{fmJ0(c.j0)}
			}
			env.history.EXPECT().GetByContact(gomock.Any(), "ws-1", gomock.Any(), "lead@example.com", gomock.Any(), 0).Return(hist, len(hist), nil).AnyTimes()
			env.autos.EXPECT().GetByID(gomock.Any(), "ws-1", fmAuto).Return(fmAutomation(c.status), nil).AnyTimes()
			if !c.wantSend {
				env.queue.EXPECT().Delete(gomock.Any(), "ws-1", "q-1").Return(nil)
			}

			got := env.worker.veridianFirstMailGate(ws, c.entry)
			assert.Equal(t, c.wantSend, got)
			if c.wantSend {
				assert.Empty(t, env.exits)
				assert.Empty(t, env.journal.rows)
				return
			}
			require.Equal(t, []string{"lead@example.com"}, env.exits, "le contact doit sortir de la sequence")
			assert.Equal(t, []string{c.wantReason}, env.exitMsgs)
			require.Len(t, env.journal.rows, 1)
			row := env.journal.rows[0]
			assert.Equal(t, domain.VeridianOutcomeExited, row.Outcome)
			assert.Equal(t, c.wantReason, row.Reason)
			assert.NotEmpty(t, row.Detail)
			assert.Equal(t, "lead@example.com", row.ContactEmail)
		})
	}
}

func TestVeridianFirstMailGate_ReadFailureFailsClosed(t *testing.T) {
	env := newFMEnv(t)
	ws := &domain.Workspace{ID: "ws-1"}
	env.history.EXPECT().GetByContact(gomock.Any(), "ws-1", gomock.Any(), "lead@example.com", gomock.Any(), 0).Return(nil, 0, errors.New("db down"))
	env.queue.EXPECT().MarkAsFailed(gomock.Any(), "ws-1", "q-1", gomock.Any(), gomock.Any()).Return(nil)
	assert.False(t, env.worker.veridianFirstMailGate(ws, fmEntry("j4", "tpl-relance")), "illisible : la relance ne part pas")
	assert.Empty(t, env.exits, "illisible : le contact ne sort pas non plus")
}

func TestVeridianFirstMailGate_NoopWithoutWiring(t *testing.T) {
	base := newVeridianThrottleTestEnv(t)
	ws := &domain.Workspace{ID: "ws-1"}
	// Constructeur upstream sans garde finale : comportement historique.
	assert.True(t, base.worker.veridianFirstMailGate(ws, fmEntry("j4", "tpl-relance")))
	assert.True(t, base.worker.veridianFirstMailGate(ws, &domain.EmailQueueEntry{SourceType: domain.EmailQueueSourceBroadcast}))
	assert.True(t, base.worker.veridianFirstMailGate(nil, nil))
}

func TestVeridianNodeSuccessors(t *testing.T) {
	n := &domain.AutomationNode{NextNodeID: fmStr("b"), Config: map[string]interface{}{
		"continue_node_id": "c", "variants": []interface{}{map[string]interface{}{"next_node_id": "d"}}, "other": "x",
	}}
	assert.ElementsMatch(t, []string{"b", "c", "d"}, veridianNodeSuccessors(n))
}
