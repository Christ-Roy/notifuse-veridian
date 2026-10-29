package reconcile

import (
	"net/http"
	"testing"

	"github.com/Notifuse/notifuse/internal/domain"
	"github.com/Notifuse/notifuse/internal/service"
	"github.com/Notifuse/notifuse/internal/veridian/platform"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// incompleteHost : hôte d'un assemblage de test, sans service d'authentification ni dépôt.
type incompleteHost struct {
	platform.Host
	mux *http.ServeMux
}

func (h incompleteHost) Mux() *http.ServeMux                             { return h.mux }
func (h incompleteHost) AuthService() *service.AuthService               { return nil }
func (h incompleteHost) WorkspaceRepository() domain.WorkspaceRepository { return nil }

func capability(t *testing.T) platform.Capability {
	for _, c := range platform.Default.List() {
		if c.Name == "reconcile" {
			return c
		}
	}
	t.Fatal("capacité reconcile non enregistrée")
	return platform.Capability{}
}

func TestWire_IncompleteHostRegistersNothingAndDoesNotFail(t *testing.T) {
	mux := http.NewServeMux()
	require.NoError(t, capability(t).Wire(incompleteHost{mux: mux}), "un hôte incomplet ne doit jamais empêcher le démarrage")
	rec := newRecorder(mux, "POST", "/api/veridian/reconcile.run")
	assert.Equal(t, http.StatusNotFound, rec, "aucune route posée")
}

func TestWire_CapabilityIsNamedReconcile(t *testing.T) {
	assert.Equal(t, "reconcile", capability(t).Name)
	assert.Nil(t, capability(t).Start, "aucune boucle de fond ni tâche planifiée")
}

func newRecorder(mux *http.ServeMux, method, path string) int {
	req, _ := http.NewRequest(method, path, nil)
	_, pattern := mux.Handler(req)
	if pattern == "" {
		return http.StatusNotFound
	}
	return http.StatusOK
}
