package service

import (
	"context"
	"database/sql"
	"errors"
	"testing"
	"time"

	"github.com/Notifuse/notifuse/internal/domain"
	"github.com/golang/mock/gomock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func mustParseTime(t *testing.T, s string) time.Time {
	t.Helper()
	v, err := time.Parse(time.RFC3339, s)
	require.NoError(t, err)
	return v
}

// === ListTenants ===

func TestVeridianService_ListTenants_RejectsShortPrefix(t *testing.T) {
	svc, _ := newVeridianService(t)
	_, err := svc.ListTenants(context.Background(), domain.ListTenantsInput{Prefix: "ab"})
	assert.ErrorContains(t, err, "at least 3 chars")
}

func TestVeridianService_ListTenants_RejectsSQLWildcards(t *testing.T) {
	svc, _ := newVeridianService(t)
	_, err := svc.ListTenants(context.Background(), domain.ListTenantsInput{Prefix: "ab%"})
	assert.ErrorContains(t, err, "SQL wildcards")
}

func TestVeridianService_ListTenants_PrefixOnly_ReturnsManaged(t *testing.T) {
	svc, m := newVeridianService(t)
	ctx := context.Background()

	m.planRepo.EXPECT().ListByPrefix(ctx, "test").
		Return([]string{"test1", "test2"}, nil).Times(1)
	m.planRepo.EXPECT().Get(ctx, "test1").Return(&domain.VeridianPlan{
		WorkspaceID: "test1", Plan: "free", Status: domain.PlanStatusActive,
	}, nil).Times(1)
	m.planRepo.EXPECT().Get(ctx, "test2").Return(&domain.VeridianPlan{
		WorkspaceID: "test2", Plan: "pro", Status: domain.PlanStatusActive,
	}, nil).Times(1)

	resp, err := svc.ListTenants(ctx, domain.ListTenantsInput{Prefix: "test"})
	require.NoError(t, err)
	require.NotNil(t, resp)
	assert.Equal(t, 2, resp.Total)
	assert.Len(t, resp.Managed, 2)
	assert.Empty(t, resp.Orphans, "sans IncludeOrphans, pas de scan workspaces")
	assert.Equal(t, "free", resp.Managed[0].Plan)
	assert.Equal(t, "pro", resp.Managed[1].Plan)
	assert.True(t, resp.Managed[0].HasPlan)
}

func TestVeridianService_ListTenants_IncludeOrphans_SeparatesBuckets(t *testing.T) {
	svc, m := newVeridianService(t)
	ctx := context.Background()

	// 1 managed (testmanaged) + 2 workspaces total dont 1 orphelin (testorphan)
	m.planRepo.EXPECT().ListByPrefix(ctx, "test").
		Return([]string{"testmanaged"}, nil).Times(1)
	m.planRepo.EXPECT().Get(ctx, "testmanaged").Return(&domain.VeridianPlan{
		WorkspaceID: "testmanaged", Plan: "free", Status: domain.PlanStatusActive,
	}, nil).Times(1)

	m.workspaceRepo.EXPECT().List(ctx).Return([]*domain.Workspace{
		{ID: "testmanaged"}, // dans managed → skip orphans
		{ID: "testorphan"},  // pas de plan → orphan
		{ID: "client42"},    // ne matche pas prefix → skip
	}, nil).Times(1)

	resp, err := svc.ListTenants(ctx, domain.ListTenantsInput{
		Prefix:         "test",
		IncludeOrphans: true,
	})
	require.NoError(t, err)
	assert.Equal(t, 2, resp.Total)
	assert.Len(t, resp.Managed, 1)
	assert.Equal(t, "testmanaged", resp.Managed[0].TenantID)
	assert.Len(t, resp.Orphans, 1)
	assert.Equal(t, "testorphan", resp.Orphans[0].TenantID)
	assert.False(t, resp.Orphans[0].HasPlan)
}

func TestVeridianService_ListTenants_IncludeOrphans_NoPrefix_ListsAllWorkspaces(t *testing.T) {
	svc, m := newVeridianService(t)
	ctx := context.Background()

	// Sans prefix : collectPlanIDs scanne TOUTE la table veridian_plan via
	// ListAllIDs → les tenants manages atterrissent dans Managed, et les
	// workspaces SANS plan dans Orphans. Ici ws1 a un plan (managed), ws2/ws3
	// sont orphelins (pas de ligne veridian_plan).
	m.planRepo.EXPECT().ListAllIDs(ctx, gomock.Any()).
		Return([]string{"ws1"}, nil).Times(1)
	m.planRepo.EXPECT().Get(ctx, "ws1").Return(&domain.VeridianPlan{
		WorkspaceID: "ws1", Plan: "pro", Status: domain.PlanStatusActive,
	}, nil).Times(1)

	m.workspaceRepo.EXPECT().List(ctx).Return([]*domain.Workspace{
		{ID: "ws1"}, {ID: "ws2"}, {ID: "ws3"},
	}, nil).Times(1)

	resp, err := svc.ListTenants(ctx, domain.ListTenantsInput{
		IncludeOrphans: true,
	})
	require.NoError(t, err)
	assert.Equal(t, 3, resp.Total)
	assert.Len(t, resp.Managed, 1, "ws1 a un plan → managed même sans prefix")
	assert.Equal(t, "ws1", resp.Managed[0].TenantID)
	assert.Len(t, resp.Orphans, 2, "ws2/ws3 sans plan → orphans")
}

// TestVeridianService_ListTenants_NoPrefix_ListsManaged : le fix du bug
// 2026-06-15 — SANS prefix ni IncludeOrphans, le bucket "managed" doit lister
// TOUS les tenants ayant un plan (avant le fix : managed vide alors que des
// tenants manages existaient).
func TestVeridianService_ListTenants_NoPrefix_ListsManaged(t *testing.T) {
	svc, m := newVeridianService(t)
	ctx := context.Background()

	m.planRepo.EXPECT().ListAllIDs(ctx, gomock.Any()).
		Return([]string{"coldtunnel", "canaryfree"}, nil).Times(1)
	m.planRepo.EXPECT().Get(ctx, "coldtunnel").Return(&domain.VeridianPlan{
		WorkspaceID: "coldtunnel", Plan: "pro", Status: domain.PlanStatusActive,
	}, nil).Times(1)
	m.planRepo.EXPECT().Get(ctx, "canaryfree").Return(&domain.VeridianPlan{
		WorkspaceID: "canaryfree", Plan: "free", Status: domain.PlanStatusActive,
	}, nil).Times(1)

	resp, err := svc.ListTenants(ctx, domain.ListTenantsInput{}) // aucun param
	require.NoError(t, err)
	require.NotNil(t, resp)
	assert.Equal(t, 2, resp.Total)
	require.Len(t, resp.Managed, 2, "sans prefix, managed doit lister TOUS les tenants avec plan")
	for _, summ := range resp.Managed {
		assert.True(t, summ.HasPlan, "tenant avec plan → HasPlan=true")
	}
	assert.Empty(t, resp.Orphans, "sans IncludeOrphans, pas de scan workspaces")
}

// TestVeridianService_ListTenants_Coherence_ManagedSameWithAndWithoutPrefix :
// test contractuel qui VERROUILLE l'invariant du ticket — un tenant has_plan
// présent dans le bucket "managed" AVEC un prefix qui le matche doit AUSSI
// être présent dans "managed" SANS prefix. Avant le fix, l'appel sans prefix
// renvoyait un bucket managed vide → incohérence. Ce test échoue si quelqu'un
// re-casse collectPlanIDs pour le cas prefix=="".
func TestVeridianService_ListTenants_Coherence_ManagedSameWithAndWithoutPrefix(t *testing.T) {
	ctx := context.Background()

	plan := &domain.VeridianPlan{
		WorkspaceID: "coldtunnel", Plan: "pro", Status: domain.PlanStatusActive,
	}

	// --- Avec prefix "cold" : le tenant apparaît dans managed (chemin OK).
	svcP, mP := newVeridianService(t)
	mP.planRepo.EXPECT().ListByPrefix(ctx, "cold").
		Return([]string{"coldtunnel"}, nil).Times(1)
	mP.planRepo.EXPECT().Get(ctx, "coldtunnel").Return(plan, nil).Times(1)

	respWithPrefix, err := svcP.ListTenants(ctx, domain.ListTenantsInput{Prefix: "cold"})
	require.NoError(t, err)
	require.Len(t, respWithPrefix.Managed, 1)
	assert.Equal(t, "coldtunnel", respWithPrefix.Managed[0].TenantID)

	// --- Sans prefix : le MÊME tenant DOIT apparaître dans managed (le fix).
	svcN, mN := newVeridianService(t)
	mN.planRepo.EXPECT().ListAllIDs(ctx, gomock.Any()).
		Return([]string{"coldtunnel"}, nil).Times(1)
	mN.planRepo.EXPECT().Get(ctx, "coldtunnel").Return(plan, nil).Times(1)

	respNoPrefix, err := svcN.ListTenants(ctx, domain.ListTenantsInput{})
	require.NoError(t, err)
	require.Len(t, respNoPrefix.Managed, 1,
		"INVARIANT: un tenant has_plan présent avec prefix DOIT être présent sans prefix")

	// Verrou explicite : le même TenantID dans les deux buckets managed.
	assert.Equal(t, respWithPrefix.Managed[0].TenantID, respNoPrefix.Managed[0].TenantID,
		"le tenant managé doit être le même avec et sans prefix")
}

func TestVeridianService_ListTenants_Limit_CapsManaged(t *testing.T) {
	svc, m := newVeridianService(t)
	ctx := context.Background()

	m.planRepo.EXPECT().ListByPrefix(ctx, "test").
		Return([]string{"test1", "test2", "test3", "test4"}, nil).Times(1)
	// Get appele 2x seulement (limit=2, break apres Managed[1]).
	m.planRepo.EXPECT().Get(ctx, gomock.Any()).Return(&domain.VeridianPlan{
		Plan: "free", Status: domain.PlanStatusActive,
	}, nil).Times(2)

	resp, err := svc.ListTenants(ctx, domain.ListTenantsInput{
		Prefix: "test",
		Limit:  2,
	})
	require.NoError(t, err)
	assert.Len(t, resp.Managed, 2, "limit=2 doit capper Managed a 2")
	assert.Equal(t, 2, resp.Total)
}

func TestVeridianService_ListTenants_PlanGetError_NotFatal(t *testing.T) {
	// Si planRepo.Get echoue pour 1 tenant, on garde le summary minimal
	// (HasPlan=true, sans Plan/Status). Le listing reste utile.
	svc, m := newVeridianService(t)
	ctx := context.Background()

	m.planRepo.EXPECT().ListByPrefix(ctx, "test").
		Return([]string{"test1"}, nil).Times(1)
	m.planRepo.EXPECT().Get(ctx, "test1").
		Return(nil, sql.ErrNoRows).Times(1)

	resp, err := svc.ListTenants(ctx, domain.ListTenantsInput{Prefix: "test"})
	require.NoError(t, err)
	assert.Len(t, resp.Managed, 1)
	assert.True(t, resp.Managed[0].HasPlan, "presence dans ListByPrefix = HasPlan true meme si Get fail")
	assert.Empty(t, resp.Managed[0].Plan)
}

func TestVeridianService_ListTenants_ListWorkspacesError_Propagates(t *testing.T) {
	svc, m := newVeridianService(t)
	ctx := context.Background()

	m.planRepo.EXPECT().ListByPrefix(ctx, "test").Return(nil, nil).Times(1)
	m.workspaceRepo.EXPECT().List(ctx).Return(nil, errors.New("db down")).Times(1)

	_, err := svc.ListTenants(ctx, domain.ListTenantsInput{
		Prefix:         "test",
		IncludeOrphans: true,
	})
	assert.ErrorContains(t, err, "list workspaces")
}

func TestVeridianService_ListTenants_DeletedAtFormatted(t *testing.T) {
	svc, m := newVeridianService(t)
	ctx := context.Background()

	deletedAt := mustParseTime(t, "2026-03-15T10:30:00Z")
	m.planRepo.EXPECT().ListByPrefix(ctx, "test").Return([]string{"test1"}, nil).Times(1)
	m.planRepo.EXPECT().Get(ctx, "test1").Return(&domain.VeridianPlan{
		WorkspaceID: "test1",
		Plan:        "free",
		Status:      domain.PlanStatusDeleted,
		DeletedAt:   &deletedAt,
	}, nil).Times(1)

	resp, err := svc.ListTenants(ctx, domain.ListTenantsInput{Prefix: "test"})
	require.NoError(t, err)
	require.Len(t, resp.Managed, 1)
	assert.Equal(t, "2026-03-15T10:30:00Z", resp.Managed[0].DeletedAt)
	assert.Equal(t, "deleted", resp.Managed[0].Status)
}

// === WipeTestTenants — IncludeOrphans path ===

func TestVeridianService_WipeTestTenants_IncludeOrphans_AddsWorkspacesOnly(t *testing.T) {
	// Vérifie que IncludeOrphans=true élargit les candidats sans dupliquer
	// les tids déjà retournés par planRepo.ListByPrefix. Les workspaces qui
	// ne matchent pas le prefix sont ignorés.
	svc, m := newVeridianService(t)
	ctx := context.Background()

	// Setup ctxAsRoot (3 calls userRepo). On veut que TOUS les candidates
	// (canary protected) skip — donc pas de wipe réel à mocker.
	rootUser := &domain.User{ID: "root-id", Email: "root@veridian.site", Type: domain.UserTypeUser}
	m.userRepo.EXPECT().GetUserByEmail(gomock.Any(), "root@veridian.site").
		Return(rootUser, nil).AnyTimes()
	m.userRepo.EXPECT().CreateSession(gomock.Any(), gomock.Any()).Return(nil).AnyTimes()
	m.userRepo.EXPECT().DeleteSession(gomock.Any(), gomock.Any()).Return(nil).AnyTimes()

	// planRepo retourne 1 tenant managed.
	m.planRepo.EXPECT().ListByPrefix(ctx, "canary").
		Return([]string{"canarymanaged"}, nil).Times(1)

	// workspaceRepo retourne 2 ws : 1 déjà dans candidates (dedupe attendu),
	// 1 orphelin (canaryorphan, ajouté), 1 hors prefix (autre, ignoré).
	m.workspaceRepo.EXPECT().List(ctx).Return([]*domain.Workspace{
		{ID: "canarymanaged"},
		{ID: "canaryorphan"},
		{ID: "client42"},
	}, nil).Times(1)

	resp, err := svc.WipeTestTenants(ctx, domain.WipeTestTenantsInput{
		Prefix:         "canary",
		IncludeOrphans: true,
		// Pas de SafetyClientPrefixes override → utilise defaults qui
		// incluent "canary" → tout est skip.
	})
	require.NoError(t, err)
	// Les 2 candidats canary (managed + orphan) doivent être skipped via safety prefix "canary".
	assert.ElementsMatch(t,
		[]string{"canarymanaged", "canaryorphan"},
		resp.Skipped,
		"dedupe + filtre prefix + safety: managed dans candidates + orphan ajouté, client42 ignoré")
	assert.Empty(t, resp.Wiped)
}

func TestVeridianService_WipeTestTenants_IncludeOrphans_WithoutPrefix_NoOp(t *testing.T) {
	// IncludeOrphans=true sans Prefix : ne touche pas workspaceRepo (filtrage
	// prefix obligatoire pour la sécurité — scan tous workspaces serait dangereux).
	// Validé par l'absence d'EXPECT() workspaceRepo.List(ctx).
	svc, m := newVeridianService(t)
	rootUser := &domain.User{ID: "root-id", Email: "root@veridian.site", Type: domain.UserTypeUser}
	m.userRepo.EXPECT().GetUserByEmail(gomock.Any(), "root@veridian.site").
		Return(rootUser, nil).AnyTimes()
	m.userRepo.EXPECT().CreateSession(gomock.Any(), gomock.Any()).Return(nil).AnyTimes()
	m.userRepo.EXPECT().DeleteSession(gomock.Any(), gomock.Any()).Return(nil).AnyTimes()

	resp, err := svc.WipeTestTenants(context.Background(), domain.WipeTestTenantsInput{
		TenantIDs:      []string{"canaryfree"}, // tid explicite + safety prefix → skip
		IncludeOrphans: true,                   // sans prefix → ignoré
	})
	require.NoError(t, err)
	assert.ElementsMatch(t, []string{"canaryfree"}, resp.Skipped)
}

func TestVeridianService_WipeTestTenants_IncludeOrphans_ListWorkspacesError_Propagates(t *testing.T) {
	svc, m := newVeridianService(t)
	ctx := context.Background()
	rootUser := &domain.User{ID: "root-id", Email: "root@veridian.site", Type: domain.UserTypeUser}
	m.userRepo.EXPECT().GetUserByEmail(gomock.Any(), "root@veridian.site").
		Return(rootUser, nil).AnyTimes()

	m.planRepo.EXPECT().ListByPrefix(ctx, "ghost").Return([]string{}, nil).Times(1)
	m.workspaceRepo.EXPECT().List(ctx).Return(nil, errors.New("db down")).Times(1)

	_, err := svc.WipeTestTenants(ctx, domain.WipeTestTenantsInput{
		Prefix:         "ghost",
		IncludeOrphans: true,
	})
	assert.ErrorContains(t, err, "list workspaces")
}
