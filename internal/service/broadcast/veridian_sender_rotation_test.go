package broadcast

import (
	"context"
	"testing"

	"github.com/Notifuse/notifuse/internal/domain"
	"github.com/Notifuse/notifuse/internal/domain/mocks"
	"github.com/golang/mock/gomock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// rotationTestProvider : infra à 3 senders d'IDs triables (s1<s2<s3).
func rotationTestProvider() *domain.EmailProvider {
	return &domain.EmailProvider{
		Kind:               domain.EmailProviderKindSMTP,
		RateLimitPerMinute: 10,
		Senders: []domain.EmailSender{
			{ID: "s1", Email: "a@agences-veridian.fr", Name: "A", IsDefault: true},
			{ID: "s2", Email: "b@agences-veridian.fr", Name: "B"},
			{ID: "s3", Email: "c@agences-veridian.fr", Name: "C"},
		},
	}
}

// coldBroadcast : broadcast avec config cold (rates) → contexte cold détecté.
func coldBroadcast() *domain.Broadcast {
	return &domain.Broadcast{
		ID:       "bcast-1",
		Metadata: domain.MapOfAny{domain.VeridianProviderClassRatesMetadataKey: map[string]any{"google": 1.0}},
	}
}

func TestVeridianResolveSender_NonColdIsUpstream(t *testing.T) {
	p := rotationTestProvider()
	r := domain.NewVeridianSenderRotator()
	// Broadcast nu (pas de config cold), pas de tag contact → contexte non-cold →
	// pas de rotation, sender par défaut (s1) à chaque appel.
	plain := &domain.Broadcast{ID: "b", Metadata: domain.MapOfAny{}}
	for i := 0; i < 3; i++ {
		s := veridianResolveSender(context.Background(), r, nil, "ws-1", "int-1",
			p, "", nil, "x@gmail.com", plain)
		require.NotNil(t, s)
		assert.Equal(t, "s1", s.ID, "appel %d", i)
	}
}

func TestVeridianResolveSender_ColdRoundRobinByClass(t *testing.T) {
	p := rotationTestProvider()
	r := domain.NewVeridianSenderRotator()
	b := coldBroadcast()

	// google tourne s1→s2→s3, microsoft a son propre curseur.
	assert.Equal(t, "s1", veridianResolveSender(context.Background(), r, nil, "ws-1", "int-1", p, "", nil, "a@gmail.com", b).ID)
	assert.Equal(t, "s2", veridianResolveSender(context.Background(), r, nil, "ws-1", "int-1", p, "", nil, "b@gmail.com", b).ID)
	assert.Equal(t, "s1", veridianResolveSender(context.Background(), r, nil, "ws-1", "int-1", p, "", nil, "x@outlook.com", b).ID)
	assert.Equal(t, "s3", veridianResolveSender(context.Background(), r, nil, "ws-1", "int-1", p, "", nil, "c@gmail.com", b).ID)
}

func TestVeridianResolveSender_ContactTagActivatesCold(t *testing.T) {
	p := rotationTestProvider()
	r := domain.NewVeridianSenderRotator()
	// Pas de config broadcast, mais le contact porte un tag classe → contexte cold.
	plain := &domain.Broadcast{ID: "b", Metadata: domain.MapOfAny{}}
	contact := &domain.Contact{
		Email:         "y@gmail.com",
		CustomString5: &domain.NullableString{String: domain.ProviderClassGoogle, IsNull: false},
	}
	s := veridianResolveSender(context.Background(), r, nil, "ws-1", "int-1", p, "", contact, "y@gmail.com", plain)
	require.NotNil(t, s)
	assert.Equal(t, "s1", s.ID)
	// 2e appel même classe → s2.
	s2 := veridianResolveSender(context.Background(), r, nil, "ws-1", "int-1", p, "", contact, "y@gmail.com", plain)
	assert.Equal(t, "s2", s2.ID)
}

func TestVeridianResolveSender_TemplateSenderIDRespected(t *testing.T) {
	p := rotationTestProvider()
	r := domain.NewVeridianSenderRotator()
	b := coldBroadcast()
	// Même en cold, un SenderID de template explicite garde la main.
	for i := 0; i < 3; i++ {
		s := veridianResolveSender(context.Background(), r, nil, "ws-1", "int-1", p, "s3", nil, "a@gmail.com", b)
		require.NotNil(t, s)
		assert.Equal(t, "s3", s.ID)
	}
}

func TestVeridianResolveSender_NilRotatorIsUpstream(t *testing.T) {
	p := rotationTestProvider()
	b := coldBroadcast()
	// rotator nil → upstream (default sender) même en cold.
	for i := 0; i < 3; i++ {
		s := veridianResolveSender(context.Background(), nil, nil, "ws-1", "int-1", p, "", nil, "a@gmail.com", b)
		require.NotNil(t, s)
		assert.Equal(t, "s1", s.ID)
	}
}

func TestVeridianResolveSender_SingleSenderNoRotation(t *testing.T) {
	p := &domain.EmailProvider{
		Senders: []domain.EmailSender{{ID: "only", Email: "x@a.fr", Name: "X", IsDefault: true}},
	}
	r := domain.NewVeridianSenderRotator()
	b := coldBroadcast()
	s := veridianResolveSender(context.Background(), r, nil, "ws-1", "int-1", p, "", nil, "a@gmail.com", b)
	require.NotNil(t, s)
	assert.Equal(t, "only", s.ID)
}

func TestVeridianResolveSender_WorkspaceOnlyColdViaPixelResolver(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	p := rotationTestProvider()
	r := domain.NewVeridianSenderRotator()
	// Broadcast nu, pas de tag : le contexte cold n'est porté QUE par le workspace
	// settings. Le pixelResolver charge le workspace (mémoïsé) et active la rotation.
	plain := &domain.Broadcast{ID: "b", Metadata: domain.MapOfAny{}}

	ws := &domain.Workspace{
		ID: "ws-1",
		Settings: domain.WorkspaceSettings{
			VeridianProviderClassRates: map[string]float64{"google": 1},
		},
	}
	repo := mocks.NewMockWorkspaceRepository(ctrl)
	// Mémoïsé : un seul fetch attendu sur tout le batch.
	repo.EXPECT().GetByID(gomock.Any(), "ws-1").Return(ws, nil).Times(1)
	resolver := newVeridianWorkspacePixelResolver(repo, pixelTestLogger(ctrl))

	// Cold détecté via workspace → rotation active.
	assert.Equal(t, "s1", veridianResolveSender(context.Background(), r, resolver, "ws-1", "int-1", p, "", nil, "a@gmail.com", plain).ID)
	assert.Equal(t, "s2", veridianResolveSender(context.Background(), r, resolver, "ws-1", "int-1", p, "", nil, "b@gmail.com", plain).ID)
}
