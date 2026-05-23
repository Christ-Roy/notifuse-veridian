package service

import (
	"context"
	"errors"
	"testing"

	"github.com/Notifuse/notifuse/internal/domain"
	"github.com/Notifuse/notifuse/internal/domain/mocks"
	"github.com/Notifuse/notifuse/pkg/logger"
	"github.com/Notifuse/notifuse/pkg/notifuse_mjml"
	"github.com/golang/mock/gomock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ─── Sanity ──────────────────────────────────────────────────────────────────

// TestInvitationProspectionMJMLEmbed_NotEmpty vérifie que le MJML est bien
// embarqué via //go:embed et contient les variables Liquid attendues par
// veridian-prospection/src/lib/notifuse/client.ts. Si quelqu'un supprime
// par mégarde le .mjml ou casse les variables, ce test bloque.
func TestInvitationProspectionMJMLEmbed_NotEmpty(t *testing.T) {
	require.NotEmpty(t, invitationProspectionMJML, "MJML embedded vide — go:embed cassé")
	require.True(t, len(invitationProspectionMJML) > 500, "MJML suspicieusement court")

	for _, mustContain := range []string{
		"{{ inviter_email }}",
		"{{ workspace_name }}",
		"{{ invite_url }}",
		"{{ expires_at }}",
		"<mjml>",
		"</mjml>",
	} {
		assert.Contains(t, invitationProspectionMJML, mustContain,
			"MJML doit contenir %q (variable Liquid contractuelle Prospection)", mustContain)
	}
}

func TestSeedInvitationProspectionTemplateID_Constants(t *testing.T) {
	// Doit matcher exactement ce que Prospection envoie dans notification.id.
	// Si on rename, on casse le caller cross-app.
	assert.Equal(t, "invitation-prospection", SeedInvitationProspectionTemplateID)
	assert.LessOrEqual(t, len(SeedInvitationProspectionTemplateID), 32, "ID dépasse la contrainte template.id (32 chars)")
	assert.LessOrEqual(t, len(seedInvitationProspectionName), 32, "Name dépasse la contrainte template.name (32 chars)")
}

func TestEmptyMJMLRoot(t *testing.T) {
	root := emptyMJMLRoot()
	require.NotNil(t, root)
	assert.Equal(t, notifuse_mjml.MJMLComponentMjml, root.GetType())
	children := root.GetChildren()
	require.NotNil(t, children)
	// On veut head + body — l'EmailTemplate.Validate() exige des children non
	// nil sur le root, même en code mode où le tree n'est pas évalué.
	assert.Len(t, children, 2, "root mjml doit avoir head + body")
}

func TestIsDuplicateErr(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want bool
	}{
		{"nil", nil, false},
		{"random", errors.New("connection refused"), false},
		{"pq duplicate key", errors.New("pq: duplicate key value violates unique constraint"), true},
		{"already exists wrap", errors.New("create template: notification already exists"), true},
		{"uppercase Duplicate", errors.New("Duplicate entry detected"), true},
		{"non-duplicate unrelated", errors.New("template invalid mjml"), false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, isDuplicateErr(tc.err))
		})
	}
}

// ─── ConfigureSeedTemplatesSupport setter ────────────────────────────────────

func TestConfigureSeedTemplatesSupport_HappyPath(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	svc := &veridianService{logger: logger.NewLogger()}
	mockTemplateSvc := mocks.NewMockTemplateService(ctrl)
	txSvc := newTxServiceWithMocks(t, ctrl)

	err := ConfigureSeedTemplatesSupport(svc, mockTemplateSvc, txSvc)
	require.NoError(t, err)
	assert.Equal(t, mockTemplateSvc, svc.templateService)
	assert.Equal(t, txSvc, svc.transactionalNotificationService)
}

func TestConfigureSeedTemplatesSupport_WrongImpl_ReturnsError(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	// Une implémentation custom de VeridianService non-*veridianService
	// (ici un mock gomock qui satisfait l'interface mais n'est pas le
	// concrete struct). ConfigureSeedTemplatesSupport doit refuser.
	notOurImpl := mocks.NewMockVeridianService(ctrl)
	err := ConfigureSeedTemplatesSupport(notOurImpl, mocks.NewMockTemplateService(ctrl), newTxServiceWithMocks(t, ctrl))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not a *veridianService")
}

// ─── seedInvitationProspectionTemplate ───────────────────────────────────────

func TestSeedInvitationProspection_NoServicesConfigured_SilentNoOp(t *testing.T) {
	svc, _ := newVeridianService(t)
	// templateService + transactionalNotificationService restent nil → skip.
	// Doit pas paniquer ni rien faire.
	require.NotPanics(t, func() {
		svc.seedInvitationProspectionTemplate(context.Background(), "ws-1")
	})
}

func TestSeedInvitationProspection_HappyPath_CreatesTemplateAndNotification(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	svc, m := newVeridianService(t)
	mockTemplateSvc := mocks.NewMockTemplateService(ctrl)
	txSvc, txMocks := buildTxService(t, ctrl, mockTemplateSvc)
	require.NoError(t, ConfigureSeedTemplatesSupport(svc, mockTemplateSvc, txSvc))

	rootUser := &domain.User{ID: "root-id", Email: "root@veridian.site", Type: domain.UserTypeUser}
	expectCtxAsRoot(m, rootUser)

	// Template absent → CreateTemplate appelé.
	mockTemplateSvc.EXPECT().
		GetTemplateByID(gomock.Any(), "ws-1", SeedInvitationProspectionTemplateID, int64(0)).
		Return(nil, errors.New("not found"))
	mockTemplateSvc.EXPECT().
		CreateTemplate(gomock.Any(), "ws-1", gomock.AssignableToTypeOf(&domain.Template{})).
		DoAndReturn(func(_ context.Context, _ string, tmpl *domain.Template) error {
			// Sanity : vérifier les invariants du template seedé.
			assert.Equal(t, SeedInvitationProspectionTemplateID, tmpl.ID)
			assert.Equal(t, domain.ChannelEmail, tmpl.Channel)
			require.NotNil(t, tmpl.Email)
			assert.Equal(t, domain.EditorModeCode, tmpl.Email.EditorMode)
			require.NotNil(t, tmpl.Email.MjmlSource)
			assert.NotEmpty(t, *tmpl.Email.MjmlSource)
			assert.Equal(t, invitationProspectionMJML, *tmpl.Email.MjmlSource)
			return nil
		})

	// Notification absente → CreateNotification appelé (via le vrai txSvc
	// avec mocks repo). On force l'auth à passer + repo.Get not found +
	// repo.Create OK.
	expectTxAuthOK(txMocks, "ws-1")
	txMocks.repo.EXPECT().Get(gomock.Any(), "ws-1", SeedInvitationProspectionTemplateID).Return(nil, errors.New("not found"))
	// templateService.GetTemplateByID est appelé une seconde fois par
	// CreateNotification pour valider que le template existe avant l'insert.
	// Le seed a juste créé le template, donc on stub un retour OK.
	mockTemplateSvc.EXPECT().
		GetTemplateByID(gomock.Any(), "ws-1", SeedInvitationProspectionTemplateID, int64(0)).
		Return(&domain.Template{ID: SeedInvitationProspectionTemplateID}, nil)
	txMocks.repo.EXPECT().
		Create(gomock.Any(), "ws-1", gomock.AssignableToTypeOf(&domain.TransactionalNotification{})).
		DoAndReturn(func(_ context.Context, _ string, n *domain.TransactionalNotification) error {
			assert.Equal(t, SeedInvitationProspectionTemplateID, n.ID)
			require.NotEmpty(t, n.Channels)
			assert.Equal(t, SeedInvitationProspectionTemplateID, n.Channels[domain.TransactionalChannelEmail].TemplateID)
			return nil
		})

	svc.seedInvitationProspectionTemplate(context.Background(), "ws-1")
}

func TestSeedInvitationProspection_TemplateExists_OnlyCreatesNotification(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	svc, m := newVeridianService(t)
	mockTemplateSvc := mocks.NewMockTemplateService(ctrl)
	txSvc, txMocks := buildTxService(t, ctrl, mockTemplateSvc)
	require.NoError(t, ConfigureSeedTemplatesSupport(svc, mockTemplateSvc, txSvc))

	rootUser := &domain.User{ID: "root-id", Email: "root@veridian.site", Type: domain.UserTypeUser}
	expectCtxAsRoot(m, rootUser)

	// Template DÉJÀ présent → CreateTemplate NE doit PAS être appelé
	// (préserve la customisation client).
	existingTmpl := &domain.Template{ID: SeedInvitationProspectionTemplateID, DeletedAt: nil}
	mockTemplateSvc.EXPECT().
		GetTemplateByID(gomock.Any(), "ws-1", SeedInvitationProspectionTemplateID, int64(0)).
		Return(existingTmpl, nil)
	// PAS d'EXPECT sur CreateTemplate — gomock le bloquerait si appelé.

	// Notification absente → on continue sur la notification.
	expectTxAuthOK(txMocks, "ws-1")
	txMocks.repo.EXPECT().Get(gomock.Any(), "ws-1", SeedInvitationProspectionTemplateID).Return(nil, errors.New("not found"))
	mockTemplateSvc.EXPECT().
		GetTemplateByID(gomock.Any(), "ws-1", SeedInvitationProspectionTemplateID, int64(0)).
		Return(existingTmpl, nil)
	txMocks.repo.EXPECT().Create(gomock.Any(), "ws-1", gomock.Any()).Return(nil)

	svc.seedInvitationProspectionTemplate(context.Background(), "ws-1")
}

func TestSeedInvitationProspection_NotificationAlreadyExists_NoOp(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	svc, m := newVeridianService(t)
	mockTemplateSvc := mocks.NewMockTemplateService(ctrl)
	txSvc, txMocks := buildTxService(t, ctrl, mockTemplateSvc)
	require.NoError(t, ConfigureSeedTemplatesSupport(svc, mockTemplateSvc, txSvc))

	rootUser := &domain.User{ID: "root-id", Email: "root@veridian.site", Type: domain.UserTypeUser}
	expectCtxAsRoot(m, rootUser)

	// Template présent.
	existingTmpl := &domain.Template{ID: SeedInvitationProspectionTemplateID}
	mockTemplateSvc.EXPECT().
		GetTemplateByID(gomock.Any(), "ws-1", SeedInvitationProspectionTemplateID, int64(0)).
		Return(existingTmpl, nil)

	// Notification déjà présente → no-op total côté notification.
	existingNotif := &domain.TransactionalNotification{ID: SeedInvitationProspectionTemplateID}
	expectTxAuthOK(txMocks, "ws-1")
	txMocks.repo.EXPECT().Get(gomock.Any(), "ws-1", SeedInvitationProspectionTemplateID).Return(existingNotif, nil)
	// PAS d'EXPECT sur Create — il ne doit pas être appelé.

	svc.seedInvitationProspectionTemplate(context.Background(), "ws-1")
}

func TestSeedInvitationProspection_TemplateDuplicateError_ContinuesToNotification(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	svc, m := newVeridianService(t)
	mockTemplateSvc := mocks.NewMockTemplateService(ctrl)
	txSvc, txMocks := buildTxService(t, ctrl, mockTemplateSvc)
	require.NoError(t, ConfigureSeedTemplatesSupport(svc, mockTemplateSvc, txSvc))

	rootUser := &domain.User{ID: "root-id", Email: "root@veridian.site", Type: domain.UserTypeUser}
	expectCtxAsRoot(m, rootUser)

	// Get : not found (race avec un autre provision)
	mockTemplateSvc.EXPECT().
		GetTemplateByID(gomock.Any(), "ws-1", SeedInvitationProspectionTemplateID, int64(0)).
		Return(nil, errors.New("not found"))
	// Create renvoie une duplicate err → on log mais on continue.
	mockTemplateSvc.EXPECT().
		CreateTemplate(gomock.Any(), "ws-1", gomock.Any()).
		Return(errors.New("pq: duplicate key value violates unique constraint"))

	// On poursuit sur la notification.
	expectTxAuthOK(txMocks, "ws-1")
	txMocks.repo.EXPECT().Get(gomock.Any(), "ws-1", SeedInvitationProspectionTemplateID).Return(nil, errors.New("not found"))
	mockTemplateSvc.EXPECT().
		GetTemplateByID(gomock.Any(), "ws-1", SeedInvitationProspectionTemplateID, int64(0)).
		Return(&domain.Template{ID: SeedInvitationProspectionTemplateID}, nil)
	txMocks.repo.EXPECT().Create(gomock.Any(), "ws-1", gomock.Any()).Return(nil)

	svc.seedInvitationProspectionTemplate(context.Background(), "ws-1")
}

func TestSeedInvitationProspection_TemplateUnknownError_AbortsWithoutNotification(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	svc, m := newVeridianService(t)
	mockTemplateSvc := mocks.NewMockTemplateService(ctrl)
	txSvc, txMocks := buildTxService(t, ctrl, mockTemplateSvc)
	require.NoError(t, ConfigureSeedTemplatesSupport(svc, mockTemplateSvc, txSvc))

	rootUser := &domain.User{ID: "root-id", Email: "root@veridian.site", Type: domain.UserTypeUser}
	expectCtxAsRoot(m, rootUser)

	mockTemplateSvc.EXPECT().
		GetTemplateByID(gomock.Any(), "ws-1", SeedInvitationProspectionTemplateID, int64(0)).
		Return(nil, errors.New("not found"))
	// Create echec non-duplicate → abort, pas de tentative notification.
	mockTemplateSvc.EXPECT().
		CreateTemplate(gomock.Any(), "ws-1", gomock.Any()).
		Return(errors.New("validation failed: bad MJML"))

	// PAS d'EXPECT sur txMocks.repo — la fonction doit return early.
	_ = txMocks

	svc.seedInvitationProspectionTemplate(context.Background(), "ws-1")
}

// ─── Helpers de test ─────────────────────────────────────────────────────────

// expectCtxAsRoot prépare les attentes mock pour que ctxAsRoot() réussisse :
// lookup root user par email + création de session. La session est nettoyée
// en defer via cleanupSession → on l'attend aussi.
func expectCtxAsRoot(m *veridianServiceMocks, rootUser *domain.User) {
	m.userRepo.EXPECT().GetUserByEmail(gomock.Any(), "root@veridian.site").Return(rootUser, nil)
	m.userRepo.EXPECT().CreateSession(gomock.Any(), gomock.Any()).Return(nil)
	m.userRepo.EXPECT().DeleteSession(gomock.Any(), gomock.Any()).Return(nil)
}

// txServiceMocks bundle les mocks d'un TransactionalNotificationService
// pour configurer ses attentes sur les repos sous-jacents depuis les tests
// du seed.
type txServiceMocks struct {
	repo               *mocks.MockTransactionalNotificationRepository
	msgHistoryRepo     *mocks.MockMessageHistoryRepository
	templateSvc        *mocks.MockTemplateService
	contactSvc         *mocks.MockContactService
	emailSvc           *mocks.MockEmailServiceInterface
	authSvc            *mocks.MockAuthService
	workspaceRepo      *mocks.MockWorkspaceRepository
}

// buildTxService construit un vrai TransactionalNotificationService avec
// des mocks underneath. Retourne le service + ses mocks pour qu'un test
// puisse stub leurs comportements. Si sharedTemplateSvc est non-nil, il est
// utilisé comme TemplateService interne (utile pour les tests du seed qui
// veulent EXPECT sur un seul mock partagé).
func buildTxService(t *testing.T, ctrl *gomock.Controller, sharedTemplateSvc *mocks.MockTemplateService) (*TransactionalNotificationService, *txServiceMocks) {
	t.Helper()
	tmplSvc := sharedTemplateSvc
	if tmplSvc == nil {
		tmplSvc = mocks.NewMockTemplateService(ctrl)
	}
	m := &txServiceMocks{
		repo:           mocks.NewMockTransactionalNotificationRepository(ctrl),
		msgHistoryRepo: mocks.NewMockMessageHistoryRepository(ctrl),
		templateSvc:    tmplSvc,
		contactSvc:     mocks.NewMockContactService(ctrl),
		emailSvc:       mocks.NewMockEmailServiceInterface(ctrl),
		authSvc:        mocks.NewMockAuthService(ctrl),
		workspaceRepo:  mocks.NewMockWorkspaceRepository(ctrl),
	}
	svc := NewTransactionalNotificationService(
		m.repo,
		m.msgHistoryRepo,
		m.templateSvc,
		m.contactSvc,
		m.emailSvc,
		m.authSvc,
		logger.NewLogger(),
		m.workspaceRepo,
		"https://notifuse.app.veridian.site",
	)
	return svc, m
}

// newTxServiceWithMocks construit un TxService minimal sans configurer
// d'attentes — utilisé uniquement pour ConfigureSeedTemplatesSupport
// (qui ne déclenche aucun appel sur les mocks).
func newTxServiceWithMocks(t *testing.T, ctrl *gomock.Controller) *TransactionalNotificationService {
	t.Helper()
	svc, _ := buildTxService(t, ctrl, nil)
	return svc
}

// expectTxAuthOK prépare l'attente d'auth pour un appel sur le
// TransactionalNotificationService — qui passe par authService
// AuthenticateUserForWorkspace avant chaque op. On retourne un userWorkspace
// avec full permissions transactional.
func expectTxAuthOK(m *txServiceMocks, workspaceID string) {
	userWorkspace := &domain.UserWorkspace{
		UserID:      "root-id",
		WorkspaceID: workspaceID,
		Role:        "owner",
		Permissions: domain.FullPermissions,
	}
	// Auth est appelée pour Get et Create — on autorise plusieurs fois.
	m.authSvc.EXPECT().
		AuthenticateUserForWorkspace(gomock.Any(), workspaceID).
		DoAndReturn(func(ctx context.Context, _ string) (context.Context, *domain.User, *domain.UserWorkspace, error) {
			return ctx, &domain.User{ID: "root-id", Type: domain.UserTypeUser}, userWorkspace, nil
		}).
		AnyTimes()
}

