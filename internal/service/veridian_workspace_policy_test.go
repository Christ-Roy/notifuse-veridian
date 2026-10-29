package service

import (
	"context"
	"testing"
	"time"

	"github.com/golang/mock/gomock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/Notifuse/notifuse/config"
	"github.com/Notifuse/notifuse/internal/domain"
	pkgmocks "github.com/Notifuse/notifuse/pkg/mocks"
)

func TestVeridianGuardWorkspaceCreation(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	lg := pkgmocks.NewMockLogger(ctrl)
	lg.EXPECT().WithField(gomock.Any(), gomock.Any()).Return(lg).AnyTimes()
	lg.EXPECT().Warn(gomock.Any()).AnyTimes()
	user := &domain.User{Email: "root@example.com"}

	managed := &WorkspaceService{config: &config.Config{HubAPISecret: "secret"}, logger: lg}
	err := managed.veridianGuardWorkspaceCreation(context.Background(), user)
	var unauth *domain.ErrUnauthorized
	require.ErrorAs(t, err, &unauth, "mode gere par le Hub : la creation interactive est refusee")

	assert.NoError(t, managed.veridianGuardWorkspaceCreation(context.WithValue(context.Background(), domain.SystemCallKey, true), user),
		"un appel interne (SystemCallKey) passe")

	selfHosted := &WorkspaceService{config: &config.Config{}, logger: lg}
	assert.NoError(t, selfHosted.veridianGuardWorkspaceCreation(context.Background(), user), "sans HUB_API_SECRET : comportement upstream")
}

func TestVeridianApplyWorkspaceSettings(t *testing.T) {
	followups := true
	five := 5.0
	yes := true
	dst := domain.WorkspaceSettings{
		VeridianDecisionLogLevel: "full",
		VeridianThreadFollowups:  &followups,
		VeridianJitterPct:        &five,
	}
	in := domain.WorkspaceSettings{
		VeridianMarketingEmailProviderIDs: []string{"a", "b"},
		VeridianPerSenderDailyCap:         40,
		VeridianJitterPct:                 nil, // vide = effacer
		VeridianAntiHashEnabled:           &yes,
	}
	veridianApplyWorkspaceSettings(&dst, in)
	assert.Equal(t, []string{"a", "b"}, dst.VeridianMarketingEmailProviderIDs)
	assert.Equal(t, 40, dst.VeridianPerSenderDailyCap)
	require.NotNil(t, dst.VeridianAntiHashEnabled)
	assert.True(t, *dst.VeridianAntiHashEnabled)
	assert.Nil(t, dst.VeridianJitterPct, "nil efface la config")
	assert.Equal(t, "full", dst.VeridianDecisionLogLevel, "niveau du journal absent de la console : conserve")
	require.NotNil(t, dst.VeridianThreadFollowups)
	assert.True(t, *dst.VeridianThreadFollowups, "relances dans le fil absentes de la console : conservees")
}

func TestVeridianSettingsSnapshotAndUsage(t *testing.T) {
	existing := domain.WorkspaceSettings{VeridianMarketingEmailProviderIDs: []string{"a"}, TransactionalEmailProviderID: "t"}
	changed := existing
	changed.VeridianPerSenderDailyCap = 10
	snap := veridianSnapshotSettings(existing, changed)
	assert.True(t, snap.policyChanged)
	assert.Equal(t, []string{"a"}, snap.poolBefore)
	assert.Equal(t, "t", snap.transactionalBefore)
	// la copie du pool est independante : modifier l'original ne bouge pas l'instantane
	existing.VeridianMarketingEmailProviderIDs[0] = "z"
	assert.Equal(t, []string{"a"}, snap.poolBefore)

	assert.False(t, veridianSnapshotSettings(existing, existing).policyChanged)
	assert.False(t, veridianUsageSettingsChanged([]string{"z"}, "t", existing))
	assert.True(t, veridianUsageSettingsChanged([]string{"a"}, "t", existing))
	assert.True(t, veridianUsageSettingsChanged([]string{"z"}, "autre", existing))
}

func TestVeridianPrepareEmailProviders(t *testing.T) {
	verified := time.Now().UTC()
	p := &domain.EmailProvider{VeridianTransportVerifiedAt: &verified, VeridianCredentialsConfigured: true}
	veridianPrepareNewEmailProvider(p)
	assert.Nil(t, p.VeridianTransportVerifiedAt, "la verification est delivree par le serveur seulement")
	assert.False(t, p.VeridianCredentialsConfigured)

	smtp := func(host string) *domain.EmailProvider {
		return &domain.EmailProvider{Kind: domain.EmailProviderKindSMTP, SMTP: &domain.SMTPSettings{Host: host, Port: 587}}
	}
	current := smtp("smtp.a.example")
	current.VeridianTransportVerifiedAt = &verified

	same := smtp("smtp.a.example")
	veridianPrepareUpdatedEmailProvider(same, current)
	require.NotNil(t, same.VeridianTransportVerifiedAt, "transport inchange : la verification est conservee")

	moved := smtp("smtp.b.example")
	veridianPrepareUpdatedEmailProvider(moved, current)
	assert.Nil(t, moved.VeridianTransportVerifiedAt, "transport change : a re-verifier")
}

func TestVeridianMergeIMAPSettings(t *testing.T) {
	existing := &domain.IMAPSettings{EncryptedPassword: "enc"}
	assert.Same(t, existing, veridianMergeIMAPSettings(nil, existing), "rien fourni : on garde l'existant")
	assert.Nil(t, veridianMergeIMAPSettings(nil, nil))

	kept := veridianMergeIMAPSettings(&domain.IMAPSettings{}, existing)
	assert.Equal(t, "enc", kept.EncryptedPassword, "mot de passe non retape : chiffre existant preserve")

	typed := veridianMergeIMAPSettings(&domain.IMAPSettings{Password: "neuf"}, existing)
	assert.Equal(t, "", typed.EncryptedPassword, "mot de passe retape : pas de recopie de l'ancien")

	assert.NotNil(t, veridianMergeIMAPSettings(&domain.IMAPSettings{}, nil))
}

func TestVeridianValidateEmailIntegrationSkipsOtherTypes(t *testing.T) {
	assert.NoError(t, veridianValidateEmailIntegration(&domain.Workspace{}, &domain.Integration{Type: domain.IntegrationTypeLLM}))
}

func TestVeridianForgetIntegration(t *testing.T) {
	ws := &domain.Workspace{Settings: domain.WorkspaceSettings{
		VeridianMarketingEmailProviderIDs: []string{"a", "b"},
		MarketingEmailProviderID:          "a",
	}}
	veridianForgetIntegration(ws, "a")
	assert.Equal(t, []string{"b"}, ws.Settings.VeridianMarketingEmailProviderIDs)
	assert.Equal(t, "b", ws.Settings.MarketingEmailProviderID, "le profil marketing retombe sur le suivant du pool")
}

func TestVeridianWakeAfterPolicyChange(t *testing.T) {
	rec := &wakeRecorder{}
	s := &WorkspaceService{emailIntegrationPolicyRepo: rec}
	ws := &domain.Workspace{Integrations: []domain.Integration{{ID: "i1", Type: domain.IntegrationTypeEmail}, {ID: "l1", Type: domain.IntegrationTypeLLM}}}

	s.veridianWakeAfterPolicyChange(context.Background(), "w", ws, veridianSettingsSnapshot{policyChanged: false})
	assert.Empty(t, rec.woken, "politique inchangee : aucune entree reveillee")
	s.veridianWakeAfterPolicyChange(context.Background(), "w", ws, veridianSettingsSnapshot{policyChanged: true})
	assert.Equal(t, []string{"i1"}, rec.woken, "politique changee : seuls les profils email sont reveilles")
}
