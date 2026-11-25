package service

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"testing"

	"github.com/golang/mock/gomock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/Notifuse/notifuse/config"
	"github.com/Notifuse/notifuse/internal/domain"
	"github.com/Notifuse/notifuse/internal/domain/mocks"
	pkgmocks "github.com/Notifuse/notifuse/pkg/mocks"
)

// Le rôle « admin » d'un espace (VER-263, fiche 95) : tous les droits d'un propriétaire SAUF supprimer l'espace, transférer la propriété,
// retirer ou modifier un propriétaire. Ces tests posent un appelant propriétaire, admin, ou membre AVEC FullPermissions (le piège : un membre à
// tous droits, ou une clé API, n'est pas un administrateur), puis jouent chaque méthode réservée. Pour « autorisé », il suffit que l'appel
// dépasse le contrôle de rôle : l'étape suivante est un mock qui échoue avec errStop. Pour « refusé », AUCUNE expectation n'est posée après le
// contrôle : un appel de plus fait échouer gomock.

var errStop = errors.New("stop apres le controle de role")

// roleLedgerRepo : le dépôt des espaces avec le registre des admins (ce que fait le dépôt réel, repository/veridian_workspace_admin.go).
type roleLedgerRepo struct {
	*mocks.MockWorkspaceRepository
	granted, withdrawn bool
	err                error
	grantedCalls       int
	withdrawnCalls     int
}

func (r *roleLedgerRepo) VeridianAdminGranted(ctx context.Context, workspaceID, userID string) (bool, error) {
	r.grantedCalls++
	return r.granted, r.err
}

func (r *roleLedgerRepo) VeridianAdminWithdrawn(ctx context.Context, workspaceID, userID string) (bool, error) {
	r.withdrawnCalls++
	return r.withdrawn, r.err
}

var _ domain.VeridianAdminLedger = (*roleLedgerRepo)(nil)

type roleHarness struct {
	t        *testing.T
	s        *WorkspaceService
	repo     *mocks.MockWorkspaceRepository
	ledger   *roleLedgerRepo
	auth     *mocks.MockAuthService
	userSvc  *mocks.MockUserServiceInterface
	userRepo *mocks.MockUserRepository
	user     *domain.User
	uw       *domain.UserWorkspace
	reached  int
}

func roleWorkspace(role string) *domain.UserWorkspace {
	uw := &domain.UserWorkspace{UserID: "req", WorkspaceID: "ws", Role: role}
	if role == domain.VeridianRoleMember {
		uw.Permissions = domain.FullPermissions // le piège : un membre à tous droits (ou une clé API) n'administre pas
	}
	return uw
}

func newRoleHarness(t *testing.T, role string, withLedger bool) *roleHarness {
	t.Helper()
	ctrl := gomock.NewController(t)
	h := &roleHarness{t: t}
	h.repo = mocks.NewMockWorkspaceRepository(ctrl)
	h.ledger = &roleLedgerRepo{MockWorkspaceRepository: h.repo}
	h.auth = mocks.NewMockAuthService(ctrl)
	h.userSvc = mocks.NewMockUserServiceInterface(ctrl)
	h.userRepo = mocks.NewMockUserRepository(ctrl)
	log := pkgmocks.NewMockLogger(ctrl)
	log.EXPECT().WithField(gomock.Any(), gomock.Any()).Return(log).AnyTimes()
	log.EXPECT().WithFields(gomock.Any()).Return(log).AnyTimes()
	log.EXPECT().Error(gomock.Any()).AnyTimes()
	log.EXPECT().Info(gomock.Any()).AnyTimes()
	log.EXPECT().Warn(gomock.Any()).AnyTimes()
	log.EXPECT().Debug(gomock.Any()).AnyTimes()
	h.user = &domain.User{ID: "req", Email: "req@x.test", Type: domain.UserTypeUser}
	h.uw = roleWorkspace(role)
	h.auth.EXPECT().AuthenticateUserForWorkspace(gomock.Any(), "ws").Return(context.Background(), h.user, h.uw, nil).AnyTimes()
	h.auth.EXPECT().AuthenticateUserFromContext(gomock.Any()).Return(h.user, nil).AnyTimes()
	h.repo.EXPECT().GetUserWorkspace(gomock.Any(), "req", "ws").Return(h.uw, nil).AnyTimes()
	var repo domain.WorkspaceRepository = h.repo
	if withLedger {
		repo = h.ledger
	}
	h.s = &WorkspaceService{repo: repo, userRepo: h.userRepo, userService: h.userSvc, authService: h.auth, logger: log,
		config: &config.Config{APIEndpoint: "https://api.test"}}
	return h
}

func denied(err error) bool {
	if err == nil {
		return false
	}
	var unauth *domain.ErrUnauthorized
	if errors.As(err, &unauth) {
		return true
	}
	m := err.Error()
	return strings.Contains(m, "only workspace owners can manage user permissions") || strings.Contains(m, "cannot modify permissions for workspace owners")
}

type roleAccess struct{ owner, admin, member bool }

type roleCase struct {
	name      string
	allow     roleAccess
	mustReach bool // l'étape suivante (mock errStop) doit avoir été atteinte
	arrange   func(h *roleHarness)
	call      func(h *roleHarness) error
}

func TestVeridianWorkspaceRole_MethodesReservees(t *testing.T) {
	ctx := context.Background()
	reach := func(h *roleHarness) { h.reached++ }
	member := &domain.UserWorkspace{UserID: "tgt", WorkspaceID: "ws", Role: "member"}
	adminTarget := &domain.UserWorkspace{UserID: "tgt", WorkspaceID: "ws", Role: "admin"}
	ownerTarget := &domain.UserWorkspace{UserID: "tgt", WorkspaceID: "ws", Role: "owner"}
	getByIDStop := func(h *roleHarness) {
		h.repo.EXPECT().GetByID(gomock.Any(), "ws").DoAndReturn(func(context.Context, string) (*domain.Workspace, error) {
			reach(h)
			return nil, errStop
		}).MaxTimes(1)
	}
	targetOf := func(uw *domain.UserWorkspace) func(h *roleHarness) {
		return func(h *roleHarness) {
			h.repo.EXPECT().GetUserWorkspace(gomock.Any(), "tgt", "ws").Return(uw, nil).AnyTimes()
		}
	}
	createUserStop := func(h *roleHarness) {
		h.userRepo.EXPECT().CreateUser(gomock.Any(), gomock.Any()).DoAndReturn(func(context.Context, *domain.User) error {
			reach(h)
			return errStop
		}).MaxTimes(1)
	}
	wsWithIntegrations := &domain.Workspace{ID: "ws", Integrations: []domain.Integration{
		{ID: "i-llm", Type: domain.IntegrationTypeLLM, Name: "llm"},
		{ID: "i-sb", Type: domain.IntegrationTypeSupabase, Name: "sb"},
		{ID: "i-ses", Type: domain.IntegrationTypeEmail, Name: "ses", EmailProvider: domain.EmailProvider{Kind: domain.EmailProviderKindSES}},
	}}
	updateAny := func(h *roleHarness) {
		h.repo.EXPECT().GetByID(gomock.Any(), "ws").Return(wsWithIntegrations, nil).AnyTimes()
		h.repo.EXPECT().Update(gomock.Any(), gomock.Any()).DoAndReturn(func(context.Context, *domain.Workspace) error {
			reach(h)
			return errStop
		}).AnyTimes()
	}
	createInt := func(req domain.CreateIntegrationRequest) func(h *roleHarness) error {
		req.WorkspaceID = "ws"
		req.Name = "n"
		return func(h *roleHarness) error { _, err := h.s.CreateIntegration(ctx, req); return err }
	}
	updateInt := func(id string) func(h *roleHarness) error {
		return func(h *roleHarness) error {
			return h.s.UpdateIntegration(ctx, domain.UpdateIntegrationRequest{WorkspaceID: "ws", IntegrationID: id, Name: "n"})
		}
	}
	all := roleAccess{true, true, true}
	ownerAdmin := roleAccess{true, true, false}
	ownerOnly := roleAccess{true, false, false}
	nobody := roleAccess{false, false, false}

	cases := []roleCase{
		{"UpdateWorkspace", ownerAdmin, true, getByIDStop, func(h *roleHarness) error {
			_, err := h.s.UpdateWorkspace(ctx, "ws", "n", domain.WorkspaceSettings{})
			return err
		}},
		{"SetUserPermissions cible membre", ownerAdmin, true, func(h *roleHarness) {
			targetOf(member)(h)
			h.repo.EXPECT().UpdateUserWorkspacePermissions(gomock.Any(), gomock.Any()).DoAndReturn(func(context.Context, *domain.UserWorkspace) error {
				reach(h)
				return errStop
			}).MaxTimes(1)
		}, func(h *roleHarness) error { return h.s.SetUserPermissions(ctx, "ws", "tgt", domain.UserPermissions{}) }},
		{"SetUserPermissions cible admin", nobody, false, targetOf(adminTarget), func(h *roleHarness) error {
			return h.s.SetUserPermissions(ctx, "ws", "tgt", domain.UserPermissions{})
		}},
		{"SetUserPermissions cible proprietaire", nobody, false, targetOf(ownerTarget), func(h *roleHarness) error {
			return h.s.SetUserPermissions(ctx, "ws", "tgt", domain.UserPermissions{})
		}},
		{"CreateAPIKey", ownerAdmin, true, createUserStop, func(h *roleHarness) error {
			_, _, err := h.s.CreateAPIKey(ctx, "ws", "k")
			return err
		}},
		{"RevokeAPIKey", ownerAdmin, true, func(h *roleHarness) {
			h.userSvc.EXPECT().GetUserByID(gomock.Any(), "key").DoAndReturn(func(context.Context, string) (*domain.User, error) {
				reach(h)
				return nil, errStop
			}).MaxTimes(1)
		}, func(h *roleHarness) error { return h.s.RevokeAPIKey(ctx, "ws", "key") }},
		{"CreateAgentInstallToken", ownerAdmin, true, func(h *roleHarness) {
			h.s.agentInstallTokenRepo = mocks.NewMockAgentInstallTokenRepository(gomock.NewController(h.t))
			createUserStop(h)
		}, func(h *roleHarness) error {
			_, _, err := h.s.CreateAgentInstallToken(ctx, "ws")
			return err
		}},
		{"InviteMember", ownerAdmin, true, getByIDStop, func(h *roleHarness) error {
			_, _, err := h.s.InviteMember(ctx, "ws", "new@x.test", domain.UserPermissions{})
			return err
		}},
		{"DeleteInvitation", ownerAdmin, true, func(h *roleHarness) {
			h.repo.EXPECT().GetInvitationByID(gomock.Any(), "inv").Return(&domain.WorkspaceInvitation{ID: "inv", WorkspaceID: "ws", Email: "n@x.test"}, nil).AnyTimes()
			h.repo.EXPECT().DeleteInvitation(gomock.Any(), "inv").DoAndReturn(func(context.Context, string) error {
				reach(h)
				return errStop
			}).MaxTimes(1)
		}, func(h *roleHarness) error { return h.s.DeleteInvitation(ctx, "inv") }},
		{"RemoveMember cible membre", ownerAdmin, true, func(h *roleHarness) {
			targetOf(member)(h)
			h.userSvc.EXPECT().GetUserByID(gomock.Any(), "tgt").DoAndReturn(func(context.Context, string) (*domain.User, error) {
				reach(h)
				return nil, errStop
			}).MaxTimes(1)
		}, func(h *roleHarness) error { return h.s.RemoveMember(ctx, "ws", "tgt") }},
		{"RemoveMember cible admin", ownerAdmin, true, func(h *roleHarness) {
			targetOf(adminTarget)(h)
			h.userSvc.EXPECT().GetUserByID(gomock.Any(), "tgt").DoAndReturn(func(context.Context, string) (*domain.User, error) {
				reach(h)
				return nil, errStop
			}).MaxTimes(1)
		}, func(h *roleHarness) error { return h.s.RemoveMember(ctx, "ws", "tgt") }},
		{"RemoveMember cible proprietaire", ownerOnly, true, func(h *roleHarness) {
			targetOf(ownerTarget)(h)
			h.userSvc.EXPECT().GetUserByID(gomock.Any(), "tgt").DoAndReturn(func(context.Context, string) (*domain.User, error) {
				reach(h)
				return nil, errStop
			}).MaxTimes(1)
		}, func(h *roleHarness) error { return h.s.RemoveMember(ctx, "ws", "tgt") }},
		// Intégrations : un membre qui a workspace:write gère encore SMTP et IMAP (fiche 79, inchangé) ; tout le reste est à l'administrateur.
		{"CreateIntegration smtp", all, true, getByIDStop, createInt(domain.CreateIntegrationRequest{Type: domain.IntegrationTypeEmail, Provider: domain.EmailProvider{Kind: domain.EmailProviderKindSMTP}})},
		{"CreateIntegration imap", all, true, getByIDStop, createInt(domain.CreateIntegrationRequest{Type: domain.IntegrationTypeIMAP})},
		{"CreateIntegration llm", ownerAdmin, true, getByIDStop, createInt(domain.CreateIntegrationRequest{Type: domain.IntegrationTypeLLM})},
		{"CreateIntegration supabase", ownerAdmin, true, getByIDStop, createInt(domain.CreateIntegrationRequest{Type: domain.IntegrationTypeSupabase})},
		{"CreateIntegration SES", ownerAdmin, true, getByIDStop, createInt(domain.CreateIntegrationRequest{Type: domain.IntegrationTypeEmail, Provider: domain.EmailProvider{Kind: domain.EmailProviderKindSES}})},
		{"UpdateIntegration llm", ownerAdmin, false, updateAny, updateInt("i-llm")},
		{"UpdateIntegration supabase", ownerAdmin, false, updateAny, updateInt("i-sb")},
		{"UpdateIntegration SES", ownerAdmin, false, updateAny, updateInt("i-ses")},
		{"DeleteIntegration", ownerAdmin, true, getByIDStop, func(h *roleHarness) error { return h.s.DeleteIntegration(ctx, "ws", "i1") }},
		{"DeleteWorkspace", ownerOnly, true, getByIDStop, func(h *roleHarness) error { return h.s.DeleteWorkspace(ctx, "ws") }},
		{"TransferOwnership", ownerOnly, true, func(h *roleHarness) {
			dest := &domain.UserWorkspace{UserID: "dest", WorkspaceID: "ws", Role: "member"}
			h.repo.EXPECT().GetUserWorkspace(gomock.Any(), "dest", "ws").Return(dest, nil).AnyTimes()
			h.repo.EXPECT().AddUserToWorkspace(gomock.Any(), gomock.Any()).DoAndReturn(func(context.Context, *domain.UserWorkspace) error {
				reach(h)
				return errStop
			}).MaxTimes(1)
		}, func(h *roleHarness) error { return h.s.TransferOwnership(ctx, "ws", "dest", "req") }},
		{"AddUserToWorkspace (interne)", ownerOnly, true, func(h *roleHarness) {
			h.repo.EXPECT().AddUserToWorkspace(gomock.Any(), gomock.Any()).DoAndReturn(func(context.Context, *domain.UserWorkspace) error {
				reach(h)
				return errStop
			}).MaxTimes(1)
		}, func(h *roleHarness) error {
			return h.s.AddUserToWorkspace(ctx, "ws", "u2", "member", domain.FullPermissions)
		}},
		{"RemoveUserFromWorkspace (interne)", ownerOnly, true, func(h *roleHarness) {
			h.repo.EXPECT().RemoveUserFromWorkspace(gomock.Any(), "u2", "ws").DoAndReturn(func(context.Context, string, string) error {
				reach(h)
				return errStop
			}).MaxTimes(1)
		}, func(h *roleHarness) error { return h.s.RemoveUserFromWorkspace(ctx, "ws", "u2") }},
	}

	for _, c := range cases {
		for role, allowed := range map[string]bool{"owner": c.allow.owner, "admin": c.allow.admin, "member": c.allow.member} {
			t.Run(c.name+"/"+role, func(t *testing.T) {
				h := newRoleHarness(t, role, true)
				c.arrange(h)
				err := c.call(h)
				require.Error(t, err)
				if allowed {
					assert.False(t, denied(err), "le rôle %s doit passer le contrôle : %v", role, err)
					if c.mustReach {
						assert.True(t, errors.Is(err, errStop), "l'appel doit atteindre l'étape suivante : %v", err)
						assert.Equal(t, 1, h.reached)
					}
				} else {
					assert.True(t, denied(err), "le rôle %s doit être refusé : %v", role, err)
					assert.Equal(t, 0, h.reached, "rien ne s'exécute après un refus")
				}
			})
		}
	}
}

// Admin : retirer un propriétaire refusé, cible illisible refusée (rien ne s'exécute), s'enlever soi-même refusé.
func TestWorkspaceService_RemoveMember_AdminCibleIllisibleEtSoiMeme(t *testing.T) {
	ctx := context.Background()
	h := newRoleHarness(t, "admin", true)
	h.repo.EXPECT().GetUserWorkspace(gomock.Any(), "ghost", "ws").Return(nil, sql.ErrNoRows).Times(1)
	err := h.s.RemoveMember(ctx, "ws", "ghost")
	assert.True(t, denied(err), "cible illisible : refus, jamais un retrait à l'aveugle : %v", err)

	h = newRoleHarness(t, "admin", true)
	h.repo.EXPECT().GetUserWorkspace(gomock.Any(), "req", "ws").Return(h.uw, nil).AnyTimes()
	err = h.s.RemoveMember(ctx, "ws", "req")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "cannot remove yourself")

	// le propriétaire n'a pas besoin de lire la cible pour le contrôle de rôle
	o := newRoleHarness(t, "owner", true)
	o.userSvc.EXPECT().GetUserByID(gomock.Any(), "ghost").Return(nil, errStop).Times(1)
	assert.ErrorIs(t, o.s.RemoveMember(ctx, "ws", "ghost"), errStop)
}

// Un administrateur retire un autre admin : le membre est retiré, jamais un propriétaire.
func TestWorkspaceService_RemoveMember_AdminRetireUnMembre_Ecrit(t *testing.T) {
	h := newRoleHarness(t, "admin", true)
	h.repo.EXPECT().GetUserWorkspace(gomock.Any(), "tgt", "ws").Return(&domain.UserWorkspace{UserID: "tgt", WorkspaceID: "ws", Role: "member"}, nil).AnyTimes()
	h.userSvc.EXPECT().GetUserByID(gomock.Any(), "tgt").Return(&domain.User{ID: "tgt", Type: domain.UserTypeUser}, nil).Times(1)
	h.repo.EXPECT().RemoveUserFromWorkspace(gomock.Any(), "tgt", "ws").Return(nil).Times(1)
	assert.NoError(t, h.s.RemoveMember(context.Background(), "ws", "tgt"))
}

func TestWorkspaceService_VeridianMayRemoveMember(t *testing.T) {
	ctx := context.Background()
	h := newRoleHarness(t, "owner", true)
	h.repo.EXPECT().GetUserWorkspace(gomock.Any(), "m", "ws").Return(&domain.UserWorkspace{Role: "member"}, nil).AnyTimes()
	h.repo.EXPECT().GetUserWorkspace(gomock.Any(), "a", "ws").Return(&domain.UserWorkspace{Role: "admin"}, nil).AnyTimes()
	h.repo.EXPECT().GetUserWorkspace(gomock.Any(), "o", "ws").Return(&domain.UserWorkspace{Role: "owner"}, nil).AnyTimes()
	h.repo.EXPECT().GetUserWorkspace(gomock.Any(), "nil", "ws").Return(nil, nil).AnyTimes()
	owner, admin, member := roleWorkspace("owner"), roleWorkspace("admin"), roleWorkspace("member")
	for _, tgt := range []string{"m", "a", "o", "nil", "inconnu"} {
		assert.True(t, h.s.veridianMayRemoveMember(ctx, owner, tgt), "le propriétaire retire "+tgt)
		assert.False(t, h.s.veridianMayRemoveMember(ctx, member, tgt), "le membre ne retire personne : "+tgt)
	}
	assert.True(t, h.s.veridianMayRemoveMember(ctx, admin, "m"))
	assert.True(t, h.s.veridianMayRemoveMember(ctx, admin, "a"))
	assert.False(t, h.s.veridianMayRemoveMember(ctx, admin, "o"), "jamais un propriétaire")
	assert.False(t, h.s.veridianMayRemoveMember(ctx, admin, "nil"), "cible vide : refus")
}

// === Transfert de propriété ================================================

type transferRecord struct{ calls []string }

func arrangeTransfer(h *roleHarness, destRole string, rec *transferRecord) {
	dest := &domain.UserWorkspace{UserID: "dest", WorkspaceID: "ws", Role: destRole}
	h.repo.EXPECT().GetUserWorkspace(gomock.Any(), "dest", "ws").Return(dest, nil).AnyTimes()
	h.repo.EXPECT().AddUserToWorkspace(gomock.Any(), gomock.Any()).DoAndReturn(func(_ context.Context, uw *domain.UserWorkspace) error {
		rec.calls = append(rec.calls, uw.UserID+"="+uw.Role)
		return nil
	}).AnyTimes()
}

func TestTransferOwnership_AncienProprietaire_AdminSiDesigne(t *testing.T) {
	ctx := context.Background()
	for name, c := range map[string]struct {
		granted bool
		err     error
		ledger  bool
		want    string
	}{
		"désigné admin : il devient admin sans coupure": {true, nil, true, "req=admin"},
		"jamais désigné : membre":                       {false, nil, true, "req=member"},
		"registre illisible : membre":                   {true, errors.New("db down"), true, "req=member"},
		"dépôt sans registre : membre":                  {false, nil, false, "req=member"},
	} {
		h := newRoleHarness(t, "owner", c.ledger)
		h.ledger.granted, h.ledger.err = c.granted, c.err
		rec := &transferRecord{}
		arrangeTransfer(h, "member", rec)
		require.NoError(t, h.s.TransferOwnership(ctx, "ws", "dest", "req"), name)
		assert.Equal(t, []string{"dest=owner", c.want}, rec.calls, name)
	}
}

func TestTransferOwnership_AdminPeutEtreLeDestinataire(t *testing.T) {
	h := newRoleHarness(t, "owner", true)
	rec := &transferRecord{}
	arrangeTransfer(h, "admin", rec)
	require.NoError(t, h.s.TransferOwnership(context.Background(), "ws", "dest", "req"))
	assert.Equal(t, []string{"dest=owner", "req=member"}, rec.calls)
}

func TestTransferOwnership_DestinataireDejaProprietaire_Refuse(t *testing.T) {
	h := newRoleHarness(t, "owner", true)
	rec := &transferRecord{}
	arrangeTransfer(h, "owner", rec)
	err := h.s.TransferOwnership(context.Background(), "ws", "dest", "req")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "must be a current member")
	assert.Empty(t, rec.calls, "aucune écriture")
}

func TestTransferOwnership_AdminCommeAncienProprietaire_Refuse(t *testing.T) {
	h := newRoleHarness(t, "admin", true)
	rec := &transferRecord{}
	arrangeTransfer(h, "member", rec)
	err := h.s.TransferOwnership(context.Background(), "ws", "dest", "req")
	assert.True(t, denied(err), "%v", err)
	assert.Empty(t, rec.calls)
	assert.Equal(t, 0, h.ledger.grantedCalls)
}

func TestVeridianRoleAfterTransfer(t *testing.T) {
	ctx := context.Background()
	former := &domain.UserWorkspace{UserID: "u", WorkspaceID: "ws", Role: "owner"}
	h := newRoleHarness(t, "owner", true)
	h.ledger.granted = true
	assert.Equal(t, "admin", h.s.veridianRoleAfterTransfer(ctx, former))
	h.ledger.granted = false
	assert.Equal(t, "member", h.s.veridianRoleAfterTransfer(ctx, former))
	h.ledger.granted, h.ledger.err = true, errors.New("boom")
	assert.Equal(t, "member", h.s.veridianRoleAfterTransfer(ctx, former), "en cas de doute, jamais plus de droits")
}

// === Registre : SyncMember, AttachMember, provision rejouée ================

func withLedger(svc *veridianService, m *veridianServiceMocks) *roleLedgerRepo {
	led := &roleLedgerRepo{MockWorkspaceRepository: m.workspaceRepo}
	svc.workspaceRepo = led
	return led
}

func attachInput(invitation string) domain.AttachMemberInput {
	return domain.AttachMemberInput{TenantID: "ws-1", HubUserID: testHubUID, HubUserEmail: "alice@example.com", Role: domain.AttachMemberRoleMember, InvitationID: invitation}
}

func arrangeAttachUntilLedger(m *veridianServiceMocks, ctx context.Context) {
	m.workspaceRepo.EXPECT().GetByID(ctx, "ws-1").Return(&domain.Workspace{ID: "ws-1"}, nil)
	m.planRepo.EXPECT().Get(ctx, "ws-1").Return(nil, sql.ErrNoRows)
	m.userRepo.EXPECT().GetUserByHubUserID(ctx, testHubUID).Return(membershipMemberUser(), nil)
	m.workspaceRepo.EXPECT().GetUserWorkspace(ctx, "member-uuid-1", "ws-1").Return(nil, sql.ErrNoRows)
}

func TestAttachMember_AdminRetire_SansInvitation_Refuse_AucunAjout(t *testing.T) {
	svc, m := newVeridianService(t)
	ctx := context.Background()
	led := withLedger(svc, m)
	led.withdrawn = true
	arrangeAttachUntilLedger(m, ctx)

	resp, err := svc.AttachMember(ctx, attachInput(""))
	assert.Nil(t, resp)
	assert.ErrorIs(t, err, ErrAdminWithdrawn)
	assert.Equal(t, 1, led.withdrawnCalls)
}

func TestAttachMember_RegistreEnErreur_SansInvitation_PasDAjout(t *testing.T) {
	svc, m := newVeridianService(t)
	ctx := context.Background()
	led := withLedger(svc, m)
	led.err = errors.New("db down")
	arrangeAttachUntilLedger(m, ctx)

	_, err := svc.AttachMember(ctx, attachInput(""))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "read admin ledger")
}

// Une invitation acceptée est le geste de l'espace : elle ramène la personne, comme membre, sans même lire le registre.
func TestAttachMember_AdminRetire_AvecInvitation_Passe(t *testing.T) {
	svc, m := newVeridianService(t)
	ctx := context.Background()
	led := withLedger(svc, m)
	led.withdrawn = true
	arrangeAttachUntilLedger(m, ctx)
	m.workspaceRepo.EXPECT().GetWorkspaceUsersWithEmail(ctx, "ws-1").
		Return([]*domain.UserWorkspaceWithEmail{membershipOwnerWithEmail("owner-1", "owner@x.test")}, nil)
	m.userRepo.EXPECT().CreateSession(systemCall{}, gomock.Any()).Return(nil)
	m.workspace.EXPECT().AddUserToWorkspace(gomock.Any(), "ws-1", "member-uuid-1", "member", gomock.Any()).Return(nil).Times(1)
	m.emitter.EXPECT().Emit(ctx, domain.EventTenantMemberAdded, "ws-1", gomock.Any()).Times(1)
	m.userRepo.EXPECT().DeleteSession(ctx, gomock.Any()).Return(nil)

	resp, err := svc.AttachMember(ctx, attachInput("inv-1"))
	require.NoError(t, err)
	assert.True(t, resp.Attached)
	assert.Equal(t, "member", resp.Role, "jamais admin par ce chemin")
	assert.Equal(t, 0, led.withdrawnCalls)
}

// === attach-owner : jamais de transfert silencieux ==========================

func ownerRow(userID, email string, kind domain.UserType) *domain.UserWorkspaceWithEmail {
	return &domain.UserWorkspaceWithEmail{
		UserWorkspace: domain.UserWorkspace{UserID: userID, WorkspaceID: "ws-multi", Role: "owner"},
		Email:         email, Type: kind,
	}
}

func TestAttachOwner_AutreProprietaireHumain_OwnerExists_AucuneEcriture(t *testing.T) {
	for name, tc := range map[string]struct {
		existingNewOwner bool
	}{
		"nouveau propriétaire déjà compte ici": {true},
		"nouveau propriétaire sans compte ici": {false},
	} {
		svc, m := newVeridianService(t)
		ctx := context.Background()
		m.workspaceRepo.EXPECT().GetByID(ctx, "ws-multi").Return(&domain.Workspace{ID: "ws-multi"}, nil).Times(1)
		if tc.existingNewOwner {
			m.user.EXPECT().GetUserByEmail(ctx, "bob@x.test").Return(&domain.User{ID: "bob-id", Email: "bob@x.test", Type: domain.UserTypeUser}, nil).Times(1)
		} else {
			m.user.EXPECT().GetUserByEmail(ctx, "bob@x.test").Return(nil, &domain.ErrUserNotFound{Message: "nf"}).Times(1)
		}
		m.workspaceRepo.EXPECT().GetWorkspaceUsersWithEmail(ctx, "ws-multi").Return([]*domain.UserWorkspaceWithEmail{
			ownerRow("alice-id", "alice@x.test", domain.UserTypeUser),
			ownerRow("key-id", "k@api.test", domain.UserTypeAPIKey),
		}, nil).Times(1)
		// Aucun CreateUser, GetUserWorkspace, session, AddUserToWorkspace, TransferOwnership, event : gomock échoue sur tout appel.

		resp, err := svc.AttachOwner(ctx, domain.AttachOwnerInput{TenantID: "ws-multi", OwnerEmail: "bob@x.test"})
		assert.Nil(t, resp, name)
		assert.ErrorIs(t, err, ErrOwnerExists, name)
	}
}

func TestRefuseSilentTransfer(t *testing.T) {
	ctx := context.Background()
	input := func(transfer bool) domain.AttachOwnerInput {
		return domain.AttachOwnerInput{TenantID: "ws-multi", OwnerEmail: "bob@x.test", Transfer: transfer}
	}
	bob := &domain.User{ID: "bob-id", Email: "bob@x.test"}
	for name, tc := range map[string]struct {
		transfer bool
		members  []*domain.UserWorkspaceWithEmail
		target   *domain.User
		listErr  error
		want     error
		listed   bool
	}{
		"transfer explicite : aucune lecture":    {true, nil, bob, nil, nil, false},
		"autre propriétaire humain":              {false, []*domain.UserWorkspaceWithEmail{ownerRow("a", "alice@x.test", domain.UserTypeUser)}, bob, nil, ErrOwnerExists, true},
		"déjà propriétaire : rien à transférer":  {false, []*domain.UserWorkspaceWithEmail{ownerRow("bob-id", "bob@x.test", domain.UserTypeUser), ownerRow("a", "alice@x.test", domain.UserTypeUser)}, bob, nil, nil, true},
		"compte racine ne compte pas":            {false, []*domain.UserWorkspaceWithEmail{ownerRow("r", "root@veridian.site", domain.UserTypeUser)}, bob, nil, nil, true},
		"copropriétaire configuré ne compte pas": {false, []*domain.UserWorkspaceWithEmail{ownerRow("c", "robert@veridian.site", domain.UserTypeUser)}, bob, nil, nil, true},
		"clé API ne compte pas":                  {false, []*domain.UserWorkspaceWithEmail{ownerRow("k", "k@api.test", domain.UserTypeAPIKey)}, bob, nil, nil, true},
		"un membre ne compte pas":                {false, []*domain.UserWorkspaceWithEmail{{UserWorkspace: domain.UserWorkspace{UserID: "m", Role: "member"}, Email: "m@x.test", Type: domain.UserTypeUser}}, bob, nil, nil, true},
		"cible sans compte, autre propriétaire":  {false, []*domain.UserWorkspaceWithEmail{ownerRow("a", "alice@x.test", domain.UserTypeUser)}, nil, nil, ErrOwnerExists, true},
		"espace sans propriétaire":               {false, nil, bob, nil, nil, true},
		"liste illisible : refus, pas silence":   {false, nil, bob, errors.New("db down"), errors.New("list workspace members"), true},
	} {
		svc, m := newVeridianService(t)
		svc.ConfigureDefaultCoOwner("robert@veridian.site")
		if tc.listed {
			m.workspaceRepo.EXPECT().GetWorkspaceUsersWithEmail(ctx, "ws-multi").Return(tc.members, tc.listErr).Times(1)
		}
		err := svc.refuseSilentTransfer(ctx, input(tc.transfer), tc.target)
		switch {
		case tc.want == nil:
			assert.NoError(t, err, name)
		case errors.Is(tc.want, ErrOwnerExists):
			assert.ErrorIs(t, err, ErrOwnerExists, name)
		default:
			require.Error(t, err, name)
			assert.Contains(t, err.Error(), tc.want.Error(), name)
		}
	}
}

// Le transfert voulu passe toujours : TransferOwner (route transfer-owner) envoie Transfer:true à AttachOwner.
func TestAttachOwner_TransferExplicite_Passe(t *testing.T) {
	svc, m := newVeridianService(t)
	ctx := context.Background()
	bob := &domain.User{ID: "bob-id", Email: "bob@x.test", Type: domain.UserTypeUser}
	rootUser := &domain.User{ID: "root-id", Email: "root@veridian.site", Type: domain.UserTypeUser}
	m.workspaceRepo.EXPECT().GetByID(ctx, "ws-multi").Return(&domain.Workspace{ID: "ws-multi"}, nil).Times(1)
	m.user.EXPECT().GetUserByEmail(ctx, "bob@x.test").Return(bob, nil).Times(1)
	m.workspaceRepo.EXPECT().GetUserWorkspace(ctx, "bob-id", "ws-multi").Return(nil, sql.ErrNoRows).Times(1)
	m.workspaceRepo.EXPECT().GetWorkspaceUsersWithEmail(ctx, "ws-multi").Return([]*domain.UserWorkspaceWithEmail{
		ownerRow("alice-id", "alice@x.test", domain.UserTypeUser)}, nil).Times(1)
	m.userRepo.EXPECT().CreateSession(gomock.Any(), gomock.Any()).Return(nil).Times(1)
	m.userRepo.EXPECT().DeleteSession(gomock.Any(), gomock.Any()).Return(nil).Times(1)
	m.workspace.EXPECT().AddUserToWorkspace(gomock.Any(), "ws-multi", "bob-id", "member", gomock.Any()).Return(nil).Times(1)
	m.workspace.EXPECT().TransferOwnership(gomock.Any(), "ws-multi", "bob-id", "alice-id").Return(nil).Times(1)
	m.userRepo.EXPECT().GetUserByEmail(gomock.Any(), "root@veridian.site").Return(rootUser, nil).Times(1)
	m.emitter.EXPECT().Emit(gomock.Any(), domain.EventTenantOwnerChanged, "ws-multi", gomock.Any()).Times(1)

	resp, err := svc.AttachOwner(ctx, domain.AttachOwnerInput{TenantID: "ws-multi", OwnerEmail: "bob@x.test", Transfer: true})
	require.NoError(t, err)
	assert.True(t, resp.OwnerTransferred)
}

func TestRefuseWithdrawnAdmin(t *testing.T) {
	ctx := context.Background()
	svc, m := newVeridianService(t)
	// dépôt sans registre : rien à lire, aucune restriction
	assert.NoError(t, svc.refuseWithdrawnAdmin(ctx, "ws-1", "u"))
	led := withLedger(svc, m)
	assert.NoError(t, svc.refuseWithdrawnAdmin(ctx, "ws-1", "u"))
	led.withdrawn = true
	assert.ErrorIs(t, svc.refuseWithdrawnAdmin(ctx, "ws-1", "u"), ErrAdminWithdrawn)
	led.err = errors.New("boom")
	err := svc.refuseWithdrawnAdmin(ctx, "ws-1", "u")
	require.Error(t, err)
	assert.NotErrorIs(t, err, ErrAdminWithdrawn)
	assert.Contains(t, err.Error(), "read admin ledger")
}
