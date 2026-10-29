package app

import (
	"context"
	"database/sql"
	"fmt"
	"net/http"
	"time"

	"github.com/Notifuse/notifuse/config"
	"github.com/Notifuse/notifuse/internal/domain"
	"github.com/Notifuse/notifuse/internal/http/middleware"
	veridianmig "github.com/Notifuse/notifuse/internal/migrations/veridian"
	"github.com/Notifuse/notifuse/internal/service"
	"github.com/Notifuse/notifuse/internal/service/queue"
	"github.com/Notifuse/notifuse/internal/veridian/platform"
	"github.com/Notifuse/notifuse/pkg/logger"
)

// Point UNIQUE de câblage Veridian (lot L0, 29/10/2026). app.go l'appelle d'une seule ligne, en fin
// d'InitHandlers : tout le reste du code Veridian s'accroche ici, jamais dans app.go (fichier gelé,
// voir docs/veridian/MODULARITE.md).
//
// Ordre : (1) migrations Veridian manquantes (registre internal/migrations/veridian, suivi par
// numéro), (2) Wire de chaque capacité enregistrée (internal/veridian/<module>, par ordre de nom),
// (3) Start/Stop différés : Start une fois le serveur HTTP créé, Stop à l'annulation du contexte
// d'arrêt de l'application.

// WireVeridian câble toutes les capacités Veridian sur l'application initialisée.
func WireVeridian(a *App) error {
	return wireVeridian(a, veridianmig.Default, platform.Default)
}

func wireVeridian(a *App, migs *veridianmig.Registry, caps *platform.Registry) error {
	if len(migs.List()) > 0 {
		rep, err := migs.RunAll(context.Background(), a.db, workspaceSource{repo: a.workspaceRepo}, a.logger)
		if err != nil {
			return fmt.Errorf("veridian migrations: %w", err)
		}
		if len(rep.Failures) > 0 {
			// Journalisé en erreur par RunAll ; le démarrage continue (base orpheline ou injoignable).
			a.logger.WithField("failures", len(rep.Failures)).Error("veridian migrations: workspaces en échec, rejoués au prochain démarrage")
		}
	}

	host := &veridianHost{a: a}
	capabilities := caps.List()
	for _, c := range capabilities {
		if err := safeWire(c, host); err != nil {
			return fmt.Errorf("veridian capability %q: %w", c.Name, err)
		}
		a.logger.WithField("capability", c.Name).Info("Veridian capability wired")
	}
	if len(capabilities) > 0 {
		go runLifecycle(a, host, capabilities)
	}
	return nil
}

func safeWire(c platform.Capability, h platform.Host) (err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("panic: %v", r)
		}
	}()
	return c.Wire(h)
}

// runLifecycle démarre les capacités quand le serveur existe et les arrête à l'arrêt de l'application.
func runLifecycle(a *App, h platform.Host, caps []platform.Capability) {
	ctx := a.GetShutdownContext()
	if !waitServerCreated(ctx, a) {
		return
	}
	for _, c := range caps {
		if c.Start == nil {
			continue
		}
		go func(c platform.Capability) {
			defer func() {
				if r := recover(); r != nil {
					a.logger.WithField("capability", c.Name).WithField("panic", fmt.Sprint(r)).Error("Veridian capability Start panicked")
				}
			}()
			if err := c.Start(ctx, h); err != nil {
				a.logger.WithField("capability", c.Name).WithField("error", err).Error("Veridian capability Start failed")
			}
		}(c)
	}
	<-ctx.Done()
	stopCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	for _, c := range caps {
		if c.Stop == nil {
			continue
		}
		if err := c.Stop(stopCtx); err != nil {
			a.logger.WithField("capability", c.Name).WithField("error", err).Warn("Veridian capability Stop failed")
		}
	}
}

// waitServerCreated attend que Start() ait créé le serveur HTTP (sondage : le canal serverStarted
// d'App est refermé puis recréé par Start(), il ne se prête pas à une attente avant Start).
func waitServerCreated(ctx context.Context, a *App) bool {
	t := time.NewTicker(200 * time.Millisecond)
	defer t.Stop()
	for {
		if a.IsServerCreated() {
			return true
		}
		select {
		case <-ctx.Done():
			return false
		case <-t.C:
		}
	}
}

// workspaceSource adapte le dépôt de workspaces au runner de migrations.
type workspaceSource struct{ repo domain.WorkspaceRepository }

func (s workspaceSource) IDs(ctx context.Context) ([]string, error) {
	if s.repo == nil {
		return nil, nil
	}
	list, err := s.repo.List(ctx)
	if err != nil {
		return nil, err
	}
	ids := make([]string, 0, len(list))
	for _, w := range list {
		ids = append(ids, w.ID)
	}
	return ids, nil
}

func (s workspaceSource) Conn(ctx context.Context, id string) (*sql.DB, error) {
	return s.repo.GetConnection(ctx, id)
}

// veridianHost : adaptateur App -> platform.Host.
type veridianHost struct{ a *App }

var _ platform.Host = (*veridianHost)(nil)

func (h *veridianHost) Config() *config.Config { return h.a.config }
func (h *veridianHost) Logger() logger.Logger  { return h.a.logger }
func (h *veridianHost) DB() *sql.DB            { return h.a.db }
func (h *veridianHost) Mux() *http.ServeMux    { return h.a.mux }

func (h *veridianHost) JWTSecret() func() ([]byte, error) {
	return func() ([]byte, error) {
		if len(h.a.config.Security.JWTSecret) == 0 {
			return nil, fmt.Errorf("JWT secret not configured")
		}
		return h.a.config.Security.JWTSecret, nil
	}
}

func (h *veridianHost) RequireAuth() func(http.Handler) http.Handler {
	return middleware.NewAuthMiddleware(h.JWTSecret()).RequireAuth()
}

func (h *veridianHost) AuthService() *service.AuthService { return h.a.authService }

func (h *veridianHost) WorkspaceRepository() domain.WorkspaceRepository { return h.a.workspaceRepo }
func (h *veridianHost) ContactRepository() domain.ContactRepository     { return h.a.contactRepo }
func (h *veridianHost) MessageHistoryRepository() domain.MessageHistoryRepository {
	return h.a.messageHistoryRepo
}
func (h *veridianHost) EmailQueueRepository() domain.EmailQueueRepository { return h.a.emailQueueRepo }
func (h *veridianHost) EmailService() *service.EmailService               { return h.a.emailService }
func (h *veridianHost) WebhookEmitter() domain.WebhookEmitter             { return h.a.veridianWebhookEmitter }
func (h *veridianHost) IMAPPoller() *queue.VeridianIMAPPollerService      { return h.a.veridianIMAPPoller }
func (h *veridianHost) EmailQueueWorker() *queue.EmailQueueWorker         { return h.a.emailQueueWorker }
func (h *veridianHost) ShutdownContext() context.Context                  { return h.a.GetShutdownContext() }
