package service

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/golang/mock/gomock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/Notifuse/notifuse/internal/domain"
	"github.com/Notifuse/notifuse/internal/domain/mocks"
	pkgmocks "github.com/Notifuse/notifuse/pkg/mocks"
)

// --- doubles a la main (les interfaces du lot n'ont pas de mock genere) ---

type fakeExplainRepo struct {
	explainOut    *domain.VeridianQueueExplain
	explainErr    error
	explainCalls  int
	gotFilter     domain.VeridianQueueExplainFilter
	detail        *domain.VeridianQueueEntryDetail
	detailErr     error
	recomputeIDs  []string
	recomputeErr  error
	recomputeCall int
	gotRecompute  domain.VeridianQueueRecomputeRequest
	gotWS         string
}

func (f *fakeExplainRepo) Explain(_ context.Context, ws string, fl domain.VeridianQueueExplainFilter) (*domain.VeridianQueueExplain, error) {
	f.explainCalls++
	f.gotWS, f.gotFilter = ws, fl
	return f.explainOut, f.explainErr
}
func (f *fakeExplainRepo) EntryDetail(_ context.Context, ws, id string) (*domain.VeridianQueueEntryDetail, error) {
	f.gotWS = ws
	return f.detail, f.detailErr
}
func (f *fakeExplainRepo) Recompute(_ context.Context, ws string, r domain.VeridianQueueRecomputeRequest) ([]string, error) {
	f.recomputeCall++
	f.gotWS, f.gotRecompute = ws, r
	return f.recomputeIDs, f.recomputeErr
}

type fakeDecisionRepo struct {
	list      []*domain.VeridianSendDecision
	next      string
	listErr   error
	listCalls int
	gotFilter domain.VeridianSendDecisionFilter
	inserted  []*domain.VeridianSendDecision
	insertErr error
}

func (f *fakeDecisionRepo) Insert(_ context.Context, _ string, d *domain.VeridianSendDecision) error {
	f.inserted = append(f.inserted, d)
	return f.insertErr
}
func (f *fakeDecisionRepo) List(_ context.Context, _ string, fl domain.VeridianSendDecisionFilter) ([]*domain.VeridianSendDecision, string, error) {
	f.listCalls++
	f.gotFilter = fl
	return f.list, f.next, f.listErr
}

type explainHarness struct {
	svc       domain.VeridianQueueExplainService
	explain   *fakeExplainRepo
	decisions *fakeDecisionRepo
	wsRepo    *mocks.MockWorkspaceRepository
	auth      *mocks.MockAuthService
}

func newExplainHarness(t *testing.T) *explainHarness {
	t.Helper()
	ctrl := gomock.NewController(t)
	t.Cleanup(ctrl.Finish)
	log := pkgmocks.NewMockLogger(ctrl)
	log.EXPECT().WithField(gomock.Any(), gomock.Any()).Return(log).AnyTimes()
	log.EXPECT().Warn(gomock.Any()).AnyTimes()
	log.EXPECT().Error(gomock.Any()).AnyTimes()
	h := &explainHarness{
		explain: &fakeExplainRepo{}, decisions: &fakeDecisionRepo{},
		wsRepo: mocks.NewMockWorkspaceRepository(ctrl), auth: mocks.NewMockAuthService(ctrl),
	}
	h.svc = NewVeridianQueueExplainService(h.explain, h.decisions, h.wsRepo, h.auth, log)
	return h
}

func automationsMembership(read, write bool) *domain.UserWorkspace {
	return &domain.UserWorkspace{Role: "member", Permissions: domain.UserPermissions{
		domain.PermissionResourceAutomations: {Read: read, Write: write},
	}}
}

func (h *explainHarness) authAs(m *domain.UserWorkspace) {
	h.auth.EXPECT().AuthenticateUserForWorkspace(gomock.Any(), "ws1").
		Return(context.Background(), &domain.User{}, m, nil).AnyTimes()
}

func (h *explainHarness) workspaceWithProfiles() {
	h.wsRepo.EXPECT().GetByID(gomock.Any(), "ws1").Return(&domain.Workspace{
		ID:           "ws1",
		Integrations: []domain.Integration{{ID: "p1", Name: "Relais nord"}, {ID: "p2", Name: "Relais sud"}},
	}, nil).AnyTimes()
}

func TestVeridianQueueExplainService_Permissions(t *testing.T) {
	ctx := context.Background()

	t.Run("workspace_id vide -> refus avant tout acces", func(t *testing.T) {
		h := newExplainHarness(t) // aucune attente sur auth : un appel ferait echouer le test
		_, err := h.svc.Explain(ctx, "", domain.VeridianQueueExplainFilter{})
		require.Error(t, err)
		assert.Equal(t, 0, h.explain.explainCalls)
	})

	t.Run("authentification en echec -> aucune lecture", func(t *testing.T) {
		h := newExplainHarness(t)
		h.auth.EXPECT().AuthenticateUserForWorkspace(gomock.Any(), "ws1").Return(ctx, nil, nil, errors.New("nope"))
		_, err := h.svc.Explain(ctx, "ws1", domain.VeridianQueueExplainFilter{})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "failed to authenticate user")
		assert.Equal(t, 0, h.explain.explainCalls)
	})

	t.Run("lecture: automations:read requis, write seul ne suffit pas", func(t *testing.T) {
		h := newExplainHarness(t)
		h.authAs(automationsMembership(false, true))
		_, err := h.svc.Explain(ctx, "ws1", domain.VeridianQueueExplainFilter{})
		var perm *domain.PermissionError
		require.ErrorAs(t, err, &perm)
		assert.Equal(t, domain.PermissionResourceAutomations, perm.Resource)
		assert.Equal(t, domain.PermissionTypeRead, perm.Permission)

		_, _, _, err = h.svc.Decisions(ctx, "ws1", domain.VeridianSendDecisionFilter{})
		require.ErrorAs(t, err, &perm)
		assert.Equal(t, 0, h.explain.explainCalls)
		assert.Equal(t, 0, h.decisions.listCalls)
	})

	t.Run("recalcul: automations:write requis, read seul ne suffit pas", func(t *testing.T) {
		h := newExplainHarness(t)
		h.authAs(automationsMembership(true, false))
		_, err := h.svc.Recompute(ctx, domain.VeridianQueueRecomputeRequest{WorkspaceID: "ws1", AutomationID: "a1", Limit: 10})
		var perm *domain.PermissionError
		require.ErrorAs(t, err, &perm)
		assert.Equal(t, domain.PermissionTypeWrite, perm.Permission)
		assert.Equal(t, 0, h.explain.recomputeCall, "le depot ne doit pas etre touche sans droit d'ecriture")
		assert.Empty(t, h.decisions.inserted)
	})

	t.Run("autre ressource que automations -> refus", func(t *testing.T) {
		h := newExplainHarness(t)
		h.authAs(&domain.UserWorkspace{Role: "member", Permissions: domain.UserPermissions{
			domain.PermissionResourceContacts: {Read: true, Write: true},
		}})
		_, err := h.svc.Explain(ctx, "ws1", domain.VeridianQueueExplainFilter{})
		require.Error(t, err)
	})
}

func TestVeridianQueueExplainService_Explain(t *testing.T) {
	ctx := context.Background()

	t.Run("group_by par defaut et noms de profils resolus", func(t *testing.T) {
		h := newExplainHarness(t)
		h.authAs(automationsMembership(true, false))
		h.workspaceWithProfiles()
		h.explain.explainOut = &domain.VeridianQueueExplain{
			WorkspaceID: "ws1", Total: 3,
			Groups: []domain.VeridianQueueGroup{
				{ProfileID: "p1", Count: 2}, {ProfileID: "p2", Count: 1}, {ProfileID: "inconnu", Count: 0}, {Count: 0},
			},
		}
		out, err := h.svc.Explain(ctx, "ws1", domain.VeridianQueueExplainFilter{Reason: "window_closed"})
		require.NoError(t, err)
		assert.Equal(t, []string{"automation", "node", "reason", "profile"}, h.explain.gotFilter.GroupBy)
		assert.Equal(t, "window_closed", h.explain.gotFilter.Reason, "le filtre est transmis tel quel")
		assert.Equal(t, "ws1", h.explain.gotWS)
		assert.Equal(t, "Relais nord", out.Groups[0].ProfileName)
		assert.Equal(t, "Relais sud", out.Groups[1].ProfileName)
		assert.Equal(t, "", out.Groups[2].ProfileName, "profil absent du workspace: pas de nom invente")
		assert.Equal(t, "", out.Groups[3].ProfileName)
	})

	t.Run("group_by explicite transmis", func(t *testing.T) {
		h := newExplainHarness(t)
		h.authAs(automationsMembership(true, false))
		h.workspaceWithProfiles()
		h.explain.explainOut = &domain.VeridianQueueExplain{}
		_, err := h.svc.Explain(ctx, "ws1", domain.VeridianQueueExplainFilter{GroupBy: []string{"class", "node"}})
		require.NoError(t, err)
		assert.Equal(t, []string{"class", "node"}, h.explain.gotFilter.GroupBy)
	})

	t.Run("group_by inconnu -> erreur 'invalid ...' (400 cote handler), depot non appele", func(t *testing.T) {
		h := newExplainHarness(t)
		h.authAs(automationsMembership(true, false))
		h.workspaceWithProfiles()
		_, err := h.svc.Explain(ctx, "ws1", domain.VeridianQueueExplainFilter{GroupBy: []string{"node", "; drop table"}})
		require.Error(t, err)
		assert.True(t, strings.HasPrefix(err.Error(), "invalid "), "le handler s'appuie sur ce prefixe pour repondre 400: %q", err.Error())
		assert.Equal(t, 0, h.explain.explainCalls)
	})

	t.Run("workspace illisible -> degrade sans noms, pas d'erreur", func(t *testing.T) {
		h := newExplainHarness(t)
		h.authAs(automationsMembership(true, false))
		h.wsRepo.EXPECT().GetByID(gomock.Any(), "ws1").Return(nil, errors.New("boom"))
		h.explain.explainOut = &domain.VeridianQueueExplain{Groups: []domain.VeridianQueueGroup{{ProfileID: "p1"}}}
		out, err := h.svc.Explain(ctx, "ws1", domain.VeridianQueueExplainFilter{})
		require.NoError(t, err)
		assert.Equal(t, "", out.Groups[0].ProfileName)
	})

	t.Run("erreur du depot -> enveloppee", func(t *testing.T) {
		h := newExplainHarness(t)
		h.authAs(automationsMembership(true, false))
		h.workspaceWithProfiles()
		h.explain.explainErr = errors.New("db down")
		_, err := h.svc.Explain(ctx, "ws1", domain.VeridianQueueExplainFilter{})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "failed to explain queue")
		assert.ErrorIs(t, err, h.explain.explainErr)
	})

	t.Run("entry_id: detail + derniere decision avec trace, noms resolus, groupes vides", func(t *testing.T) {
		h := newExplainHarness(t)
		h.authAs(automationsMembership(true, false))
		h.workspaceWithProfiles()
		h.explain.detail = &domain.VeridianQueueEntryDetail{ID: "e1", IntegrationID: "p1"}
		h.decisions.list = []*domain.VeridianSendDecision{{
			ID: "d1", EntryID: "e1", ProfileID: "p2",
		}}
		out, err := h.svc.Explain(ctx, "ws1", domain.VeridianQueueExplainFilter{EntryID: "e1", Reason: "ignore"})
		require.NoError(t, err)
		require.NotNil(t, out.Entry)
		assert.Equal(t, "Relais nord", out.Entry.ProfileName)
		require.NotNil(t, out.Entry.LastDecision)
		assert.Equal(t, "Relais sud", out.Entry.LastDecision.ProfileName)
		assert.NotNil(t, out.Groups)
		assert.Empty(t, out.Groups)
		assert.Equal(t, 0, h.explain.explainCalls, "le mode detail ne lance pas l'agregation")
		assert.Equal(t, "e1", h.decisions.gotFilter.EntryID)
		assert.Equal(t, 1, h.decisions.gotFilter.Limit)
		assert.True(t, h.decisions.gotFilter.WithTrace, "le detail d'une entree doit porter la trace")
	})

	t.Run("entry_id inconnue -> ErrVeridianQueueEntryNotFound", func(t *testing.T) {
		h := newExplainHarness(t)
		h.authAs(automationsMembership(true, false))
		h.workspaceWithProfiles()
		h.explain.detail = nil
		_, err := h.svc.Explain(ctx, "ws1", domain.VeridianQueueExplainFilter{EntryID: "ghost"})
		assert.ErrorIs(t, err, ErrVeridianQueueEntryNotFound)
	})

	t.Run("entry_id: la derniere decision en echec n'empeche pas le detail", func(t *testing.T) {
		h := newExplainHarness(t)
		h.authAs(automationsMembership(true, false))
		h.workspaceWithProfiles()
		h.explain.detail = &domain.VeridianQueueEntryDetail{ID: "e1"}
		h.decisions.listErr = errors.New("journal HS")
		out, err := h.svc.Explain(ctx, "ws1", domain.VeridianQueueExplainFilter{EntryID: "e1"})
		require.NoError(t, err)
		assert.Nil(t, out.Entry.LastDecision)
	})
}

func TestVeridianQueueExplainService_Decisions(t *testing.T) {
	ctx := context.Background()

	t.Run("filtre transmis, curseur rendu, noms (y compris candidats de la trace) resolus", func(t *testing.T) {
		h := newExplainHarness(t)
		h.authAs(automationsMembership(true, false))
		h.wsRepo.EXPECT().GetByID(gomock.Any(), "ws1").Return(&domain.Workspace{
			ID:           "ws1",
			Integrations: []domain.Integration{{ID: "p1", Name: "Relais nord"}, {ID: "p2", Name: "Relais sud"}},
			Settings:     domain.WorkspaceSettings{VeridianDecisionLogLevel: domain.VeridianDecisionLogAll},
		}, nil)
		h.decisions.list = []*domain.VeridianSendDecision{{
			ID: "d1", ProfileID: "p1",
			Trace: &domain.VeridianSendTrace{Candidates: []domain.VeridianCandidateTrace{
				{Profile: "p2"}, {Profile: "p1", ProfileName: "deja nomme"},
			}},
		}}
		h.decisions.next = "cursor-2"
		f := domain.VeridianSendDecisionFilter{AutomationID: "a1", Email: "x@y.fr", Outcome: domain.VeridianOutcomeDeferred, Limit: 25, Cursor: "c1"}

		list, next, level, err := h.svc.Decisions(ctx, "ws1", f)
		require.NoError(t, err)
		assert.Equal(t, f, h.decisions.gotFilter)
		assert.Equal(t, "cursor-2", next)
		assert.Equal(t, domain.VeridianDecisionLogAll, level)
		require.Len(t, list, 1)
		assert.Equal(t, "Relais nord", list[0].ProfileName)
		assert.Equal(t, "Relais sud", list[0].Trace.Candidates[0].ProfileName)
		assert.Equal(t, "deja nomme", list[0].Trace.Candidates[1].ProfileName, "un nom deja present n'est pas ecrase")
	})

	t.Run("issue inconnue -> 'invalid outcome', journal non lu", func(t *testing.T) {
		h := newExplainHarness(t)
		h.authAs(automationsMembership(true, false))
		_, _, _, err := h.svc.Decisions(ctx, "ws1", domain.VeridianSendDecisionFilter{Outcome: "exploded"})
		require.Error(t, err)
		assert.True(t, strings.HasPrefix(err.Error(), "invalid "))
		assert.Equal(t, 0, h.decisions.listCalls)
	})

	t.Run("niveau du journal: defaut transitions, normalise", func(t *testing.T) {
		for stored, want := range map[string]string{
			"":                            domain.VeridianDecisionLogTransitions,
			"n'importe quoi":              domain.VeridianDecisionLogTransitions,
			domain.VeridianDecisionLogOff: domain.VeridianDecisionLogOff,
			domain.VeridianDecisionLogAll: domain.VeridianDecisionLogAll,
		} {
			h := newExplainHarness(t)
			h.authAs(automationsMembership(true, false))
			h.wsRepo.EXPECT().GetByID(gomock.Any(), "ws1").Return(&domain.Workspace{
				ID: "ws1", Settings: domain.WorkspaceSettings{VeridianDecisionLogLevel: stored},
			}, nil)
			_, _, level, err := h.svc.Decisions(ctx, "ws1", domain.VeridianSendDecisionFilter{})
			require.NoError(t, err)
			assert.Equal(t, want, level, "niveau stocke %q", stored)
		}
	})

	t.Run("workspace illisible -> niveau par defaut, pas d'erreur", func(t *testing.T) {
		h := newExplainHarness(t)
		h.authAs(automationsMembership(true, false))
		h.wsRepo.EXPECT().GetByID(gomock.Any(), "ws1").Return(nil, errors.New("boom"))
		_, _, level, err := h.svc.Decisions(ctx, "ws1", domain.VeridianSendDecisionFilter{})
		require.NoError(t, err)
		assert.Equal(t, domain.VeridianDecisionLogTransitions, level)
	})

	t.Run("erreur du journal -> enveloppee", func(t *testing.T) {
		h := newExplainHarness(t)
		h.authAs(automationsMembership(true, false))
		h.workspaceWithProfiles()
		h.decisions.listErr = errors.New("db down")
		_, _, _, err := h.svc.Decisions(ctx, "ws1", domain.VeridianSendDecisionFilter{})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "failed to list send decisions")
	})
}

func TestVeridianQueueExplainService_Recompute(t *testing.T) {
	ctx := context.Background()

	t.Run("requetes non bornees refusees AVANT toute authentification", func(t *testing.T) {
		bad := map[string]domain.VeridianQueueRecomputeRequest{
			"sans workspace":            {AutomationID: "a1", Limit: 10},
			"sans filtre (tout le ws)":  {WorkspaceID: "ws1", NodeID: "j0a", Reason: "window_closed", Limit: 10},
			"limite nulle":              {WorkspaceID: "ws1", AutomationID: "a1"},
			"limite negative":           {WorkspaceID: "ws1", AutomationID: "a1", Limit: -1},
			"limite au-dela du plafond": {WorkspaceID: "ws1", AutomationID: "a1", Limit: domain.VeridianQueueRecomputeMaxLimit + 1},
		}
		for name, req := range bad {
			t.Run(name, func(t *testing.T) {
				h := newExplainHarness(t) // pas d'attente auth: un appel ferait echouer
				_, err := h.svc.Recompute(ctx, req)
				var invalid domain.ErrVeridianQueueRecompute
				require.ErrorAs(t, err, &invalid)
				assert.Equal(t, 0, h.explain.recomputeCall)
			})
		}
	})

	t.Run("bornes acceptees: limite 1 et 5000, entry_ids sans automation", func(t *testing.T) {
		for _, req := range []domain.VeridianQueueRecomputeRequest{
			{WorkspaceID: "ws1", AutomationID: "a1", Limit: 1},
			{WorkspaceID: "ws1", AutomationID: "a1", Limit: domain.VeridianQueueRecomputeMaxLimit},
			{WorkspaceID: "ws1", EntryIDs: []string{"e1"}, Limit: 5},
		} {
			h := newExplainHarness(t)
			h.authAs(automationsMembership(false, true))
			_, err := h.svc.Recompute(ctx, req)
			require.NoError(t, err)
			assert.Equal(t, 1, h.explain.recomputeCall)
		}
	})

	t.Run("succes: requete transmise, compte rendu, UNE ligne de journal pour N entrees", func(t *testing.T) {
		h := newExplainHarness(t)
		h.authAs(automationsMembership(false, true))
		h.explain.recomputeIDs = []string{"e1", "e2", "e3"}
		req := domain.VeridianQueueRecomputeRequest{WorkspaceID: "ws1", AutomationID: "a1", NodeID: "j0a", Reason: "window_closed", ProfileID: "p1", Limit: 100}

		n, err := h.svc.Recompute(ctx, req)
		require.NoError(t, err)
		assert.Equal(t, 3, n)
		assert.Equal(t, req, h.explain.gotRecompute)
		require.Len(t, h.decisions.inserted, 1, "une ligne resume l'action, jamais une par entree")
		d := h.decisions.inserted[0]
		assert.Equal(t, domain.VeridianOutcomeRecomputed, d.Outcome)
		assert.Equal(t, "e1", d.EntryID)
		assert.Equal(t, "a1", d.AutomationID)
		assert.Equal(t, "j0a", d.NodeID)
		assert.Equal(t, "window_closed", d.Reason)
		assert.Contains(t, d.Detail, "recomputed 3 entries")
		assert.NotEmpty(t, d.ID)
		assert.False(t, d.At.IsZero())
	})

	t.Run("aucune entree touchee -> 0 et rien de journalise", func(t *testing.T) {
		h := newExplainHarness(t)
		h.authAs(automationsMembership(false, true))
		n, err := h.svc.Recompute(ctx, domain.VeridianQueueRecomputeRequest{WorkspaceID: "ws1", AutomationID: "a1", Limit: 10})
		require.NoError(t, err)
		assert.Equal(t, 0, n)
		assert.Empty(t, h.decisions.inserted)
	})

	t.Run("detail du journal tronque a 190 caracteres", func(t *testing.T) {
		h := newExplainHarness(t)
		h.authAs(automationsMembership(false, true))
		h.explain.recomputeIDs = []string{"e1"}
		_, err := h.svc.Recompute(ctx, domain.VeridianQueueRecomputeRequest{
			WorkspaceID: "ws1", AutomationID: strings.Repeat("a", 300), Limit: 10})
		require.NoError(t, err)
		require.Len(t, h.decisions.inserted, 1)
		assert.LessOrEqual(t, len(h.decisions.inserted[0].Detail), 190)
	})

	t.Run("echec du journal -> le recalcul reste un succes", func(t *testing.T) {
		h := newExplainHarness(t)
		h.authAs(automationsMembership(false, true))
		h.explain.recomputeIDs = []string{"e1"}
		h.decisions.insertErr = errors.New("journal HS")
		n, err := h.svc.Recompute(ctx, domain.VeridianQueueRecomputeRequest{WorkspaceID: "ws1", AutomationID: "a1", Limit: 10})
		require.NoError(t, err)
		assert.Equal(t, 1, n)
	})

	t.Run("erreur du depot -> enveloppee, rien de journalise", func(t *testing.T) {
		h := newExplainHarness(t)
		h.authAs(automationsMembership(false, true))
		h.explain.recomputeErr = errors.New("db down")
		_, err := h.svc.Recompute(ctx, domain.VeridianQueueRecomputeRequest{WorkspaceID: "ws1", AutomationID: "a1", Limit: 10})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "failed to recompute queue")
		assert.Empty(t, h.decisions.inserted)
	})
}

func TestVeridianProfileName(t *testing.T) {
	ws := &domain.Workspace{Integrations: []domain.Integration{{ID: "p1", Name: "Relais nord"}, {ID: "p2"}}}
	assert.Equal(t, "Relais nord", veridianProfileName(ws, "p1"))
	assert.Equal(t, "", veridianProfileName(ws, "p2"), "profil sans nom")
	assert.Equal(t, "", veridianProfileName(ws, "absent"))
	assert.Equal(t, "", veridianProfileName(ws, ""))
	assert.Equal(t, "", veridianProfileName(nil, "p1"))
}
