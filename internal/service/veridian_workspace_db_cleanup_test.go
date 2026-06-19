package service

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"

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
