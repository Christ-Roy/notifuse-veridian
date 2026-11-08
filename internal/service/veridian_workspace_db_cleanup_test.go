package service

import (
	"context"
	"errors"
	"testing"

	"github.com/golang/mock/gomock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/Notifuse/notifuse/internal/domain"
	"github.com/Notifuse/notifuse/pkg/logger"
)

// fakeForceDropRepo embed l'interface WorkspaceRepository (nil) et n'override que
// VeridianForceDropDatabase → satisfait à la fois domain.WorkspaceRepository (pour
// le champ s.workspaceRepo) et veridianForceDropper (par type-assertion).
type fakeForceDropRepo struct {
	domain.WorkspaceRepository
	called    int
	lastWSID  string
	returnErr error
}

func (f *fakeForceDropRepo) VeridianForceDropDatabase(ctx context.Context, workspaceID string, log logger.Logger) error {
	f.called++
	f.lastWSID = workspaceID
	return f.returnErr
}

// repoWithoutDropCapability satisfait WorkspaceRepository mais PAS veridianForceDropper.
type repoWithoutDropCapability struct {
	domain.WorkspaceRepository
}

// fakeDBExistenceRepo embed WorkspaceRepository (nil) et implémente
// VeridianWorkspaceDBExists → satisfait veridianDBExistenceChecker.
type fakeDBExistenceRepo struct {
	domain.WorkspaceRepository
	exists bool
	err    error
	calls  int
}

func (f *fakeDBExistenceRepo) VeridianWorkspaceDBExists(ctx context.Context, workspaceID string) (bool, error) {
	f.calls++
	return f.exists, f.err
}

// fakeSystemRecordDeleterRepo embed WorkspaceRepository (nil) et implémente
// VeridianDeleteWorkspaceSystemRecord → satisfait veridianSystemRecordDeleter.
type fakeSystemRecordDeleterRepo struct {
	domain.WorkspaceRepository
	called    int
	lastWSID  string
	returnErr error
}

func (f *fakeSystemRecordDeleterRepo) VeridianDeleteWorkspaceSystemRecord(ctx context.Context, workspaceID string) error {
	f.called++
	f.lastWSID = workspaceID
	return f.returnErr
}

// fakeWorkspaceLockerRepo embed WorkspaceRepository (nil) et implémente
// VeridianAcquireWorkspaceLock → satisfait veridianWorkspaceLocker. Durcissement
// 2026-10-04 (audit sécurité, race wipe/recréation).
type fakeWorkspaceLockerRepo struct {
	domain.WorkspaceRepository
	acquireCalls  int
	lastWSID      string
	acquireErr    error
	releaseCalled bool
}

func (f *fakeWorkspaceLockerRepo) VeridianAcquireWorkspaceLock(ctx context.Context, workspaceID string) (func(), error) {
	f.acquireCalls++
	f.lastWSID = workspaceID
	if f.acquireErr != nil {
		return nil, f.acquireErr
	}
	return func() { f.releaseCalled = true }, nil
}

func TestAcquireWorkspaceWipeLock_NoCapability_ReturnsNoopNilError(t *testing.T) {
	// Repo sans la capacité (ex. double de test minimal) → no-op, pas d'erreur :
	// comportement inchangé pour un repo qui n'implémente pas le verrou.
	s := &veridianService{workspaceRepo: &repoWithoutDropCapability{}}
	release, err := s.acquireWorkspaceWipeLock(context.Background(), "tst123")
	assert.NoError(t, err)
	require.NotNil(t, release)
	assert.NotPanics(t, release)
}

func TestAcquireWorkspaceWipeLock_CapabilityPresent_AcquiresAndReleases(t *testing.T) {
	repo := &fakeWorkspaceLockerRepo{}
	s := &veridianService{workspaceRepo: repo}

	release, err := s.acquireWorkspaceWipeLock(context.Background(), "tst123")
	require.NoError(t, err)
	assert.Equal(t, 1, repo.acquireCalls)
	assert.Equal(t, "tst123", repo.lastWSID)

	release()
	assert.True(t, repo.releaseCalled, "release() returned by acquireWorkspaceWipeLock must call through to the repo's release func")
}

func TestAcquireWorkspaceWipeLock_AcquireFails_PropagatesError(t *testing.T) {
	// Contrairement aux autres helpers best-effort de ce fichier, un échec
	// d'acquisition du verrou de wipe DOIT remonter une erreur : on ne sait
	// plus dire si un CreateWorkspace concurrent est en cours sur le même
	// id, et continuer sans verrou serait exactement la race que ce
	// durcissement ferme (audit 2026-10-04).
	repo := &fakeWorkspaceLockerRepo{acquireErr: errors.New("system db unreachable")}
	s := &veridianService{workspaceRepo: repo}

	release, err := s.acquireWorkspaceWipeLock(context.Background(), "tst123")
	require.Error(t, err)
	assert.Nil(t, release)
	assert.Contains(t, err.Error(), "system db unreachable")
}

func TestDeleteWorkspaceSystemRecordBestEffort_CutsRecord(t *testing.T) {
	repo := &fakeSystemRecordDeleterRepo{}
	s := &veridianService{workspaceRepo: repo}
	cut := s.deleteWorkspaceSystemRecordBestEffort(context.Background(), "tst123")
	assert.True(t, cut, "record-first delete succeeded → re-election cut")
	assert.Equal(t, 1, repo.called)
	assert.Equal(t, "tst123", repo.lastWSID)
}

func TestDeleteWorkspaceSystemRecordBestEffort_NoCapability_ReturnsFalse(t *testing.T) {
	// Repo sans la capacité → false (on retombe sur le comportement upstream seul).
	s := &veridianService{workspaceRepo: &repoWithoutDropCapability{}}
	assert.False(t, s.deleteWorkspaceSystemRecordBestEffort(context.Background(), "tst123"))
}

func TestDeleteWorkspaceSystemRecordBestEffort_Error_ReturnsFalse(t *testing.T) {
	// Erreur de suppression → false + log, ne panique pas (le force-drop reste tenté).
	repo := &fakeSystemRecordDeleterRepo{returnErr: errors.New("boom")}
	s := &veridianService{workspaceRepo: repo, logger: logger.NewLogger()}
	assert.NotPanics(t, func() {
		assert.False(t, s.deleteWorkspaceSystemRecordBestEffort(context.Background(), "tst123"))
	})
	assert.Equal(t, 1, repo.called)
}

func TestWorkspaceDBStillExists_TrueWhenRepoSaysExists(t *testing.T) {
	repo := &fakeDBExistenceRepo{exists: true}
	s := &veridianService{workspaceRepo: repo}
	assert.True(t, s.workspaceDBStillExists(context.Background(), "ws1"))
	assert.Equal(t, 1, repo.calls)
}

func TestWorkspaceDBStillExists_FalseWhenRepoSaysGone(t *testing.T) {
	repo := &fakeDBExistenceRepo{exists: false}
	s := &veridianService{workspaceRepo: repo}
	assert.False(t, s.workspaceDBStillExists(context.Background(), "ws1"))
}

func TestWorkspaceDBStillExists_NoCapabilityReturnsFalse(t *testing.T) {
	// Repo sans la capacité de check → best-effort false (ne bloque pas le wipe).
	s := &veridianService{workspaceRepo: &repoWithoutDropCapability{}}
	assert.False(t, s.workspaceDBStillExists(context.Background(), "ws1"))
}

func TestWorkspaceDBStillExists_CheckErrorReturnsFalse(t *testing.T) {
	// Erreur de check → best-effort false + log (ne bloque pas le wipe sur incertitude).
	repo := &fakeDBExistenceRepo{exists: true, err: errors.New("query boom")}
	s := &veridianService{workspaceRepo: repo, logger: logger.NewLogger()}
	assert.False(t, s.workspaceDBStillExists(context.Background(), "ws1"))
}

func TestConfigureWorkspaceDBCleanup_SetsPrefix(t *testing.T) {
	s := &veridianService{}
	s.ConfigureWorkspaceDBCleanup("notifuse")
	assert.Equal(t, "notifuse", s.dbPrefix)
}

func TestForceDropWorkspaceDBBestEffort_NoPrefix_NoOp(t *testing.T) {
	repo := &fakeForceDropRepo{}
	s := &veridianService{workspaceRepo: repo, dbPrefix: ""}
	s.forceDropWorkspaceDBBestEffort(context.Background(), "tst123")
	assert.Equal(t, 0, repo.called, "no prefix configured → must not call drop")
}

func TestForceDropWorkspaceDBBestEffort_CallsDropper(t *testing.T) {
	repo := &fakeForceDropRepo{}
	s := &veridianService{workspaceRepo: repo, dbPrefix: "notifuse"}
	s.forceDropWorkspaceDBBestEffort(context.Background(), "tst123")
	assert.Equal(t, 1, repo.called)
	assert.Equal(t, "tst123", repo.lastWSID)
}

func TestForceDropWorkspaceDBBestEffort_SwallowsError(t *testing.T) {
	// Un échec du DROP ne doit PAS paniquer ni propager (best-effort).
	repo := &fakeForceDropRepo{returnErr: errors.New("drop failed")}
	s := &veridianService{workspaceRepo: repo, dbPrefix: "notifuse"}
	assert.NotPanics(t, func() {
		s.forceDropWorkspaceDBBestEffort(context.Background(), "tst123")
	})
	assert.Equal(t, 1, repo.called)
}

func TestForceDropWorkspaceDBBestEffort_RepoWithoutCapability_NoOp(t *testing.T) {
	repo := &repoWithoutDropCapability{}
	s := &veridianService{workspaceRepo: repo, dbPrefix: "notifuse"}
	assert.NotPanics(t, func() {
		s.forceDropWorkspaceDBBestEffort(context.Background(), "tst123")
	})
}

// fakeProvisionCleanupRepo : GetByID dit si le record existe, VeridianForceDropDatabase compte les DROP.
type fakeProvisionCleanupRepo struct {
	domain.WorkspaceRepository
	recordExists bool
	drops        []string
}

func (f *fakeProvisionCleanupRepo) GetByID(ctx context.Context, id string) (*domain.Workspace, error) {
	if f.recordExists {
		return &domain.Workspace{ID: id}, nil
	}
	return nil, &domain.ErrWorkspaceNotFound{WorkspaceID: id}
}

func (f *fakeProvisionCleanupRepo) VeridianForceDropDatabase(ctx context.Context, workspaceID string, log logger.Logger) error {
	f.drops = append(f.drops, workspaceID)
	return nil
}

func TestCleanupFailedProvisionDB(t *testing.T) {
	ctx := context.Background()

	t.Run("base laissee par un provision rate, sans record: supprimee", func(t *testing.T) {
		repo := &fakeProvisionCleanupRepo{}
		s := &veridianService{workspaceRepo: repo, dbPrefix: "notifuse"}
		s.cleanupFailedProvisionDB(ctx, "tst1", false)
		assert.Equal(t, []string{"tst1"}, repo.drops)
	})
	t.Run("base qui existait avant la tentative: jamais touchee (donnees possibles)", func(t *testing.T) {
		repo := &fakeProvisionCleanupRepo{}
		s := &veridianService{workspaceRepo: repo, dbPrefix: "notifuse"}
		s.cleanupFailedProvisionDB(ctx, "tst1", true)
		assert.Empty(t, repo.drops)
	})
	t.Run("record workspace present: jamais touchee", func(t *testing.T) {
		repo := &fakeProvisionCleanupRepo{recordExists: true}
		s := &veridianService{workspaceRepo: repo, dbPrefix: "notifuse"}
		s.cleanupFailedProvisionDB(ctx, "tst1", false)
		assert.Empty(t, repo.drops)
	})
	t.Run("repo sans la capacite: ne plante pas", func(t *testing.T) {
		s := &veridianService{workspaceRepo: &repoOnlyGetByID{}, dbPrefix: "notifuse"}
		assert.NotPanics(t, func() { s.cleanupFailedProvisionDB(ctx, "tst1", false) })
	})
}

// repoOnlyGetByID : un repo sans record ni capacite de DROP (mock/alternatif).
type repoOnlyGetByID struct{ domain.WorkspaceRepository }

func (repoOnlyGetByID) GetByID(ctx context.Context, id string) (*domain.Workspace, error) {
	return nil, &domain.ErrWorkspaceNotFound{WorkspaceID: id}
}

func TestPruneOrphanedOwners(t *testing.T) {
	ctx := context.Background()
	member := func(id, email string, typ domain.UserType) *domain.UserWorkspaceWithEmail {
		return &domain.UserWorkspaceWithEmail{UserWorkspace: domain.UserWorkspace{UserID: id, WorkspaceID: "ws-gone"}, Email: email, Type: typ}
	}

	t.Run("owner sans autre workspace: compte et sessions supprimes; les autres restent", func(t *testing.T) {
		svc, m := newVeridianService(t)
		svc.defaultCoOwnerEmail = "robert@veridian.site"
		// proprietaire de test sans autre workspace
		m.workspaceRepo.EXPECT().GetUserWorkspaces(gomock.Any(), "u-owner").Return(nil, nil).Times(1)
		m.userRepo.EXPECT().DeleteAllSessionsByUserID(gomock.Any(), "u-owner").Return(nil).Times(1)
		m.userRepo.EXPECT().Delete(gomock.Any(), "u-owner").Return(nil).Times(1)
		// membre qui appartient encore a un autre workspace: intact (aucun appel Delete)
		// compte machine de CE tenant sans autre workspace: supprime (sinon reprovisionnement 500)
		m.workspaceRepo.EXPECT().GetUserWorkspaces(gomock.Any(), "u-key").Return(nil, nil).Times(1)
		m.userRepo.EXPECT().DeleteAllSessionsByUserID(gomock.Any(), "u-key").Return(nil).Times(1)
		m.userRepo.EXPECT().Delete(gomock.Any(), "u-key").Return(nil).Times(1)
		m.workspaceRepo.EXPECT().GetUserWorkspaces(gomock.Any(), "u-shared").
			Return([]*domain.UserWorkspace{{UserID: "u-shared", WorkspaceID: "other"}}, nil).Times(1)

		svc.pruneOrphanedOwners(ctx, "ws-gone", []*domain.UserWorkspaceWithEmail{
			member("u-owner", "e2e-sso-x-a@veridian.site", domain.UserTypeUser),
			member("u-shared", "shared@example.com", domain.UserTypeUser),
			member("u-root", "root@veridian.site", domain.UserTypeUser),       // root: jamais
			member("u-co", "robert@veridian.site", domain.UserTypeUser),        // co-owner par defaut: jamais
			member("u-key", "veridian-api-ws-gone@notifuse.app.veridian.site", domain.UserTypeAPIKey),
			member("u-other-key", "veridian-api-autre@notifuse.app.veridian.site", domain.UserTypeAPIKey), // machine d'un autre tenant: jamais
		})
	})

	t.Run("une erreur de lecture des workspaces ne supprime rien", func(t *testing.T) {
		svc, m := newVeridianService(t)
		m.workspaceRepo.EXPECT().GetUserWorkspaces(gomock.Any(), "u1").Return(nil, errors.New("db down")).Times(1)
		svc.pruneOrphanedOwners(ctx, "ws-gone", []*domain.UserWorkspaceWithEmail{member("u1", "a@x.test", domain.UserTypeUser)})
	})
}
