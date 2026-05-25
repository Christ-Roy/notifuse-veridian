package service

import (
	"context"
	"errors"
	"testing"

	"github.com/golang/mock/gomock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/Notifuse/notifuse/internal/domain"
	"github.com/Notifuse/notifuse/internal/domain/mocks"
)

// errWorkspaceNotFoundForMailProvider est une copie locale du sentinel
// repository.ErrWorkspaceNotFoundForMailProvider pour eviter l'import cycle
// service -> repository -> service (le package repository importe service
// via automation_postgres.go). On valide juste qu'un sentinel arbitraire est
// propage tel quel — le repo "vrai" est exerce dans veridian_mail_provider_postgres_test.go.
var errWorkspaceNotFoundForMailProvider = errors.New("workspace not found for mail provider lookup")

// stubMailProviderRepo implements domain.VeridianMailProviderRepository pour
// les tests service. Pas de mockgen dedie pour cette interface : surface
// minimale, stub manuel suffit + evite la dette de regen sur ajout de methode.
type stubMailProviderRepo struct {
	getResult domain.MailProviderChoice
	getErr    error
	setErr    error

	getCalls []string
	setCalls []struct {
		workspaceID string
		choice      domain.MailProviderChoice
	}
}

func (s *stubMailProviderRepo) GetMailProviderChoice(ctx context.Context, workspaceID string) (domain.MailProviderChoice, error) {
	s.getCalls = append(s.getCalls, workspaceID)
	return s.getResult, s.getErr
}

func (s *stubMailProviderRepo) SetMailProviderChoice(ctx context.Context, workspaceID string, choice domain.MailProviderChoice) error {
	s.setCalls = append(s.setCalls, struct {
		workspaceID string
		choice      domain.MailProviderChoice
	}{workspaceID, choice})
	return s.setErr
}

func TestNewVeridianMailProviderService_Constructor(t *testing.T) {
	repo := &stubMailProviderRepo{}
	svc := NewVeridianMailProviderService(repo, nil, nil)
	require.NotNil(t, svc)
	var _ VeridianMailProviderService = svc
}

func TestGetMailProviderChoice_Success(t *testing.T) {
	repo := &stubMailProviderRepo{getResult: domain.MailProviderHubGmail}
	svc := NewVeridianMailProviderService(repo, nil, nil)

	got, err := svc.GetMailProviderChoice(context.Background(), "ws-1")
	require.NoError(t, err)
	assert.Equal(t, domain.MailProviderHubGmail, got)
	assert.Equal(t, []string{"ws-1"}, repo.getCalls)
}

func TestGetMailProviderChoice_EmptyWorkspaceID(t *testing.T) {
	repo := &stubMailProviderRepo{}
	svc := NewVeridianMailProviderService(repo, nil, nil)

	_, err := svc.GetMailProviderChoice(context.Background(), "")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "workspace_id required")
	assert.Empty(t, repo.getCalls, "pas de roundtrip si workspace_id vide")
}

func TestGetMailProviderChoice_RepoError(t *testing.T) {
	repo := &stubMailProviderRepo{getErr: errors.New("db down")}
	svc := NewVeridianMailProviderService(repo, nil, nil)

	_, err := svc.GetMailProviderChoice(context.Background(), "ws-1")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "db down")
}

func TestSetMailProviderChoice_Success_EmitsWebhook(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	emitter := mocks.NewMockWebhookEmitter(ctrl)
	repo := &stubMailProviderRepo{getResult: domain.MailProviderSMTPGeneric}
	svc := NewVeridianMailProviderService(repo, emitter, nil)

	emitter.EXPECT().
		Emit(gomock.Any(), domain.EventTenantMailProviderChoiceChanged, "ws-1", gomock.Any()).
		Do(func(_ context.Context, _ domain.VeridianEvent, _ string, data map[string]interface{}) {
			assert.Equal(t, "smtp_generic", data["previous_choice"])
			assert.Equal(t, "hub_gmail", data["new_choice"])
			assert.Equal(t, "ws-1", data["workspace_id"])
			assert.Equal(t, "user", data["actor"])
		}).
		Times(1)

	resp, err := svc.SetMailProviderChoice(context.Background(), "ws-1", domain.MailProviderHubGmail)
	require.NoError(t, err)
	assert.Equal(t, "ws-1", resp.WorkspaceID)
	assert.Equal(t, domain.MailProviderHubGmail, resp.Choice)
	assert.NotEmpty(t, resp.UpdatedAt, "UpdatedAt doit etre rempli en RFC3339")

	require.Len(t, repo.setCalls, 1)
	assert.Equal(t, "ws-1", repo.setCalls[0].workspaceID)
	assert.Equal(t, domain.MailProviderHubGmail, repo.setCalls[0].choice)
}

func TestSetMailProviderChoice_InvalidChoice(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	emitter := mocks.NewMockWebhookEmitter(ctrl)
	// Aucun Emit attendu (rejet avant write).
	repo := &stubMailProviderRepo{}
	svc := NewVeridianMailProviderService(repo, emitter, nil)

	_, err := svc.SetMailProviderChoice(context.Background(), "ws-1", "microsoft_via_hub")
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrInvalidMailProviderChoice)
	assert.Empty(t, repo.setCalls, "pas d'ecriture sur choix invalide")
}

func TestSetMailProviderChoice_EmptyChoice_Invalid(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	emitter := mocks.NewMockWebhookEmitter(ctrl)
	repo := &stubMailProviderRepo{}
	svc := NewVeridianMailProviderService(repo, emitter, nil)

	_, err := svc.SetMailProviderChoice(context.Background(), "ws-1", "")
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrInvalidMailProviderChoice)
}

func TestSetMailProviderChoice_EmptyWorkspaceID(t *testing.T) {
	repo := &stubMailProviderRepo{}
	svc := NewVeridianMailProviderService(repo, nil, nil)

	_, err := svc.SetMailProviderChoice(context.Background(), "", domain.MailProviderHubGmail)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "workspace_id required")
}

func TestSetMailProviderChoice_WorkspaceNotFound_PropagatesError(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	emitter := mocks.NewMockWebhookEmitter(ctrl)
	// Pas de Emit attendu (mutation a echoue).
	repo := &stubMailProviderRepo{
		getResult: domain.MailProviderSMTPGeneric,
		setErr:    errWorkspaceNotFoundForMailProvider,
	}
	svc := NewVeridianMailProviderService(repo, emitter, nil)

	_, err := svc.SetMailProviderChoice(context.Background(), "missing", domain.MailProviderHubGmail)
	require.Error(t, err)
	assert.ErrorIs(t, err, errWorkspaceNotFoundForMailProvider)
}

func TestSetMailProviderChoice_NoEmitterIsSafe(t *testing.T) {
	// emitter nil = mode self-hosted sans Hub. Le service ne doit pas panic.
	repo := &stubMailProviderRepo{getResult: domain.MailProviderSMTPGeneric}
	svc := NewVeridianMailProviderService(repo, nil, nil)

	resp, err := svc.SetMailProviderChoice(context.Background(), "ws-1", domain.MailProviderHubGmail)
	require.NoError(t, err)
	require.NotNil(t, resp)
	assert.Equal(t, domain.MailProviderHubGmail, resp.Choice)
}

func TestSetMailProviderChoice_PreviousReadErrorDoesNotBlockMutation(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	emitter := mocks.NewMockWebhookEmitter(ctrl)
	repo := &stubMailProviderRepo{getErr: errors.New("transient db error")}
	svc := NewVeridianMailProviderService(repo, emitter, nil)

	// L'emit doit quand meme partir, avec previous_choice="" (vide).
	emitter.EXPECT().
		Emit(gomock.Any(), domain.EventTenantMailProviderChoiceChanged, "ws-1", gomock.Any()).
		Do(func(_ context.Context, _ domain.VeridianEvent, _ string, data map[string]interface{}) {
			assert.Equal(t, "", data["previous_choice"], "previous_choice doit etre vide si la read previous a fail")
			assert.Equal(t, "hub_gmail", data["new_choice"])
		}).
		Times(1)

	resp, err := svc.SetMailProviderChoice(context.Background(), "ws-1", domain.MailProviderHubGmail)
	require.NoError(t, err, "une read previous_choice qui fail ne doit pas bloquer la mutation")
	assert.Equal(t, domain.MailProviderHubGmail, resp.Choice)
}

func TestSetMailProviderChoice_Idempotent_StillEmitsForAuditTrail(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	emitter := mocks.NewMockWebhookEmitter(ctrl)
	// previous == new : on emit quand meme (le user a re-confirme).
	repo := &stubMailProviderRepo{getResult: domain.MailProviderHubGmail}
	svc := NewVeridianMailProviderService(repo, emitter, nil)

	emitter.EXPECT().
		Emit(gomock.Any(), domain.EventTenantMailProviderChoiceChanged, "ws-1", gomock.Any()).
		Do(func(_ context.Context, _ domain.VeridianEvent, _ string, data map[string]interface{}) {
			assert.Equal(t, "hub_gmail", data["previous_choice"])
			assert.Equal(t, "hub_gmail", data["new_choice"])
		}).
		Times(1)

	_, err := svc.SetMailProviderChoice(context.Background(), "ws-1", domain.MailProviderHubGmail)
	require.NoError(t, err)
}
