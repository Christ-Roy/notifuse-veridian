package broadcast

import (
	"context"
	"errors"
	"testing"

	"github.com/Notifuse/notifuse/internal/domain"
	"github.com/Notifuse/notifuse/internal/domain/mocks"
	pkgmocks "github.com/Notifuse/notifuse/pkg/mocks"
	"github.com/golang/mock/gomock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Veridian fork — tests du resolver pixel avec fallback workspace. Le bug
// corrigé : avant ce resolver, les senders passaient workspace=nil à
// VeridianResolveOpenPixel, donc la config pixel posée au NIVEAU WORKSPACE
// (chemin UI Settings → Cold outreach) était persistée mais jamais appliquée.

func pixelTestLogger(ctrl *gomock.Controller) *pkgmocks.MockLogger {
	l := pkgmocks.NewMockLogger(ctrl)
	l.EXPECT().WithFields(gomock.Any()).Return(l).AnyTimes()
	l.EXPECT().WithField(gomock.Any(), gomock.Any()).Return(l).AnyTimes()
	l.EXPECT().Debug(gomock.Any()).AnyTimes()
	l.EXPECT().Info(gomock.Any()).AnyTimes()
	l.EXPECT().Warn(gomock.Any()).AnyTimes()
	l.EXPECT().Error(gomock.Any()).AnyTimes()
	return l
}

func TestVeridianWorkspacePixelResolver_NilRepoIsNoFallback(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	// repo nil = comportement avant-fix : workspace jamais chargé, fallback
	// workspace inactif. Hors tunnel → nil (non-régression upstream stricte).
	r := newVeridianWorkspacePixelResolver(nil, pixelTestLogger(ctrl))

	got := r.resolveOpenPixel(context.Background(), "ws-1",
		&domain.Contact{Email: "jean@gmail.com"}, "jean@gmail.com", &domain.Broadcast{}, nil)
	assert.Nil(t, got, "sans repo et hors tunnel : nil (comportement upstream)")

	// workspace() doit retourner nil sans jamais paniquer ni fetcher.
	assert.Nil(t, r.workspace(context.Background(), "ws-1"))
}

func TestVeridianWorkspacePixelResolver_NilResolverSafe(t *testing.T) {
	// Sécurité défensive : un resolver nil ne panique pas.
	var r *veridianWorkspacePixelResolver
	assert.Nil(t, r.workspace(context.Background(), "ws-1"))
}

func TestVeridianWorkspacePixelResolver_WorkspaceFallbackApplied(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	repo := mocks.NewMockWorkspaceRepository(ctrl)
	ws := &domain.Workspace{
		ID: "ws-1",
		Settings: domain.WorkspaceSettings{
			// Override workspace : pixel forcé OFF sur freemail_fr (qui est ON
			// par défaut tunnel). Prouve que le fallback workspace est appliqué.
			VeridianOpenPixelByClass: map[string]bool{"freemail_fr": false},
		},
	}
	// Le fetch ne doit avoir lieu qu'UNE fois malgré 3 résolutions (mémoïsation).
	repo.EXPECT().GetByID(gomock.Any(), "ws-1").Return(ws, nil).Times(1)

	r := newVeridianWorkspacePixelResolver(repo, pixelTestLogger(ctrl))

	for i := 0; i < 3; i++ {
		got := r.resolveOpenPixel(context.Background(), "ws-1",
			&domain.Contact{Email: "x@orange.fr"}, "x@orange.fr", &domain.Broadcast{}, nil)
		require.NotNil(t, got, "tunnel actif via override workspace → non-nil")
		assert.False(t, *got, "override workspace freemail_fr=false doit s'appliquer")
	}
}

func TestVeridianWorkspacePixelResolver_BroadcastPrimesOverWorkspace(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	repo := mocks.NewMockWorkspaceRepository(ctrl)
	ws := &domain.Workspace{
		ID:       "ws-1",
		Settings: domain.WorkspaceSettings{VeridianOpenPixelByClass: map[string]bool{"freemail_fr": false}},
	}
	repo.EXPECT().GetByID(gomock.Any(), "ws-1").Return(ws, nil).Times(1)

	r := newVeridianWorkspacePixelResolver(repo, pixelTestLogger(ctrl))

	// Le broadcast force ON sur freemail_fr : doit primer sur le workspace OFF.
	b := &domain.Broadcast{Metadata: domain.MapOfAny{
		domain.VeridianOpenPixelByClassMetadataKey: map[string]any{"freemail_fr": true},
	}}
	got := r.resolveOpenPixel(context.Background(), "ws-1",
		&domain.Contact{Email: "x@orange.fr"}, "x@orange.fr", b, nil)
	require.NotNil(t, got)
	assert.True(t, *got, "broadcast metadata doit primer sur le workspace")
}

func TestVeridianWorkspacePixelResolver_InfraPrimesOverWorkspace(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	repo := mocks.NewMockWorkspaceRepository(ctrl)
	ws := &domain.Workspace{
		ID:       "ws-1",
		Settings: domain.WorkspaceSettings{VeridianOpenPixelByClass: map[string]bool{"freemail_fr": true}},
	}
	repo.EXPECT().GetByID(gomock.Any(), "ws-1").Return(ws, nil).Times(1)

	r := newVeridianWorkspacePixelResolver(repo, pixelTestLogger(ctrl))

	// L'infra (EmailProvider) force OFF sur freemail_fr : doit primer sur le
	// workspace ON. Prouve que le niveau INFRA est bien câblé côté sender.
	infra := &domain.EmailProvider{
		VeridianOpenPixelByClass: map[string]bool{"freemail_fr": false},
	}
	got := r.resolveOpenPixel(context.Background(), "ws-1",
		&domain.Contact{Email: "x@orange.fr"}, "x@orange.fr", &domain.Broadcast{}, infra)
	require.NotNil(t, got)
	assert.False(t, *got, "infra OFF doit primer sur le workspace ON")
}

func TestVeridianWorkspacePixelResolver_FetchErrorDegradesToDefault(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	repo := mocks.NewMockWorkspaceRepository(ctrl)
	// Fetch échoue : best-effort → workspace nil, on dégrade vers le défaut
	// tunnel (jamais d'échec d'envoi pour un lookup de config pixel).
	repo.EXPECT().GetByID(gomock.Any(), "ws-1").Return(nil, errors.New("db down")).Times(1)

	r := newVeridianWorkspacePixelResolver(repo, pixelTestLogger(ctrl))

	// Tunnel actif via tag contact freemail_fr → défaut tunnel = ON, malgré
	// l'échec du fetch workspace.
	c := &domain.Contact{Email: "x@orange.fr", CustomString5: &domain.NullableString{String: "freemail_fr"}}
	got := r.resolveOpenPixel(context.Background(), "ws-1", c, "x@orange.fr", &domain.Broadcast{}, nil)
	require.NotNil(t, got, "tunnel actif (tag) → non-nil même si fetch workspace échoue")
	assert.True(t, *got, "défaut tunnel freemail_fr=ON appliqué malgré fetch KO")

	// Le résultat (nil workspace) est mémoïsé : pas de second fetch.
	assert.Nil(t, r.workspace(context.Background(), "ws-1"))
}

func TestVeridianWorkspacePixelResolver_RefetchOnDifferentWorkspaceID(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	repo := mocks.NewMockWorkspaceRepository(ctrl)
	repo.EXPECT().GetByID(gomock.Any(), "ws-1").Return(&domain.Workspace{ID: "ws-1"}, nil).Times(1)
	repo.EXPECT().GetByID(gomock.Any(), "ws-2").Return(&domain.Workspace{ID: "ws-2"}, nil).Times(1)

	r := newVeridianWorkspacePixelResolver(repo, pixelTestLogger(ctrl))

	w1 := r.workspace(context.Background(), "ws-1")
	require.NotNil(t, w1)
	assert.Equal(t, "ws-1", w1.ID)
	// Re-demander ws-1 ne refetch pas.
	assert.Same(t, w1, r.workspace(context.Background(), "ws-1"))
	// Un ID différent invalide le cache → nouveau fetch.
	w2 := r.workspace(context.Background(), "ws-2")
	require.NotNil(t, w2)
	assert.Equal(t, "ws-2", w2.ID)
}
