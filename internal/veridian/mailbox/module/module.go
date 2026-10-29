// Package module branche la boîte unifiée sur l'application (capacité « mailbox » de la plateforme
// Veridian) : lecture des boîtes IMAP par rattrapage d'UID, capture du rendu exact des envois, routes /api/veridian/mail.*.
// Importé à blanc par internal/app/veridian_mod_mailbox.go.
package module

import (
	"context"
	"database/sql"
	"fmt"
	"sync"

	"github.com/Notifuse/notifuse/internal/domain"
	"github.com/Notifuse/notifuse/internal/service/queue"
	"github.com/Notifuse/notifuse/internal/veridian/mailbox"
	"github.com/Notifuse/notifuse/internal/veridian/mailbox/mailapi"
	"github.com/Notifuse/notifuse/internal/veridian/platform"
)

func init() {
	platform.Register(platform.Capability{Name: "mailbox", Wire: wire})
}

// secretCache garde la clé de chiffrement de chaque workspace (elle ne change pas en cours de vie).
type secretCache struct {
	repo domain.WorkspaceRepository
	mu   sync.RWMutex
	keys map[string]string
}

func (c *secretCache) get(ctx context.Context, ws string) (string, error) {
	c.mu.RLock()
	k, ok := c.keys[ws]
	c.mu.RUnlock()
	if ok {
		return k, nil
	}
	w, err := c.repo.GetByID(ctx, ws)
	if err != nil {
		return "", fmt.Errorf("lecture du workspace: %w", err)
	}
	if w.Settings.SecretKey == "" {
		return "", fmt.Errorf("workspace sans clé de chiffrement")
	}
	c.mu.Lock()
	c.keys[ws] = w.Settings.SecretKey
	c.mu.Unlock()
	return w.Settings.SecretKey, nil
}

type templateHost interface {
	TemplateService() domain.TemplateService
}

func wire(h platform.Host) error {
	repo := h.WorkspaceRepository()
	if repo == nil {
		return nil // assemblage de test sans dépôt : rien à câbler
	}
	log := h.Logger()
	sc := &secretCache{repo: repo, keys: map[string]string{}}
	store := mailbox.NewStore(
		func(ctx context.Context, ws string) (*sql.DB, error) { return repo.GetConnection(ctx, ws) },
		sc.get,
	)
	ingest := mailbox.NewIngestor(store, log, nil)

	var renderer mailbox.Renderer
	if th, ok := h.(templateHost); ok && th.TemplateService() != nil {
		renderer = mailbox.TemplateRenderer{Templates: th.TemplateService()}
	}
	if poller := h.IMAPPoller(); poller != nil {
		poller.RegisterMailboxSyncer(mailbox.NewSyncer(store, ingest, mailbox.NewDialer(), renderer, log))
	}
	queue.SetVeridianSentObserver(mailbox.NewSentCapture(ingest, log))

	if h.Mux() == nil || h.AuthService() == nil {
		return nil // assemblage de test sans routeur ni authentification : pas d'API
	}
	svc := &mailapi.Service{Store: store, Auth: mailapi.DomainAuthorizer{Auth: h.AuthService()}, WS: repo}
	if q := h.EmailQueueRepository(); q != nil {
		svc.Sender = &mailapi.QueueSender{Queue: q, WS: repo, Store: store}
	}
	(&mailapi.Handler{Svc: svc, Log: log, RequireAuth: h.RequireAuth()}).RegisterRoutes(h.Mux())
	return nil
}
