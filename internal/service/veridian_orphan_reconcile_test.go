package service

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/golang/mock/gomock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/Notifuse/notifuse/internal/domain"
	"github.com/Notifuse/notifuse/internal/domain/mocks"
)

// orphanRepoFake ajoute au depot d'automations la liste des orphelins (interface
// optionnelle detectee par assertion de type, comme en production).
type orphanRepoFake struct {
	*mocks.MockAutomationRepository
	list      func() []domain.VeridianParkedContact
	err       error
	gotWS     string
	gotGrace  time.Duration
	gotLimit  int
	listCalls int
}

func (f *orphanRepoFake) ListOrphanParked(_ context.Context, ws string, grace time.Duration, limit int) ([]domain.VeridianParkedContact, error) {
	f.listCalls++
	f.gotWS, f.gotGrace, f.gotLimit = ws, grace, limit
	if f.err != nil {
		return nil, f.err
	}
	return f.list(), nil
}

// orphanWorld : un contact parque sur le noeud email "j0a" (suite : "wait1").
type orphanWorld struct {
	ctrl     *gomock.Controller
	repo     *orphanRepoFake
	exec     *AutomationExecutor
	ca       *domain.ContactAutomation
	updates  []domain.ContactAutomation // copie de chaque UpdateContactAutomation
	nodeExec []*domain.NodeExecution
}

const (
	orphanWS    = "ws1"
	orphanAuto  = "auto1"
	orphanEmail = "x@example.com"
	orphanCA    = "ca1"
)

func newOrphanWorld(t *testing.T, orphan domain.VeridianParkedContact) *orphanWorld {
	t.Helper()
	ctrl := gomock.NewController(t)
	t.Cleanup(ctrl.Finish)

	node, next := "j0a", "wait1"
	w := &orphanWorld{ctrl: ctrl}
	w.ca = &domain.ContactAutomation{
		ID: orphanCA, AutomationID: orphanAuto, ContactEmail: orphanEmail,
		CurrentNodeID: &node, Status: domain.ContactAutomationStatusSending,
	}
	auto := &domain.Automation{
		ID: orphanAuto, Status: domain.AutomationStatusLive,
		Nodes: []*domain.AutomationNode{
			{ID: node, Type: domain.NodeTypeEmail, NextNodeID: &next},
			{ID: next, Type: domain.NodeTypeDelay},
		},
	}
	repo := mocks.NewMockAutomationRepository(ctrl)
	repo.EXPECT().GetContactAutomationByEmail(gomock.Any(), orphanWS, orphanAuto, orphanEmail).
		DoAndReturn(func(context.Context, string, string, string) (*domain.ContactAutomation, error) {
			c := *w.ca
			return &c, nil
		}).AnyTimes()
	repo.EXPECT().GetByID(gomock.Any(), orphanWS, orphanAuto).Return(auto, nil).AnyTimes()
	repo.EXPECT().CreateNodeExecution(gomock.Any(), orphanWS, gomock.Any()).
		DoAndReturn(func(_ context.Context, _ string, e *domain.NodeExecution) error {
			w.nodeExec = append(w.nodeExec, e)
			return nil
		}).AnyTimes()
	repo.EXPECT().IncrementAutomationStat(gomock.Any(), orphanWS, orphanAuto, gomock.Any()).Return(nil).AnyTimes()
	repo.EXPECT().UpdateContactAutomation(gomock.Any(), orphanWS, gomock.Any()).
		DoAndReturn(func(_ context.Context, _ string, ca *domain.ContactAutomation) error {
			w.updates = append(w.updates, *ca)
			*w.ca = *ca // l'etat persiste : un 2e passage voit le resultat du 1er
			return nil
		}).AnyTimes()

	orphan.ContactAutomationID, orphan.AutomationID, orphan.ContactEmail, orphan.NodeID = orphanCA, orphanAuto, orphanEmail, node
	w.repo = &orphanRepoFake{MockAutomationRepository: repo}
	// Comme la requete SQL : n'est orphelin que ce qui est ENCORE parque en « sending ».
	w.repo.list = func() []domain.VeridianParkedContact {
		if w.ca.Status != domain.ContactAutomationStatusSending {
			return nil
		}
		return []domain.VeridianParkedContact{orphan}
	}
	w.exec = &AutomationExecutor{
		automationRepo: w.repo,
		timelineRepo:   mocks.NewMockContactTimelineRepository(ctrl),
		logger:         setupMockLogger(ctrl),
	}
	w.exec.timelineRepo.(*mocks.MockContactTimelineRepository).EXPECT().Create(gomock.Any(), orphanWS, gomock.Any()).Return(nil).AnyTimes()
	return w
}

func (w *orphanWorld) run() int {
	return w.exec.VeridianReconcileOrphans(context.Background(), w.repo, orphanWS)
}

func TestVeridianReconcileOrphans_Rules(t *testing.T) {
	t.Run("regle 1: mail parti -> le contact avance, pas de re-mise en file", func(t *testing.T) {
		w := newOrphanWorld(t, domain.VeridianParkedContact{MessageID: "m1", MessageFound: true, MessageSent: true})
		assert.Equal(t, 1, w.run())
		require.Len(t, w.updates, 1)
		assert.Equal(t, domain.ContactAutomationStatusActive, w.updates[0].Status)
		require.NotNil(t, w.updates[0].CurrentNodeID)
		assert.Equal(t, "wait1", *w.updates[0].CurrentNodeID, "doit passer au noeud SUIVANT, jamais rejouer le noeud email")
		require.Len(t, w.nodeExec, 1)
		assert.Equal(t, domain.NodeActionCompleted, w.nodeExec[0].Action)
		assert.Equal(t, true, w.nodeExec[0].Output["sent"])
	})

	t.Run("regle 2: echec definitif -> sortie en echec", func(t *testing.T) {
		w := newOrphanWorld(t, domain.VeridianParkedContact{MessageID: "m1", MessageFound: true, MessageFailed: true})
		assert.Equal(t, 1, w.run())
		require.Len(t, w.updates, 1)
		assert.Equal(t, domain.ContactAutomationStatusExited, w.updates[0].Status)
		require.NotNil(t, w.updates[0].ExitReason)
		assert.Equal(t, "orphan_send_failed", *w.updates[0].ExitReason)
		require.Len(t, w.nodeExec, 1)
		assert.Equal(t, domain.NodeActionFailed, w.nodeExec[0].Action)
	})

	t.Run("regle 5: message a etat inconnu -> on ne touche a rien", func(t *testing.T) {
		w := newOrphanWorld(t, domain.VeridianParkedContact{MessageID: "m1", MessageFound: true})
		assert.Equal(t, 0, w.run())
		assert.Empty(t, w.updates)
		assert.Empty(t, w.nodeExec)
		assert.Equal(t, domain.ContactAutomationStatusSending, w.ca.Status)
	})

	t.Run("regle 3: deja contacte -> sortie orphan_already_contacted", func(t *testing.T) {
		w := newOrphanWorld(t, domain.VeridianParkedContact{AlreadyContacted: true})
		assert.Equal(t, 1, w.run())
		require.Len(t, w.updates, 1)
		assert.Equal(t, domain.ContactAutomationStatusExited, w.updates[0].Status)
		require.NotNil(t, w.updates[0].ExitReason)
		assert.Equal(t, "orphan_already_contacted", *w.updates[0].ExitReason)
	})

	t.Run("regle 4: jamais contacte -> re-arme en active, scheduled_at maintenant, meme noeud", func(t *testing.T) {
		w := newOrphanWorld(t, domain.VeridianParkedContact{})
		before := time.Now().UTC().Add(-time.Second)
		assert.Equal(t, 1, w.run())
		require.Len(t, w.updates, 1)
		u := w.updates[0]
		assert.Equal(t, domain.ContactAutomationStatusActive, u.Status)
		require.NotNil(t, u.ScheduledAt)
		assert.False(t, u.ScheduledAt.Before(before), "scheduled_at doit valoir maintenant")
		assert.WithinDuration(t, time.Now().UTC(), *u.ScheduledAt, 5*time.Second)
		require.NotNil(t, u.CurrentNodeID)
		assert.Equal(t, "j0a", *u.CurrentNodeID, "le noeud email rejoue ses controles")
		assert.Nil(t, u.ExitReason)
	})

	t.Run("le contact a bouge entre la liste et le traitement -> rien", func(t *testing.T) {
		for name, mutate := range map[string]func(*domain.ContactAutomation){
			"deja active": func(ca *domain.ContactAutomation) { ca.Status = domain.ContactAutomationStatusActive },
			"autre ligne": func(ca *domain.ContactAutomation) { ca.ID = "ca-autre" },
			"deja sortie": func(ca *domain.ContactAutomation) { ca.Status = domain.ContactAutomationStatusExited },
		} {
			t.Run(name, func(t *testing.T) {
				w := newOrphanWorld(t, domain.VeridianParkedContact{})
				// La liste (fixe) annonce l'orphelin, mais l'etat reel a change.
				stale := []domain.VeridianParkedContact{{ContactAutomationID: orphanCA, AutomationID: orphanAuto, ContactEmail: orphanEmail}}
				w.repo.list = func() []domain.VeridianParkedContact { return stale }
				mutate(w.ca)
				assert.Equal(t, 0, w.run())
				assert.Empty(t, w.updates)
			})
		}
	})
}

// Invariant central : un mail deja parti (ou possiblement parti, ou un contact deja
// contacte) ne revient JAMAIS en file. Re-mettre en file = re-armer le contact en
// « active » SUR le noeud email.
func TestVeridianReconcileOrphans_NeverRequeuesAMailAlreadySent(t *testing.T) {
	cases := []struct {
		name   string
		orphan domain.VeridianParkedContact
	}{
		{"mail parti", domain.VeridianParkedContact{MessageFound: true, MessageSent: true}},
		{"mail parti ET deja contacte", domain.VeridianParkedContact{MessageFound: true, MessageSent: true, AlreadyContacted: true}},
		{"echec definitif", domain.VeridianParkedContact{MessageFound: true, MessageFailed: true}},
		{"etat inconnu", domain.VeridianParkedContact{MessageFound: true}},
		{"deja contacte par ailleurs", domain.VeridianParkedContact{AlreadyContacted: true}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			w := newOrphanWorld(t, tc.orphan)
			w.run()
			w.run()
			for i, u := range w.updates {
				onEmailNode := u.CurrentNodeID != nil && *u.CurrentNodeID == "j0a"
				rearmed := u.Status == domain.ContactAutomationStatusActive && onEmailNode
				assert.Falsef(t, rearmed, "mise a jour %d: contact re-arme sur le noeud email alors qu'un mail est parti/possible", i)
			}
			assert.False(t, w.ca.Status == domain.ContactAutomationStatusActive && w.ca.CurrentNodeID != nil && *w.ca.CurrentNodeID == "j0a", "etat final: jamais re-arme sur le noeud email")
		})
	}
}

func TestVeridianReconcileOrphans_Idempotent(t *testing.T) {
	cases := map[string]domain.VeridianParkedContact{
		"mail parti":      {MessageFound: true, MessageSent: true},
		"echec":           {MessageFound: true, MessageFailed: true},
		"deja contacte":   {AlreadyContacted: true},
		"jamais contacte": {},
	}
	for name, orphan := range cases {
		t.Run(name, func(t *testing.T) {
			w := newOrphanWorld(t, orphan)
			assert.Equal(t, 1, w.run())
			afterFirst := len(w.updates)
			nodeExecAfterFirst := len(w.nodeExec)
			snapshot := *w.ca
			assert.Equal(t, 0, w.run(), "2e passage: plus rien a faire")
			assert.Len(t, w.updates, afterFirst)
			assert.Len(t, w.nodeExec, nodeExecAfterFirst)
			assert.Equal(t, snapshot.Status, w.ca.Status)
		})
	}
}

func TestVeridianReconcileOrphans_ListingParamsAndErrors(t *testing.T) {
	w := newOrphanWorld(t, domain.VeridianParkedContact{MessageFound: true})
	w.run()
	assert.Equal(t, orphanWS, w.repo.gotWS)
	assert.Equal(t, 15*time.Minute, w.repo.gotGrace, "delai de grace: ne jamais toucher un envoi en cours")
	assert.Equal(t, 100, w.repo.gotLimit, "lot borne")

	w.repo.err = errors.New("db down")
	assert.Equal(t, 0, w.exec.VeridianReconcileOrphans(context.Background(), w.repo, orphanWS))
	assert.Empty(t, w.updates)
}

func TestVeridianReconcileOneWorkspace_RoundRobin(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	var seen []string
	repo := &orphanRepoFake{MockAutomationRepository: mocks.NewMockAutomationRepository(ctrl)}
	repo.list = func() []domain.VeridianParkedContact { seen = append(seen, repo.gotWS); return nil }
	wsRepo := mocks.NewMockWorkspaceRepository(ctrl)
	wsRepo.EXPECT().List(gomock.Any()).Return([]*domain.Workspace{{ID: "a"}, {ID: "b"}, {ID: "c"}}, nil).AnyTimes()
	exec := &AutomationExecutor{automationRepo: repo, workspaceRepo: wsRepo, logger: setupMockLogger(ctrl)}

	for i := 0; i < 4; i++ {
		exec.veridianReconcileOneWorkspace(context.Background())
	}
	assert.Equal(t, []string{"a", "b", "c", "a"}, seen, "un workspace par tick, en tourniquet")

	t.Run("depot sans interface orphelins -> no-op", func(t *testing.T) {
		plain := mocks.NewMockAutomationRepository(ctrl) // aucune attente: tout appel echoue
		e := &AutomationExecutor{automationRepo: plain, workspaceRepo: wsRepo, logger: setupMockLogger(ctrl)}
		assert.Equal(t, 0, e.veridianReconcileOneWorkspace(context.Background()))
	})
	t.Run("sans workspaceRepo -> no-op", func(t *testing.T) {
		e := &AutomationExecutor{automationRepo: repo, logger: setupMockLogger(ctrl)}
		before := len(seen)
		assert.Equal(t, 0, e.veridianReconcileOneWorkspace(context.Background()))
		assert.Len(t, seen, before)
	})
}
