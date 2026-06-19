package service

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/Notifuse/notifuse/internal/domain"
	"github.com/Notifuse/notifuse/pkg/logger"
)

// fakeOrphanGCRepo embed WorkspaceRepository (nil) et implémente la capacité GC.
type fakeOrphanGCRepo struct {
	domain.WorkspaceRepository
	prefix      string
	orphans     []string
	listErr     error
	dropped     []string
	dropErrFor  map[string]error // base → erreur de DROP
}

func (f *fakeOrphanGCRepo) VeridianListOrphanWorkspaceDBs(ctx context.Context) ([]string, error) {
	if f.listErr != nil {
		return nil, f.listErr
	}
	return f.orphans, nil
}

func (f *fakeOrphanGCRepo) VeridianForceDropDatabaseByName(ctx context.Context, dbName string, log logger.Logger) error {
	if err, ok := f.dropErrFor[dbName]; ok {
		return err
	}
	f.dropped = append(f.dropped, dbName)
	return nil
}

func (f *fakeOrphanGCRepo) VeridianWorkspaceDBPrefix() string { return f.prefix }

func TestVeridianGCOrphanWorkspaceDBs_DropsNonSafetyOrphans(t *testing.T) {
	repo := &fakeOrphanGCRepo{
		prefix: "notifuse",
		orphans: []string{
			"notifuse_ws_tst1",
			"notifuse_ws_canaryfree", // safety → skip
			"notifuse_ws_chaos9",
		},
		dropErrFor: map[string]error{},
	}
	s := &veridianService{workspaceRepo: repo}

	resp, err := s.VeridianGCOrphanWorkspaceDBs(context.Background(), VeridianOrphanDBGCInput{})
	require.NoError(t, err)
	assert.Equal(t, 3, resp.TotalOrphans)
	assert.Equal(t, 2, resp.Droppable)
	assert.ElementsMatch(t, []string{"notifuse_ws_tst1", "notifuse_ws_chaos9"}, resp.Dropped)
	assert.Equal(t, []string{"notifuse_ws_canaryfree"}, resp.SkippedSafety)
	assert.Empty(t, resp.Errors)
}

func TestVeridianGCOrphanWorkspaceDBs_NeverDropsCanary(t *testing.T) {
	repo := &fakeOrphanGCRepo{
		prefix:  "notifuse",
		orphans: []string{"notifuse_ws_canarypro", "notifuse_ws_canary_enterprise"},
	}
	s := &veridianService{workspaceRepo: repo}

	resp, err := s.VeridianGCOrphanWorkspaceDBs(context.Background(), VeridianOrphanDBGCInput{})
	require.NoError(t, err)
	assert.Empty(t, resp.Dropped, "canary bases must NEVER be dropped")
	assert.Len(t, resp.SkippedSafety, 2)
	assert.Empty(t, repo.dropped)
}

func TestVeridianGCOrphanWorkspaceDBs_DryRun(t *testing.T) {
	repo := &fakeOrphanGCRepo{
		prefix:  "notifuse",
		orphans: []string{"notifuse_ws_tst1", "notifuse_ws_tst2"},
	}
	s := &veridianService{workspaceRepo: repo}

	resp, err := s.VeridianGCOrphanWorkspaceDBs(context.Background(), VeridianOrphanDBGCInput{DryRun: true})
	require.NoError(t, err)
	assert.True(t, resp.DryRun)
	assert.Equal(t, 2, resp.Droppable)
	assert.Empty(t, resp.Dropped, "dry run drops nothing")
	assert.Empty(t, repo.dropped)
}

func TestVeridianGCOrphanWorkspaceDBs_MaxDropsCap(t *testing.T) {
	repo := &fakeOrphanGCRepo{
		prefix:  "notifuse",
		orphans: []string{"notifuse_ws_tst1", "notifuse_ws_tst2", "notifuse_ws_tst3"},
	}
	s := &veridianService{workspaceRepo: repo}

	resp, err := s.VeridianGCOrphanWorkspaceDBs(context.Background(), VeridianOrphanDBGCInput{MaxDrops: 2})
	require.NoError(t, err)
	assert.Len(t, resp.Dropped, 2)
	assert.Equal(t, 1, resp.SkippedCap)
}

func TestVeridianGCOrphanWorkspaceDBs_DropErrorContinues(t *testing.T) {
	repo := &fakeOrphanGCRepo{
		prefix:  "notifuse",
		orphans: []string{"notifuse_ws_tst1", "notifuse_ws_tst2"},
		dropErrFor: map[string]error{
			"notifuse_ws_tst1": errors.New("boom"),
		},
	}
	s := &veridianService{workspaceRepo: repo}

	resp, err := s.VeridianGCOrphanWorkspaceDBs(context.Background(), VeridianOrphanDBGCInput{})
	require.NoError(t, err, "a single DROP failure must not abort the run")
	assert.Equal(t, []string{"notifuse_ws_tst2"}, resp.Dropped)
	assert.Contains(t, resp.Errors, "notifuse_ws_tst1")
}

func TestVeridianGCOrphanWorkspaceDBs_CustomSafetyPrefixes(t *testing.T) {
	repo := &fakeOrphanGCRepo{
		prefix:  "notifuse",
		orphans: []string{"notifuse_ws_keepme1", "notifuse_ws_tst1"},
	}
	s := &veridianService{workspaceRepo: repo}

	resp, err := s.VeridianGCOrphanWorkspaceDBs(context.Background(), VeridianOrphanDBGCInput{
		SafetyPrefixes: []string{"keepme"},
	})
	require.NoError(t, err)
	assert.Equal(t, []string{"notifuse_ws_tst1"}, resp.Dropped)
	assert.Equal(t, []string{"notifuse_ws_keepme1"}, resp.SkippedSafety)
}

func TestVeridianGCOrphanWorkspaceDBs_ListError(t *testing.T) {
	repo := &fakeOrphanGCRepo{prefix: "notifuse", listErr: errors.New("db down")}
	s := &veridianService{workspaceRepo: repo}

	_, err := s.VeridianGCOrphanWorkspaceDBs(context.Background(), VeridianOrphanDBGCInput{})
	require.Error(t, err)
}

func TestVeridianGCOrphanWorkspaceDBs_RepoWithoutCapability(t *testing.T) {
	s := &veridianService{workspaceRepo: &repoWithoutDropCapability{}}
	_, err := s.VeridianGCOrphanWorkspaceDBs(context.Background(), VeridianOrphanDBGCInput{})
	require.ErrorIs(t, err, errOrphanGCUnsupported)
}

func TestVeridianHasAnySafetyPrefix(t *testing.T) {
	assert.True(t, veridianHasAnySafetyPrefix("canaryfree", []string{"canary"}))
	assert.False(t, veridianHasAnySafetyPrefix("tst123", []string{"canary"}))
	// préfixe avec tiret → matche aussi la forme underscore (conversion DB).
	assert.True(t, veridianHasAnySafetyPrefix("real_client_x", []string{"real-client"}))
	assert.False(t, veridianHasAnySafetyPrefix("anything", []string{""}), "empty prefix is ignored")
}

func TestVeridianTrimDBPrefix(t *testing.T) {
	assert.Equal(t, "tst1", veridianTrimDBPrefix("notifuse", "notifuse_ws_tst1"))
	assert.Equal(t, "other", veridianTrimDBPrefix("notifuse", "other"))
}
