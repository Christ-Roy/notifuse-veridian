package service

// === Veridian patch — Freeze member per-user (CONTRAT-HUB §5.21) ===
// Tests unitaires de veridianService.FreezeMember / UnfreezeMember +
// ConfigureFrozenMemberSupport.
//
// Couvre :
//   - FreezeMember : success (new freeze), idempotent (already frozen),
//                    owner refused, user not member, tenant not found,
//                    validation inputs, repo not configured
//   - UnfreezeMember : success (was frozen), idempotent (not frozen),
//                      user not member, tenant not found, repo not configured
//   - ConfigureFrozenMemberSupport : success + error sur impl invalide

import (
	"context"
	"database/sql"
	"errors"
	"testing"

	"github.com/Notifuse/notifuse/internal/domain"
	"github.com/Notifuse/notifuse/internal/domain/mocks"
	"github.com/golang/mock/gomock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// newVeridianServiceWithFrozenRepo retourne un veridianService avec
// frozenMemberRepo configure. Les autres mocks restent ceux de newVeridianService.
func newVeridianServiceWithFrozenRepo(t *testing.T) (*veridianService, *veridianServiceMocks, *mocks.MockVeridianFrozenMemberRepository) {
	t.Helper()
	svc, m := newVeridianService(t)
	ctrl := gomock.NewController(t)
	t.Cleanup(ctrl.Finish)
	frozenRepo := mocks.NewMockVeridianFrozenMemberRepository(ctrl)
	svc.frozenMemberRepo = frozenRepo
	return svc, m, frozenRepo
}

// === FreezeMember ===========================================================

func TestFreezeMember_Success_NewFreeze(t *testing.T) {
	svc, m, frozenRepo := newVeridianServiceWithFrozenRepo(t)
	ctx := context.Background()

	m.workspaceRepo.EXPECT().GetByID(ctx, "ws-1").Return(&domain.Workspace{ID: "ws-1"}, nil)
	m.user.EXPECT().GetUserByEmail(ctx, "bob@example.com").
		Return(&domain.User{ID: "bob-uuid", Email: "bob@example.com", Type: domain.UserTypeUser}, nil)
	m.workspaceRepo.EXPECT().GetUserWorkspace(ctx, "bob-uuid", "ws-1").
		Return(&domain.UserWorkspace{UserID: "bob-uuid", WorkspaceID: "ws-1", Role: "member"}, nil)

	frozenRepo.EXPECT().Freeze(ctx, "ws-1", "bob-uuid", domain.FreezeReasonQuotaSeatExceeded).
		Return(&domain.VeridianFrozenMember{
			WorkspaceID: "ws-1",
			UserID:      "bob-uuid",
			Reason:      domain.FreezeReasonQuotaSeatExceeded,
		}, false, nil)

	// Webhook emit sur new freeze (pas sur replay idempotent).
	m.emitter.EXPECT().Emit(ctx, domain.EventTenantMemberFrozen, "ws-1", gomock.Any()).Times(1)

	resp, alreadyFrozen, err := svc.FreezeMember(ctx, domain.FreezeMemberInput{
		TenantID:  "ws-1",
		UserEmail: "bob@example.com",
		HubUserID: "hub-u-bob",
		Reason:    domain.FreezeReasonQuotaSeatExceeded,
	})
	require.NoError(t, err)
	assert.False(t, alreadyFrozen, "first freeze should not be alreadyFrozen")
	assert.Equal(t, "ws-1", resp.TenantID)
	assert.Equal(t, "bob@example.com", resp.UserEmail)
	assert.Equal(t, domain.FreezeReasonQuotaSeatExceeded, resp.Reason)
}

func TestFreezeMember_Idempotent_AlreadyFrozen(t *testing.T) {
	svc, m, frozenRepo := newVeridianServiceWithFrozenRepo(t)
	ctx := context.Background()

	m.workspaceRepo.EXPECT().GetByID(ctx, "ws-1").Return(&domain.Workspace{ID: "ws-1"}, nil)
	m.user.EXPECT().GetUserByEmail(ctx, "bob@example.com").
		Return(&domain.User{ID: "bob-uuid", Email: "bob@example.com", Type: domain.UserTypeUser}, nil)
	m.workspaceRepo.EXPECT().GetUserWorkspace(ctx, "bob-uuid", "ws-1").
		Return(&domain.UserWorkspace{UserID: "bob-uuid", WorkspaceID: "ws-1", Role: "member"}, nil)

	// Repo signale alreadyFrozen=true. Pas de webhook emit attendu.
	frozenRepo.EXPECT().Freeze(ctx, "ws-1", "bob-uuid", domain.FreezeReasonManual).
		Return(&domain.VeridianFrozenMember{
			WorkspaceID: "ws-1",
			UserID:      "bob-uuid",
			Reason:      domain.FreezeReasonManual,
		}, true, nil)

	resp, alreadyFrozen, err := svc.FreezeMember(ctx, domain.FreezeMemberInput{
		TenantID:  "ws-1",
		UserEmail: "bob@example.com",
		HubUserID: "hub-u-bob",
		Reason:    domain.FreezeReasonManual,
	})
	require.NoError(t, err)
	assert.True(t, alreadyFrozen)
	assert.Equal(t, domain.FreezeReasonManual, resp.Reason)
}

func TestFreezeMember_OwnerRefused(t *testing.T) {
	svc, m, _ := newVeridianServiceWithFrozenRepo(t)
	ctx := context.Background()

	m.workspaceRepo.EXPECT().GetByID(ctx, "ws-1").Return(&domain.Workspace{ID: "ws-1"}, nil)
	m.user.EXPECT().GetUserByEmail(ctx, "owner@x.test").
		Return(&domain.User{ID: "owner-uuid", Email: "owner@x.test", Type: domain.UserTypeUser}, nil)
	m.workspaceRepo.EXPECT().GetUserWorkspace(ctx, "owner-uuid", "ws-1").
		Return(&domain.UserWorkspace{UserID: "owner-uuid", WorkspaceID: "ws-1", Role: "owner"}, nil)
	// Pas d'appel au repo Freeze ni au webhook.

	_, _, err := svc.FreezeMember(ctx, domain.FreezeMemberInput{
		TenantID:  "ws-1",
		UserEmail: "owner@x.test",
		HubUserID: "hub-u-owner",
	})
	require.Error(t, err)
	assert.True(t, errors.Is(err, ErrCannotFreezeOwner))
}

func TestFreezeMember_UserNotMember(t *testing.T) {
	svc, m, _ := newVeridianServiceWithFrozenRepo(t)
	ctx := context.Background()

	m.workspaceRepo.EXPECT().GetByID(ctx, "ws-1").Return(&domain.Workspace{ID: "ws-1"}, nil)
	m.user.EXPECT().GetUserByEmail(ctx, "ghost@x.test").
		Return(&domain.User{ID: "ghost-uuid", Email: "ghost@x.test", Type: domain.UserTypeUser}, nil)
	m.workspaceRepo.EXPECT().GetUserWorkspace(ctx, "ghost-uuid", "ws-1").
		Return(nil, sql.ErrNoRows)

	_, _, err := svc.FreezeMember(ctx, domain.FreezeMemberInput{
		TenantID:  "ws-1",
		UserEmail: "ghost@x.test",
		HubUserID: "hub-u-ghost",
	})
	require.Error(t, err)
	assert.True(t, errors.Is(err, ErrMemberNotInWorkspace))
}

func TestFreezeMember_UserUnknown_Treated_AsNotMember(t *testing.T) {
	svc, m, _ := newVeridianServiceWithFrozenRepo(t)
	ctx := context.Background()

	m.workspaceRepo.EXPECT().GetByID(ctx, "ws-1").Return(&domain.Workspace{ID: "ws-1"}, nil)
	m.user.EXPECT().GetUserByEmail(ctx, "unknown@x.test").
		Return(nil, &domain.ErrUserNotFound{Message: "not found"})

	_, _, err := svc.FreezeMember(ctx, domain.FreezeMemberInput{
		TenantID:  "ws-1",
		UserEmail: "unknown@x.test",
		HubUserID: "hub-u-?",
	})
	require.Error(t, err)
	assert.True(t, errors.Is(err, ErrMemberNotInWorkspace))
}

func TestFreezeMember_TenantNotFound(t *testing.T) {
	svc, m, _ := newVeridianServiceWithFrozenRepo(t)
	ctx := context.Background()

	m.workspaceRepo.EXPECT().GetByID(ctx, "ws-unknown").Return(nil, sql.ErrNoRows)

	_, _, err := svc.FreezeMember(ctx, domain.FreezeMemberInput{
		TenantID:  "ws-unknown",
		UserEmail: "bob@x.test",
		HubUserID: "u-1",
	})
	require.Error(t, err)
	assert.True(t, errors.Is(err, sql.ErrNoRows))
}

func TestFreezeMember_ValidationErrors(t *testing.T) {
	svc, _, _ := newVeridianServiceWithFrozenRepo(t)
	ctx := context.Background()

	cases := []struct {
		name  string
		input domain.FreezeMemberInput
	}{
		{"missing tenant", domain.FreezeMemberInput{UserEmail: "a@x"}},
		{"missing email", domain.FreezeMemberInput{TenantID: "ws-1"}},
	}
	for _, tc := range cases {
		_, _, err := svc.FreezeMember(ctx, tc.input)
		assert.Error(t, err, tc.name)
	}
}

func TestFreezeMember_RepoNotConfigured(t *testing.T) {
	svc, _ := newVeridianService(t)
	// PAS de frozenMemberRepo inject.

	_, _, err := svc.FreezeMember(context.Background(), domain.FreezeMemberInput{
		TenantID:  "ws-1",
		UserEmail: "a@x.test",
		HubUserID: "u-1",
	})
	require.Error(t, err)
	assert.True(t, errors.Is(err, ErrFrozenMemberRepoNotConfigured))
}

func TestFreezeMember_DefaultReason_ManualWhenEmpty(t *testing.T) {
	svc, m, frozenRepo := newVeridianServiceWithFrozenRepo(t)
	ctx := context.Background()

	m.workspaceRepo.EXPECT().GetByID(ctx, "ws-1").Return(&domain.Workspace{ID: "ws-1"}, nil)
	m.user.EXPECT().GetUserByEmail(ctx, "bob@x.test").
		Return(&domain.User{ID: "bob-uuid", Email: "bob@x.test", Type: domain.UserTypeUser}, nil)
	m.workspaceRepo.EXPECT().GetUserWorkspace(ctx, "bob-uuid", "ws-1").
		Return(&domain.UserWorkspace{UserID: "bob-uuid", WorkspaceID: "ws-1", Role: "member"}, nil)

	// Le service doit passer "manual" au repo quand input.Reason est vide.
	frozenRepo.EXPECT().Freeze(ctx, "ws-1", "bob-uuid", domain.FreezeReasonManual).
		Return(&domain.VeridianFrozenMember{
			WorkspaceID: "ws-1", UserID: "bob-uuid", Reason: domain.FreezeReasonManual,
		}, false, nil)

	m.emitter.EXPECT().Emit(ctx, domain.EventTenantMemberFrozen, "ws-1", gomock.Any()).Times(1)

	_, _, err := svc.FreezeMember(ctx, domain.FreezeMemberInput{
		TenantID:  "ws-1",
		UserEmail: "bob@x.test",
		HubUserID: "u-1",
		// Reason vide intentionnellement
	})
	require.NoError(t, err)
}

// === UnfreezeMember =========================================================

func TestUnfreezeMember_Success_WasFrozen(t *testing.T) {
	svc, m, frozenRepo := newVeridianServiceWithFrozenRepo(t)
	ctx := context.Background()

	m.workspaceRepo.EXPECT().GetByID(ctx, "ws-1").Return(&domain.Workspace{ID: "ws-1"}, nil)
	m.user.EXPECT().GetUserByEmail(ctx, "bob@x.test").
		Return(&domain.User{ID: "bob-uuid", Email: "bob@x.test", Type: domain.UserTypeUser}, nil)
	frozenRepo.EXPECT().Unfreeze(ctx, "ws-1", "bob-uuid").Return(true, nil)
	m.emitter.EXPECT().Emit(ctx, domain.EventTenantMemberUnfrozen, "ws-1", gomock.Any()).Times(1)

	resp, wasFrozen, err := svc.UnfreezeMember(ctx, domain.UnfreezeMemberInput{
		TenantID:  "ws-1",
		UserEmail: "bob@x.test",
		HubUserID: "u-1",
	})
	require.NoError(t, err)
	assert.True(t, wasFrozen)
	assert.Equal(t, "ws-1", resp.TenantID)
	assert.False(t, resp.UnfrozenAt.IsZero())
}

func TestUnfreezeMember_Idempotent_NotFrozen_NoWebhook(t *testing.T) {
	svc, m, frozenRepo := newVeridianServiceWithFrozenRepo(t)
	ctx := context.Background()

	m.workspaceRepo.EXPECT().GetByID(ctx, "ws-1").Return(&domain.Workspace{ID: "ws-1"}, nil)
	m.user.EXPECT().GetUserByEmail(ctx, "bob@x.test").
		Return(&domain.User{ID: "bob-uuid", Email: "bob@x.test", Type: domain.UserTypeUser}, nil)
	frozenRepo.EXPECT().Unfreeze(ctx, "ws-1", "bob-uuid").Return(false, nil)
	// PAS de webhook sur replay idempotent (evite spam Hub).

	resp, wasFrozen, err := svc.UnfreezeMember(ctx, domain.UnfreezeMemberInput{
		TenantID:  "ws-1",
		UserEmail: "bob@x.test",
		HubUserID: "u-1",
	})
	require.NoError(t, err)
	assert.False(t, wasFrozen, "replay should signal wasFrozen=false")
	assert.NotNil(t, resp)
}

func TestUnfreezeMember_UserUnknown_TreatedAsNotMember(t *testing.T) {
	svc, m, _ := newVeridianServiceWithFrozenRepo(t)
	ctx := context.Background()

	m.workspaceRepo.EXPECT().GetByID(ctx, "ws-1").Return(&domain.Workspace{ID: "ws-1"}, nil)
	m.user.EXPECT().GetUserByEmail(ctx, "ghost@x.test").
		Return(nil, &domain.ErrUserNotFound{Message: "not found"})

	_, _, err := svc.UnfreezeMember(ctx, domain.UnfreezeMemberInput{
		TenantID:  "ws-1",
		UserEmail: "ghost@x.test",
		HubUserID: "u-1",
	})
	require.Error(t, err)
	assert.True(t, errors.Is(err, ErrMemberNotInWorkspace))
}

func TestUnfreezeMember_TenantNotFound(t *testing.T) {
	svc, m, _ := newVeridianServiceWithFrozenRepo(t)
	ctx := context.Background()

	m.workspaceRepo.EXPECT().GetByID(ctx, "ws-unknown").Return(nil, sql.ErrNoRows)

	_, _, err := svc.UnfreezeMember(ctx, domain.UnfreezeMemberInput{
		TenantID:  "ws-unknown",
		UserEmail: "bob@x.test",
		HubUserID: "u-1",
	})
	require.Error(t, err)
	assert.True(t, errors.Is(err, sql.ErrNoRows))
}

func TestUnfreezeMember_RepoNotConfigured(t *testing.T) {
	svc, _ := newVeridianService(t)

	_, _, err := svc.UnfreezeMember(context.Background(), domain.UnfreezeMemberInput{
		TenantID:  "ws-1",
		UserEmail: "a@x.test",
		HubUserID: "u-1",
	})
	require.Error(t, err)
	assert.True(t, errors.Is(err, ErrFrozenMemberRepoNotConfigured))
}

func TestUnfreezeMember_ValidationErrors(t *testing.T) {
	svc, _, _ := newVeridianServiceWithFrozenRepo(t)
	ctx := context.Background()

	cases := []struct {
		name  string
		input domain.UnfreezeMemberInput
	}{
		{"missing tenant", domain.UnfreezeMemberInput{UserEmail: "a@x"}},
		{"missing email", domain.UnfreezeMemberInput{TenantID: "ws-1"}},
	}
	for _, tc := range cases {
		_, _, err := svc.UnfreezeMember(ctx, tc.input)
		assert.Error(t, err, tc.name)
	}
}

// === ConfigureFrozenMemberSupport ==========================================

func TestConfigureFrozenMemberSupport_Success(t *testing.T) {
	svc, _ := newVeridianService(t)
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	repo := mocks.NewMockVeridianFrozenMemberRepository(ctrl)

	err := ConfigureFrozenMemberSupport(svc, repo)
	require.NoError(t, err)
	assert.Equal(t, domain.VeridianFrozenMemberRepository(repo), svc.frozenMemberRepo)
}

// fakeVeridianService is a domain.VeridianService impl that is NOT
// *veridianService — used to exercise the ConfigureFrozenMemberSupport
// branch that refuses unknown implementations.
type fakeVeridianService struct {
	domain.VeridianService
}

func TestConfigureFrozenMemberSupport_RefusesUnknownImpl(t *testing.T) {
	fake := &fakeVeridianService{}
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	repo := mocks.NewMockVeridianFrozenMemberRepository(ctrl)

	err := ConfigureFrozenMemberSupport(fake, repo)
	require.Error(t, err)
}
