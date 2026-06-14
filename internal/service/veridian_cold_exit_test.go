package service

import (
	"context"
	"errors"
	"testing"

	"github.com/Notifuse/notifuse/internal/domain"
	"github.com/Notifuse/notifuse/internal/domain/mocks"
	"github.com/golang/mock/gomock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fakeColdReplyChecker est une implémentation de test du contrat que le Lot 3 branchera.
type fakeColdReplyChecker struct {
	replied bool
	err     error
	calls   int
}

func (f *fakeColdReplyChecker) HasReplied(ctx context.Context, workspaceID, email string) (bool, error) {
	f.calls++
	return f.replied, f.err
}

func TestAutomationExecutor_SetColdReplyChecker(t *testing.T) {
	executor := &AutomationExecutor{}
	assert.Nil(t, executor.coldReplyChecker, "checker is nil by default (exit-on-reply off)")

	checker := &fakeColdReplyChecker{}
	executor.SetColdReplyChecker(checker)
	assert.Same(t, checker, executor.coldReplyChecker)
}

func TestAutomationExecutor_veridianColdExitReason(t *testing.T) {
	const (
		workspaceID = "ws1"
		email       = "prospect@example.com"
		listID      = "list-cold"
	)
	autoWithList := &domain.Automation{ID: "auto1", ListID: listID}

	t.Run("no checker, no list -> no exit", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		defer ctrl.Finish()
		clRepo := mocks.NewMockContactListRepository(ctrl)
		e := &AutomationExecutor{contactListRepo: clRepo}

		reason, err := e.veridianColdExitReason(context.Background(), workspaceID, &domain.Automation{ID: "a", ListID: ""}, email)
		require.NoError(t, err)
		assert.Equal(t, "", reason, "no list means no bounce check, no exit")
	})

	t.Run("replied -> ExitReasonReplied (does not even check bounce)", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		defer ctrl.Finish()
		clRepo := mocks.NewMockContactListRepository(ctrl)
		// GetContactListByIDs must NOT be called when reply short-circuits.
		checker := &fakeColdReplyChecker{replied: true}
		e := &AutomationExecutor{contactListRepo: clRepo, coldReplyChecker: checker}

		reason, err := e.veridianColdExitReason(context.Background(), workspaceID, autoWithList, email)
		require.NoError(t, err)
		assert.Equal(t, domain.ExitReasonReplied, reason)
		assert.Equal(t, 1, checker.calls)
	})

	t.Run("not replied + bounced -> ExitReasonBounced", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		defer ctrl.Finish()
		clRepo := mocks.NewMockContactListRepository(ctrl)
		clRepo.EXPECT().GetContactListByIDs(gomock.Any(), workspaceID, email, listID).
			Return(&domain.ContactList{Email: email, ListID: listID, Status: domain.ContactListStatusBounced}, nil)
		checker := &fakeColdReplyChecker{replied: false}
		e := &AutomationExecutor{contactListRepo: clRepo, coldReplyChecker: checker}

		reason, err := e.veridianColdExitReason(context.Background(), workspaceID, autoWithList, email)
		require.NoError(t, err)
		assert.Equal(t, domain.ExitReasonBounced, reason)
	})

	t.Run("complained also counts as bounced exit", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		defer ctrl.Finish()
		clRepo := mocks.NewMockContactListRepository(ctrl)
		clRepo.EXPECT().GetContactListByIDs(gomock.Any(), workspaceID, email, listID).
			Return(&domain.ContactList{Status: domain.ContactListStatusComplained}, nil)
		e := &AutomationExecutor{contactListRepo: clRepo}

		reason, err := e.veridianColdExitReason(context.Background(), workspaceID, autoWithList, email)
		require.NoError(t, err)
		assert.Equal(t, domain.ExitReasonBounced, reason)
	})

	t.Run("active status -> no exit", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		defer ctrl.Finish()
		clRepo := mocks.NewMockContactListRepository(ctrl)
		clRepo.EXPECT().GetContactListByIDs(gomock.Any(), workspaceID, email, listID).
			Return(&domain.ContactList{Status: domain.ContactListStatusActive}, nil)
		e := &AutomationExecutor{contactListRepo: clRepo}

		reason, err := e.veridianColdExitReason(context.Background(), workspaceID, autoWithList, email)
		require.NoError(t, err)
		assert.Equal(t, "", reason)
	})

	t.Run("contact not in list -> no exit", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		defer ctrl.Finish()
		clRepo := mocks.NewMockContactListRepository(ctrl)
		clRepo.EXPECT().GetContactListByIDs(gomock.Any(), workspaceID, email, listID).
			Return(nil, &domain.ErrContactListNotFound{Message: "not found"})
		e := &AutomationExecutor{contactListRepo: clRepo}

		reason, err := e.veridianColdExitReason(context.Background(), workspaceID, autoWithList, email)
		require.NoError(t, err)
		assert.Equal(t, "", reason, "not-in-list is not a bounce")
	})

	t.Run("checker error is propagated (best-effort handled by caller)", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		defer ctrl.Finish()
		clRepo := mocks.NewMockContactListRepository(ctrl)
		checker := &fakeColdReplyChecker{err: errors.New("reply store down")}
		e := &AutomationExecutor{contactListRepo: clRepo, coldReplyChecker: checker}

		reason, err := e.veridianColdExitReason(context.Background(), workspaceID, autoWithList, email)
		require.Error(t, err)
		assert.Equal(t, "", reason)
	})

	t.Run("repo error (not NotFound) is propagated", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		defer ctrl.Finish()
		clRepo := mocks.NewMockContactListRepository(ctrl)
		clRepo.EXPECT().GetContactListByIDs(gomock.Any(), workspaceID, email, listID).
			Return(nil, errors.New("db connection lost"))
		e := &AutomationExecutor{contactListRepo: clRepo}

		reason, err := e.veridianColdExitReason(context.Background(), workspaceID, autoWithList, email)
		require.Error(t, err)
		assert.Equal(t, "", reason)
	})

	t.Run("nil automation -> no exit", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		defer ctrl.Finish()
		clRepo := mocks.NewMockContactListRepository(ctrl)
		e := &AutomationExecutor{contactListRepo: clRepo}

		reason, err := e.veridianColdExitReason(context.Background(), workspaceID, nil, email)
		require.NoError(t, err)
		assert.Equal(t, "", reason)
	})
}

// coldCadenceFixture monte une cadence de 3 nodes (email→delay→email→delay→email
// simulés par des delay-passthrough) pour exercer le gate d'exit DANS la boucle Execute
// sans avoir à monter un vrai EmailNodeExecutor. Les 3 "emails" sont des nodes
// passthrough chaînés ; le gate est ce qui décide d'exiter ou de laisser dérouler.
func coldCadenceColdSequenceNodes() (*domain.Automation, *domain.ContactAutomation, *testNodeExecutor) {
	n1, n2, n3 := "n1", "n2", "n3"
	node1 := &domain.AutomationNode{ID: n1, Type: domain.NodeTypeDelay, NextNodeID: &n2}
	node2 := &domain.AutomationNode{ID: n2, Type: domain.NodeTypeDelay, NextNodeID: &n3}
	node3 := &domain.AutomationNode{ID: n3, Type: domain.NodeTypeDelay, NextNodeID: nil}

	automation := &domain.Automation{
		ID:     "auto1",
		Name:   "Cold cadence",
		Status: domain.AutomationStatusLive,
		ListID: "list-cold",
		Nodes:  []*domain.AutomationNode{node1, node2, node3},
	}
	ca := &domain.ContactAutomation{
		ID:            "ca1",
		AutomationID:  "auto1",
		ContactEmail:  "prospect@example.com",
		CurrentNodeID: &n1,
		Status:        domain.ContactAutomationStatusActive,
	}
	exec := &testNodeExecutor{
		nodeType: domain.NodeTypeDelay,
		execute: func(ctx context.Context, params NodeExecutionParams) (*NodeExecutionResult, error) {
			return &NodeExecutionResult{
				NextNodeID: params.Node.NextNodeID,
				Status:     domain.ContactAutomationStatusActive,
				Output:     map[string]interface{}{"ok": true},
			}, nil
		},
	}
	return automation, ca, exec
}

func TestAutomationExecutor_ColdCadence_RunsToEnd_NoSignal(t *testing.T) {
	// Aucun signal (pas de réponse, pas de bounce) → la cadence se déroule jusqu'au bout.
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	automationRepo := mocks.NewMockAutomationRepository(ctrl)
	contactRepo := mocks.NewMockContactRepository(ctrl)
	contactListRepo := mocks.NewMockContactListRepository(ctrl)
	timelineRepo := mocks.NewMockContactTimelineRepository(ctrl)
	logger := setupMockLogger(ctrl)

	automation, ca, nodeExec := coldCadenceColdSequenceNodes()
	checker := &fakeColdReplyChecker{replied: false}

	executor := &AutomationExecutor{
		automationRepo:   automationRepo,
		contactRepo:      contactRepo,
		contactListRepo:  contactListRepo,
		timelineRepo:     timelineRepo,
		coldReplyChecker: checker,
		nodeExecutors:    map[domain.NodeType]NodeExecutor{domain.NodeTypeDelay: nodeExec},
		logger:           logger,
	}

	const ws = "ws1"
	automationRepo.EXPECT().GetByID(gomock.Any(), ws, "auto1").Return(automation, nil)
	contactRepo.EXPECT().GetContactByEmail(gomock.Any(), ws, "prospect@example.com").
		Return(&domain.Contact{Email: "prospect@example.com"}, nil)
	// 3 nodes traversés → gate appelé 3 fois → contact "active" en liste à chaque fois.
	contactListRepo.EXPECT().GetContactListByIDs(gomock.Any(), ws, "prospect@example.com", "list-cold").
		Return(&domain.ContactList{Status: domain.ContactListStatusActive}, nil).Times(3)
	automationRepo.EXPECT().CreateNodeExecution(gomock.Any(), ws, gomock.Any()).Return(nil).Times(3)
	automationRepo.EXPECT().GetNodeExecutions(gomock.Any(), ws, "ca1").Return([]*domain.NodeExecution{}, nil).Times(3)
	automationRepo.EXPECT().UpdateContactAutomation(gomock.Any(), ws, gomock.Any()).Return(nil).Times(3)
	automationRepo.EXPECT().UpdateNodeExecution(gomock.Any(), ws, gomock.Any()).Return(nil).Times(3)
	automationRepo.EXPECT().IncrementAutomationStat(gomock.Any(), ws, "auto1", "completed").Return(nil)
	timelineRepo.EXPECT().Create(gomock.Any(), ws, gomock.Any()).Return(nil)

	err := executor.Execute(context.Background(), ws, ca)
	require.NoError(t, err)

	assert.Equal(t, domain.ContactAutomationStatusCompleted, ca.Status, "cadence runs to completion")
	assert.Nil(t, ca.CurrentNodeID)
	assert.Equal(t, 3, checker.calls, "reply checked before each step")
}

func TestAutomationExecutor_ColdCadence_ExitsEarlyOnReply(t *testing.T) {
	// Le prospect répond → il sort de la cadence AU PREMIER TICK, aucune relance derrière.
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	automationRepo := mocks.NewMockAutomationRepository(ctrl)
	contactRepo := mocks.NewMockContactRepository(ctrl)
	contactListRepo := mocks.NewMockContactListRepository(ctrl)
	timelineRepo := mocks.NewMockContactTimelineRepository(ctrl)
	logger := setupMockLogger(ctrl)

	automation, ca, nodeExec := coldCadenceColdSequenceNodes()
	checker := &fakeColdReplyChecker{replied: true}

	executor := &AutomationExecutor{
		automationRepo:   automationRepo,
		contactRepo:      contactRepo,
		contactListRepo:  contactListRepo,
		timelineRepo:     timelineRepo,
		coldReplyChecker: checker,
		nodeExecutors:    map[domain.NodeType]NodeExecutor{domain.NodeTypeDelay: nodeExec},
		logger:           logger,
	}

	const ws = "ws1"
	automationRepo.EXPECT().GetByID(gomock.Any(), ws, "auto1").Return(automation, nil)
	contactRepo.EXPECT().GetContactByEmail(gomock.Any(), ws, "prospect@example.com").
		Return(&domain.Contact{Email: "prospect@example.com"}, nil)
	// Reply court-circuite : aucun GetContactListByIDs, aucun node processé.
	automationRepo.EXPECT().CreateNodeExecution(gomock.Any(), ws, gomock.Any()).Times(0)
	automationRepo.EXPECT().IncrementAutomationStat(gomock.Any(), ws, "auto1", "exited").Return(nil)
	automationRepo.EXPECT().UpdateContactAutomation(gomock.Any(), ws, gomock.Any()).Return(nil)
	timelineRepo.EXPECT().Create(gomock.Any(), ws, gomock.Any()).Return(nil)

	err := executor.Execute(context.Background(), ws, ca)
	require.NoError(t, err)

	assert.Equal(t, domain.ContactAutomationStatusExited, ca.Status)
	require.NotNil(t, ca.ExitReason)
	assert.Equal(t, domain.ExitReasonReplied, *ca.ExitReason)
	assert.Nil(t, ca.ScheduledAt, "no relance scheduled after reply")
}

func TestAutomationExecutor_ColdCadence_ExitsOnBounce(t *testing.T) {
	// L'adresse bounce → exit immédiat avec raison bounced.
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	automationRepo := mocks.NewMockAutomationRepository(ctrl)
	contactRepo := mocks.NewMockContactRepository(ctrl)
	contactListRepo := mocks.NewMockContactListRepository(ctrl)
	timelineRepo := mocks.NewMockContactTimelineRepository(ctrl)
	logger := setupMockLogger(ctrl)

	automation, ca, nodeExec := coldCadenceColdSequenceNodes()

	executor := &AutomationExecutor{
		automationRepo:  automationRepo,
		contactRepo:     contactRepo,
		contactListRepo: contactListRepo,
		timelineRepo:    timelineRepo,
		// no reply checker injected (Lot 3 not wired) -> only bounce gate active
		nodeExecutors: map[domain.NodeType]NodeExecutor{domain.NodeTypeDelay: nodeExec},
		logger:        logger,
	}

	const ws = "ws1"
	automationRepo.EXPECT().GetByID(gomock.Any(), ws, "auto1").Return(automation, nil)
	contactRepo.EXPECT().GetContactByEmail(gomock.Any(), ws, "prospect@example.com").
		Return(&domain.Contact{Email: "prospect@example.com"}, nil)
	contactListRepo.EXPECT().GetContactListByIDs(gomock.Any(), ws, "prospect@example.com", "list-cold").
		Return(&domain.ContactList{Status: domain.ContactListStatusBounced}, nil)
	automationRepo.EXPECT().CreateNodeExecution(gomock.Any(), ws, gomock.Any()).Times(0)
	automationRepo.EXPECT().IncrementAutomationStat(gomock.Any(), ws, "auto1", "exited").Return(nil)
	automationRepo.EXPECT().UpdateContactAutomation(gomock.Any(), ws, gomock.Any()).Return(nil)
	timelineRepo.EXPECT().Create(gomock.Any(), ws, gomock.Any()).Return(nil)

	err := executor.Execute(context.Background(), ws, ca)
	require.NoError(t, err)

	assert.Equal(t, domain.ContactAutomationStatusExited, ca.Status)
	require.NotNil(t, ca.ExitReason)
	assert.Equal(t, domain.ExitReasonBounced, *ca.ExitReason)
}

func TestAutomationExecutor_ColdCadence_CheckerErrorIsBestEffort(t *testing.T) {
	// Une panne du checker de réponse ne doit PAS bloquer la cadence : le contact avance.
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	automationRepo := mocks.NewMockAutomationRepository(ctrl)
	contactRepo := mocks.NewMockContactRepository(ctrl)
	contactListRepo := mocks.NewMockContactListRepository(ctrl)
	timelineRepo := mocks.NewMockContactTimelineRepository(ctrl)
	logger := setupMockLogger(ctrl)

	automation, ca, nodeExec := coldCadenceColdSequenceNodes()
	checker := &fakeColdReplyChecker{err: errors.New("reply store down")}

	executor := &AutomationExecutor{
		automationRepo:   automationRepo,
		contactRepo:      contactRepo,
		contactListRepo:  contactListRepo,
		timelineRepo:     timelineRepo,
		coldReplyChecker: checker,
		nodeExecutors:    map[domain.NodeType]NodeExecutor{domain.NodeTypeDelay: nodeExec},
		logger:           logger,
	}

	const ws = "ws1"
	automationRepo.EXPECT().GetByID(gomock.Any(), ws, "auto1").Return(automation, nil)
	contactRepo.EXPECT().GetContactByEmail(gomock.Any(), ws, "prospect@example.com").
		Return(&domain.Contact{Email: "prospect@example.com"}, nil)
	// Checker erre à chaque tick mais best-effort → cadence se déroule quand même (3 nodes).
	automationRepo.EXPECT().CreateNodeExecution(gomock.Any(), ws, gomock.Any()).Return(nil).Times(3)
	automationRepo.EXPECT().GetNodeExecutions(gomock.Any(), ws, "ca1").Return([]*domain.NodeExecution{}, nil).Times(3)
	automationRepo.EXPECT().UpdateContactAutomation(gomock.Any(), ws, gomock.Any()).Return(nil).Times(3)
	automationRepo.EXPECT().UpdateNodeExecution(gomock.Any(), ws, gomock.Any()).Return(nil).Times(3)
	automationRepo.EXPECT().IncrementAutomationStat(gomock.Any(), ws, "auto1", "completed").Return(nil)
	timelineRepo.EXPECT().Create(gomock.Any(), ws, gomock.Any()).Return(nil)

	err := executor.Execute(context.Background(), ws, ca)
	require.NoError(t, err, "checker error must not fail the automation")

	assert.Equal(t, domain.ContactAutomationStatusCompleted, ca.Status)
	assert.Equal(t, 3, checker.calls)
}
