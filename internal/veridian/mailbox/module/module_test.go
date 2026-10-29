package module

import (
	"context"
	"testing"

	"github.com/Notifuse/notifuse/internal/domain"
	"github.com/Notifuse/notifuse/internal/domain/mocks"
	"github.com/Notifuse/notifuse/internal/veridian/platform"
	"github.com/golang/mock/gomock"
)

func workspaceWithKey(k string) *domain.Workspace {
	return &domain.Workspace{ID: "ws1", Settings: domain.WorkspaceSettings{SecretKey: k}}
}

func TestCapabilityIsRegistered(t *testing.T) {
	found := false
	for _, c := range platform.Default.List() {
		if c.Name == "mailbox" {
			found = true
			if c.Wire == nil {
				t.Fatal("Wire manquant")
			}
		}
	}
	if !found {
		t.Fatal("la capacité mailbox n'est pas enregistrée (import à blanc manquant ?)")
	}
}

func TestSecretCacheReadsOnce(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	repo := mocks.NewMockWorkspaceRepository(ctrl)
	repo.EXPECT().GetByID(gomock.Any(), "ws1").Return(workspaceWithKey("k1"), nil).Times(1)
	c := &secretCache{repo: repo, keys: map[string]string{}}
	for i := 0; i < 3; i++ {
		k, err := c.get(context.Background(), "ws1")
		if err != nil || k != "k1" {
			t.Fatalf("%q %v", k, err)
		}
	}
}

func TestSecretCacheRefusesWorkspaceWithoutKey(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	repo := mocks.NewMockWorkspaceRepository(ctrl)
	repo.EXPECT().GetByID(gomock.Any(), "ws1").Return(workspaceWithKey(""), nil)
	c := &secretCache{repo: repo, keys: map[string]string{}}
	if _, err := c.get(context.Background(), "ws1"); err == nil {
		t.Fatal("un workspace sans clé ne doit jamais stocker de corps en clair")
	}
}
