package leader

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/Notifuse/notifuse/internal/domain"
)

type repo struct{ domain.WorkspaceRepository }

func (repo) List(context.Context) ([]*domain.Workspace, error) {
	return []*domain.Workspace{{ID: "ilumphotos"}}, nil
}

func (repo) GetByID(_ context.Context, id string) (*domain.Workspace, error) {
	return &domain.Workspace{ID: id}, nil
}

// Un worker sur un exemplaire qui ne mène pas ne voit aucun espace, donc ne prend aucune entrée ; le reste du dépôt passe.
func TestWorkspaces_VidesTantQuOnNeMenePas(t *testing.T) {
	ctx := context.Background()
	suiveur := Follow(repo{}, gate(false))
	ws, err := suiveur.List(ctx)
	require.NoError(t, err)
	assert.Empty(t, ws)
	one, err := suiveur.GetByID(ctx, "ilumphotos")
	require.NoError(t, err)
	assert.Equal(t, "ilumphotos", one.ID)

	ws, _ = Follow(repo{}, gate(true)).List(ctx)
	assert.Len(t, ws, 1)
	assert.Nil(t, Follow(nil, gate(true)))
	ws, _ = Workspaces{WorkspaceRepository: repo{}}.List(ctx)
	assert.Empty(t, ws, "sans élection câblée : ne mène pas (jamais deux leaders par défaut)")
}
