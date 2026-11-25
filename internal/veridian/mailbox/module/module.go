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
	"github.com/Notifuse/notifuse/internal/veridian/bounces"
	"github.com/Notifuse/notifuse/internal/veridian/boxsend"
	"github.com/Notifuse/notifuse/internal/veridian/mailbox"
	"github.com/Notifuse/notifuse/internal/veridian/mailbox/mailapi"
	"github.com/Notifuse/notifuse/internal/veridian/platform"
	"github.com/Notifuse/notifuse/internal/veridian/standing"
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

// NewStore rend un dépôt de la boîte unifiée pour les autres modules (même clé de chiffrement par workspace).
func NewStore(repo domain.WorkspaceRepository) *mailbox.Store {
	sc := &secretCache{repo: repo, keys: map[string]string{}}
	return mailbox.NewStore(func(ctx context.Context, ws string) (*sql.DB, error) { return repo.GetConnection(ctx, ws) }, sc.get)
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
	store := NewStore(repo)
	ingest := mailbox.NewIngestor(store, log, nil)
	// Boîtes louées (fiche 84) : un envoi par le SMTP d'une boîte est rangé ici.
	boxsend.SetRecorder(ingest)
	// VER-220 : chaque rapport de non-remise rangé ici nourrit la chaîne des rebonds (seule source).
	if ap := bounces.FromHost(h); ap != nil {
		ingest.SetBounceSink(ap)
		// Le worker relit l'inscription avant d'envoyer : un contact sorti (rebond, réponse) après
		// la mise en file ne reçoit pas le mail déjà en attente.
		if ap.Automations != nil {
			queue.SetVeridianEnrollmentReader(ap.Automations)
		}
	} else {
		log.Warn("Mailbox: bounce chain not available on this host, bounces are stored but not applied")
	}

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
	svc := &mailapi.Service{Store: store, Auth: mailapi.DomainAuthorizer{Auth: h.AuthService(), Standing: &standing.Store{DB: h.DB()}}, WS: repo}
	if q := h.EmailQueueRepository(); q != nil {
		svc.Sender = &mailapi.QueueSender{Queue: q, WS: repo, Store: store}
	}
	(&mailapi.Handler{Svc: svc, Log: log, RequireAuth: h.RequireAuth()}).RegisterRoutes(h.Mux())
	return nil
}
