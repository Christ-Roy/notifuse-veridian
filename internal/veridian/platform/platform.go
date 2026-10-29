package platform

import (
	"context"
	"database/sql"
	"fmt"
	"net/http"
	"sort"
	"sync"

	"github.com/Notifuse/notifuse/config"
	"github.com/Notifuse/notifuse/internal/domain"
	"github.com/Notifuse/notifuse/internal/service"
	"github.com/Notifuse/notifuse/internal/service/queue"
	"github.com/Notifuse/notifuse/pkg/logger"
)

// Host : tout ce qu'une capacité peut demander à l'application, rien de plus. Ajouter un accesseur
// ici (et son adaptateur dans veridian_wiring.go) est la seule façon d'exposer un nouvel élément.
// Les accesseurs peuvent renvoyer nil si l'élément n'est pas construit (tests, démo) : la capacité
// vérifie ce dont elle dépend dans Wire et renvoie une erreur claire sinon.
type Host interface {
	Config() *config.Config
	Logger() logger.Logger
	DB() *sql.DB // base système
	Mux() *http.ServeMux
	// RequireAuth : même middleware que les handlers (JWT utilisateur ou clé d'API).
	RequireAuth() func(http.Handler) http.Handler
	JWTSecret() func() ([]byte, error)
	AuthService() *service.AuthService

	WorkspaceRepository() domain.WorkspaceRepository
	ContactRepository() domain.ContactRepository
	MessageHistoryRepository() domain.MessageHistoryRepository
	EmailQueueRepository() domain.EmailQueueRepository
	EmailService() *service.EmailService
	WebhookEmitter() domain.WebhookEmitter

	IMAPPoller() *queue.VeridianIMAPPollerService
	EmailQueueWorker() *queue.EmailQueueWorker

	// ShutdownContext est annulé à l'arrêt de l'application : toute boucle de fond s'y arrête.
	ShutdownContext() context.Context
}

// Capability : une capacité Veridian.
type Capability struct {
	// Name : unique, minuscule, = nom du module (ex. "mailbox").
	Name string
	// Wire : appelé une fois, après les migrations Veridian, quand repos, services, mux et poller
	// existent. Enregistre routes, consommateurs, observateurs. Une erreur ou un panic empêche le
	// démarrage (échec rapide : le staging E2E l'attrape avant la production).
	Wire func(h Host) error
	// Start : optionnel. Appelé dans sa propre goroutine quand le serveur HTTP est démarré ; peut
	// bloquer (boucle) mais doit s'arrêter quand ctx est annulé. Un panic est journalisé, pas fatal.
	Start func(ctx context.Context, h Host) error
	// Stop : optionnel. Appelé (borné à quelques secondes) quand ctx de l'application est annulé.
	Stop func(ctx context.Context) error
}

// Registry conserve les capacités.
type Registry struct {
	mu   sync.RWMutex
	caps map[string]Capability
}

// NewRegistry crée un registre vide.
func NewRegistry() *Registry { return &Registry{caps: map[string]Capability{}} }

// Default est le registre du processus.
var Default = NewRegistry()

// Register enregistre dans le registre du processus (panic si invalide : détecté par go test).
func Register(c Capability) {
	if err := Default.Register(c); err != nil {
		panic("veridian platform: " + err.Error())
	}
}

// Register valide et enregistre une capacité.
func (r *Registry) Register(c Capability) error {
	if c.Name == "" {
		return fmt.Errorf("capacité sans nom")
	}
	if c.Wire == nil {
		return fmt.Errorf("capacité %q sans Wire", c.Name)
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, dup := r.caps[c.Name]; dup {
		return fmt.Errorf("capacité %q enregistrée deux fois", c.Name)
	}
	r.caps[c.Name] = c
	return nil
}

// List renvoie les capacités triées par nom (ordre de câblage déterministe).
func (r *Registry) List() []Capability {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]Capability, 0, len(r.caps))
	for _, c := range r.caps {
		out = append(out, c)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}
